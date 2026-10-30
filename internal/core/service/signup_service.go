package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/metrics"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

// SignupInput is the validated SB-2 request (LLD §5.4).
type SignupInput struct {
	CompanyName   string
	Slug          string // may be empty → derived
	AdminName     string
	AdminEmail    string
	Password      string // plain; zeroed after hashing (SB-INV-2)
	Plan          domain.Plan
	AcceptTerms   bool
	TermsVersion  string
	CaptchaToken  string
	DefaultLocale string
	ClientIP      string
	UserAgent     string // browser User-Agent, max 512 bytes (A-4)
	Session       *port.Session
}

// SignupResult is the SB-2 response payload.
type SignupResult struct {
	SignupID              string
	State                 domain.HandoffState
	EmailVerificationSent bool
	SlugSuggestions       []string // set when State == slug_conflict
}

// maxSignupsPerIPPerHour and maxAttemptsPerEmailPerDay are the default limits.
// SignupService uses configurable fields; PaidSignupService (Phase 1.5, on hold) references
// these package-level constants until billing is wired.
const (
	maxSignupsPerIPPerHour    = 3
	maxAttemptsPerEmailPerDay = 5
)

// SignupService handles SB-2: POST /api/v1/signup/trial (LLD §5.4).
type SignupService struct {
	handoffs               port.HandoffStore
	limiter                port.RateLimiter
	captcha                port.CaptchaVerifier
	hasher                 port.PasswordHasher
	orchestrator           *HandoffOrchestrator
	idgen                  port.IDGenerator
	clock                  port.Clock
	hmacKey                []byte
	reserved               map[string]bool
	policy                 domain.PasswordPolicy
	currentTermsVersion    string
	signupsPerIPPerHour    int
	attemptsPerEmailPerDay int
}

func NewSignupService(
	handoffs port.HandoffStore,
	limiter port.RateLimiter,
	captcha port.CaptchaVerifier,
	hasher port.PasswordHasher,
	orchestrator *HandoffOrchestrator,
	idgen port.IDGenerator,
	clock port.Clock,
	hmacKey []byte,
	reserved map[string]bool,
	policy domain.PasswordPolicy,
	currentTermsVersion string,
	signupsPerIPPerHour int,
	attemptsPerEmailPerDay int,
) *SignupService {
	return &SignupService{
		handoffs: handoffs, limiter: limiter, captcha: captcha, hasher: hasher,
		orchestrator: orchestrator, idgen: idgen, clock: clock,
		hmacKey: hmacKey, reserved: reserved, policy: policy,
		currentTermsVersion: currentTermsVersion,
		signupsPerIPPerHour: signupsPerIPPerHour, attemptsPerEmailPerDay: attemptsPerEmailPerDay,
	}
}

