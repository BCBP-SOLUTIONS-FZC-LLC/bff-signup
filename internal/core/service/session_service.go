package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/metrics"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

const (
	sidBytes  = 32 // 256-bit random session id
	csrfBytes = 32 // 256-bit CSRF token
)

// SessionService handles SB-1: GET /api/v1/signup/session (LLD §5.3).
type SessionService struct {
	sessions             port.SessionStore
	limiter              port.RateLimiter
	idgen                port.IDGenerator
	clock                port.Clock
	sessionsPerIPPerHour int
}

func NewSessionService(
	sessions port.SessionStore,
	limiter port.RateLimiter,
	idgen port.IDGenerator,
	clock port.Clock,
	sessionsPerIPPerHour int,
) *SessionService {
	return &SessionService{sessions: sessions, limiter: limiter, idgen: idgen, clock: clock, sessionsPerIPPerHour: sessionsPerIPPerHour}
}

// SessionResult is the SB-1 response payload.
type SessionResult struct {
	SID       string // Set-Cookie value (never returned in JSON body)
	SignupID  string
	CSRFToken string
}

// Create starts a new signup session. Rate-limited to 20 per IP per hour.
func (s *SessionService) Create(ctx context.Context, clientIP string) (*SessionResult, error) {
	// Rate limit session creation per IP
	count, resetAt, err := s.limiter.IncrSession(ctx, clientIP)
	if err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "rate limiter unavailable")
	}
	if count > int64(s.sessionsPerIPPerHour) {
		metrics.IncRateLimited("session") // LLD §11.2
		retryAfter := int(time.Until(resetAt).Seconds()) + 1
		return nil, domain.NewError(domain.ErrRateLimited, "too many sessions from this IP").
			WithDetails(map[string]any{"retry_after_seconds": retryAfter, "scope": "session"})
	}

	// Generate 256-bit random session id
	sidRaw := make([]byte, sidBytes)
	if _, err := rand.Read(sidRaw); err != nil {
		return nil, domain.NewError(domain.ErrInternal, "failed to generate session id")
	}
	sid := base64.RawURLEncoding.EncodeToString(sidRaw)

	// Generate 256-bit CSRF token
	csrfRaw := make([]byte, csrfBytes)
	if _, err := rand.Read(csrfRaw); err != nil {
		return nil, domain.NewError(domain.ErrInternal, "failed to generate CSRF token")
	}
	csrfToken := base64.RawURLEncoding.EncodeToString(csrfRaw)
	csrfHash := hashCSRF(csrfToken)

	signupID := s.idgen.NewULID()

	sess := &port.Session{
		SID:       sid,
		CSRFHash:  csrfHash,
		SignupID:  signupID,
		CreatedAt: s.clock.Now().Unix(),
	}

	if err := s.sessions.Create(ctx, sess); err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "session store unavailable")
	}

	metrics.IncFormStarted("trial") // LLD §11.2 — mode="trial" in Phase 1; "paid" in Phase 1.5
	return &SessionResult{
		SID:       sid,
		SignupID:  signupID,
		CSRFToken: csrfToken,
	}, nil
}

// ValidateCSRF checks the session cookie and X-CSRF-Token header match.
// It also refreshes the session TTL on success.
func (s *SessionService) ValidateCSRF(ctx context.Context, sid, csrfToken string) (*port.Session, error) {
	if sid == "" {
		return nil, domain.NewError(domain.ErrSessionExpired, "missing session cookie")
	}
	sess, err := s.sessions.Get(ctx, sid)
	if err != nil {
		// A store failure (Valkey down) is not an expired session. Fail closed with
		// 503 dependency_unavailable so the client retries instead of restarting (N-7).
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "session store unavailable")
	}
	if sess == nil {
		return nil, domain.NewError(domain.ErrSessionExpired, "session expired or not found")
	}
	if csrfToken == "" || subtle.ConstantTimeCompare([]byte(hashCSRF(csrfToken)), []byte(sess.CSRFHash)) != 1 {
		return nil, domain.NewError(domain.ErrCSRFInvalid, "CSRF token mismatch")
	}
	// Refresh sliding TTL
	_ = s.sessions.Refresh(ctx, sid)
	return sess, nil
}

// GetSession returns the session for sid, ErrSessionExpired when it is missing,
// or ErrDependencyUnavailable when the session store cannot be read.
func (s *SessionService) GetSession(ctx context.Context, sid string) (*port.Session, error) {
	if sid == "" {
		return nil, domain.NewError(domain.ErrSessionExpired, "missing session cookie")
	}
	sess, err := s.sessions.Get(ctx, sid)
	if err != nil {
		// A store failure (Valkey down) is not an expired session. Fail closed with
		// 503 dependency_unavailable so the client retries instead of restarting (N-7).
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "session store unavailable")
	}
	if sess == nil {
		return nil, domain.NewError(domain.ErrSessionExpired, "session expired or not found")
	}
	// Slide the 30-min TTL on every read (I-02): polling SB-3 keeps the session alive.
	_ = s.sessions.Refresh(ctx, sid)
	return sess, nil
}

func hashCSRF(token string) string {
	h := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", h)
}