// Submit processes a trial signup request (SB-2, LLD §5.4).
// The sequence: session validation → CAPTCHA → rate limit → validation →
// resubmit check → lock → handoff → orchestrate.
func (s *SignupService) Submit(ctx context.Context, in SignupInput) (*SignupResult, error) {
	in.CompanyName = domain.NormaliseCompanyName(in.CompanyName)
	metrics.IncFormSubmitted("trial") // LLD §11.2

	// ── 1. CAPTCHA (SB-INV-5: runs before any internal call or CPU work) ─────
	if err := s.captcha.Verify(ctx, in.CaptchaToken, in.ClientIP); err != nil {
		return nil, err
	}

	// ── 2. Rate limits (counted after CAPTCHA — G-2) ─────────────────────────
	emailHMAC := s.emailHMAC(in.AdminEmail)

	ipCount, ipReset, err := s.limiter.IncrIP(ctx, in.ClientIP)
	if err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "rate limiter unavailable")
	}
	if ipCount > int64(s.signupsPerIPPerHour) {
		metrics.IncRateLimited("ip") // LLD §11.2
		return nil, domain.NewError(domain.ErrRateLimited, "too many signups from this IP").
			WithDetails(map[string]any{"retry_after_seconds": retryAfterSecs(ipReset), "scope": "ip"})
	}

	emailCount, emailReset, err := s.limiter.IncrEmail(ctx, emailHMAC)
	if err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "rate limiter unavailable")
	}
	if emailCount > int64(s.attemptsPerEmailPerDay) {
		metrics.IncRateLimited("email") // LLD §11.2
		return nil, domain.NewError(domain.ErrRateLimited, "too many attempts for this email").
			WithDetails(map[string]any{"retry_after_seconds": retryAfterSecs(emailReset), "scope": "email"})
	}

	// ── 3. Server-side validation ─────────────────────────────────────────────
	if err := s.validate(in); err != nil {
		return nil, err
	}
	// Record server time at the moment BFF validated accept_terms: true (A-4, LLD §10.6).
	termsAcceptedAt := s.clock.Now()

	// Derive slug if not supplied
	slug := in.Slug
	if slug == "" {
		slug = domain.DeriveSlug(in.CompanyName)
	}

	// ── 4. Resubmit / resume check ────────────────────────────────────────────
	existing, err := s.handoffs.Get(ctx, in.Session.SignupID)
	if err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "handoff store unavailable")
	}
	if existing != nil {
		result, handled, err := s.handleResubmit(ctx, existing, in, slug, emailHMAC)
		if handled {
			return result, err
		}
		// existing is rejected / none → fall through to new attempt
	}

	// ── 5. Hash password (CPU-heavy; after CAPTCHA per SB-INV-5) ─────────────
	seed, err := s.hasher.Hash(in.Password)
	if err != nil {
		return nil, domain.NewError(domain.ErrInternal, "password hashing failed")
	}
	seedJSON, err := seedJSON(seed)
	if err != nil {
		return nil, domain.NewError(domain.ErrInternal, "credential serialisation failed")
	}

	// ── 6. Allocate handoff ───────────────────────────────────────────────────
	tenantID := s.idgen.NewUUIDv7()
	signupID := in.Session.SignupID
	now := s.clock.Now()

	h := &domain.Handoff{
		SignupID:       signupID,
		TenantID:       tenantID,
		State:          domain.StateAllocated,
		Slug:           slug,
		CompanyName:    in.CompanyName,
		Plan:           in.Plan,
		DefaultLocale:  in.DefaultLocale,
		AdminEmailHMAC: emailHMAC,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	// Acquire distributed lock
	lockToken, acquired, err := s.handoffs.AcquireLock(ctx, signupID)
	if err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "handoff store unavailable")
	}
	if !acquired {
		return nil, domain.NewError(domain.ErrInvalidState, "another request is already processing this signup").
			WithDetails(map[string]any{"retry_after_ms": 500})
	}
	defer func() {
		// Lock TTL is 15 s — a release failure is non-fatal; the lock expires on its own.
		// Error is intentionally ignored here; Valkey outage is surfaced via health metrics.
		_ = s.handoffs.ReleaseLock(ctx, signupID, lockToken)
	}()

	if err := s.handoffs.Create(ctx, h); err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "handoff store unavailable")
	}

	// ── 7. Orchestrate provisioning ───────────────────────────────────────────
	orcResult, err := s.orchestrator.Run(ctx, OrchestratorInput{
		Handoff:            h,
		AdminEmail:         domain.NormaliseEmail(in.AdminEmail),
		AdminName:          strings.TrimSpace(in.AdminName),
		HashedPasswordSeed: seedJSON,
		TermsAcceptance: port.TermsAcceptance{
			Version:    in.TermsVersion,
			AcceptedAt: termsAcceptedAt.UTC().Format(time.RFC3339),
			IPAddress:  in.ClientIP,
			UserAgent:  in.UserAgent,
		},
	})
	if err != nil {
		// Slug conflict: return the result (with suggestions) alongside the error
		// so the handler can include suggestions in the 409 response.
		if orcResult != nil && isDomainCause(err, domain.ErrSlugTaken) {
			return &SignupResult{
				SignupID:        signupID,
				State:           orcResult.State,
				SlugSuggestions: orcResult.SlugSuggestions,
			}, err
		}
		return nil, err
	}

	return &SignupResult{
		SignupID:              signupID,
		State:                 orcResult.State,
		EmailVerificationSent: orcResult.EmailVerificationSent,
		SlugSuggestions:       orcResult.SlugSuggestions,
	}, nil
}

// handleResubmit decides what to do when SB-2 is called on a session that
// already has a handoff (G-1, LLD §5.4).
func (s *SignupService) handleResubmit(
	ctx context.Context,
	existing *domain.Handoff,
	in SignupInput,
	slug, emailHMAC string,
) (*SignupResult, bool, error) {
	switch existing.State { //nolint:exhaustive
	case domain.StateRejected, "":
		return nil, false, nil // treat as new attempt

	case domain.StateAllocated:
		sameDetails := hmacEqual(existing.AdminEmailHMAC, emailHMAC) &&
			existing.Slug == slug &&
			existing.CompanyName == in.CompanyName &&
			existing.Plan == in.Plan
		if sameDetails {
			// Retry with the same tenant_id and RP-1 idempotency key
			metrics.IncHandoffResumed("resubmit") // LLD §11.2
			seed, err := s.hasher.Hash(in.Password)
			if err != nil {
				return nil, true, domain.NewError(domain.ErrInternal, "password hashing failed")
			}
			sj, err := seedJSON(seed)
			if err != nil {
				return nil, true, domain.NewError(domain.ErrInternal, "credential serialisation failed")
			}
			orcResult, err := s.orchestrator.Run(ctx, OrchestratorInput{
				Handoff:            existing,
				AdminEmail:         domain.NormaliseEmail(in.AdminEmail),
				AdminName:          strings.TrimSpace(in.AdminName),
				HashedPasswordSeed: sj,
			})
			if err != nil {
				if orcResult != nil && isDomainCause(err, domain.ErrSlugTaken) {
					return &SignupResult{
						SignupID:        existing.SignupID,
						State:           orcResult.State,
						SlugSuggestions: orcResult.SlugSuggestions,
					}, true, err
				}
				return nil, true, err
			}
			return &SignupResult{SignupID: existing.SignupID, State: orcResult.State,
				SlugSuggestions: orcResult.SlugSuggestions}, true, nil
		}
		// Different details while RP-1 not confirmed → discard and start fresh.
		// Acquire the lock first to avoid deleting while the original request's
		// orchestrator is still running (G-1, LLD §9.2).
		lockToken, acquired, _ := s.handoffs.AcquireLock(ctx, existing.SignupID)
		if !acquired {
			return nil, true, domain.NewError(domain.ErrInvalidState, "another request is processing this signup").
				WithDetails(map[string]any{"retry_after_ms": 500})
		}
		_ = s.handoffs.ReleaseLock(ctx, existing.SignupID, lockToken)
		_ = s.handoffs.Delete(ctx, existing.SignupID)
		return nil, false, nil

	case domain.StateIdentityCreated, domain.StateTenantPending, domain.StateSlugConflict:
		sameDetails := hmacEqual(existing.AdminEmailHMAC, emailHMAC)
		if !sameDetails {
			return nil, true, domain.NewError(domain.ErrInvalidState, "signup details changed after identity was created").
				WithDetails(map[string]any{"reason": "details_changed"})
		}
		// Resume — no new CAPTCHA hit or rate-limit increment
		return &SignupResult{SignupID: existing.SignupID, State: existing.State}, true, nil

	case domain.StateCompleted:
		if hmacEqual(existing.AdminEmailHMAC, emailHMAC) {
			return &SignupResult{SignupID: existing.SignupID, State: domain.StateCompleted,
				EmailVerificationSent: true}, true, nil
		}
		return nil, true, domain.NewError(domain.ErrInvalidState, "signup already completed with different details").
			WithDetails(map[string]any{"reason": "details_changed"})
	}
	return nil, false, nil
}

func (s *SignupService) validate(in SignupInput) error {
	if err := domain.ValidateCompanyName(in.CompanyName); err != nil {
		return err
	}
	if in.Slug != "" {
		if err := domain.ValidateSlug(in.Slug, s.reserved); err != nil {
			return err
		}
	}
	if err := domain.ValidateAdminName(in.AdminName); err != nil {
		return err
	}
	normEmail := domain.NormaliseEmail(in.AdminEmail)
	if err := domain.ValidateAdminEmail(normEmail); err != nil {
		return err
	}
	if err := domain.ValidatePassword(in.Password, normEmail, s.policy); err != nil {
		return err
	}
	if !domain.ValidPlan(in.Plan) {
		return domain.NewError(domain.ErrValidationFailed, "plan must be starter, pro, or enterprise")
	}
	if !in.AcceptTerms {
		return domain.NewError(domain.ErrValidationFailed, "terms must be accepted")
	}
	if in.TermsVersion == "" {
		return domain.NewError(domain.ErrValidationFailed, "terms_version is required")
	}
	if s.currentTermsVersion != "" && in.TermsVersion != s.currentTermsVersion {
		return domain.NewError(domain.ErrValidationFailed, "terms_version is not the current published version").
			WithDetails(map[string]any{"current_version": s.currentTermsVersion})
	}
	return nil
}

func (s *SignupService) emailHMAC(email string) string {
	return emailHMAC(email, s.hmacKey)
}

// emailHMAC computes HMAC-SHA256 of the lower-cased email (SB-INV-2 / LLD §4.2).
// The key is SBFF_HMAC_KEY. Used by both trial and paid signup paths.
func emailHMAC(email string, key []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(strings.ToLower(email)))
	return hex.EncodeToString(mac.Sum(nil))
}

// hmacEqual compares two HMAC hex strings in constant time to avoid timing side-channels.
func hmacEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func retryAfterSecs(resetAt time.Time) int {
	d := time.Until(resetAt)
	if d < 0 {
		return 1
	}
	return int(d.Seconds()) + 1
}
