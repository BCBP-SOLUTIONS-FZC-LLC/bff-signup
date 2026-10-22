package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

const (
	maxSignupsPerIPPerHour    = 3
	maxAttemptsPerEmailPerDay = 5
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
	Session       *port.Session
}

// SignupResult is the SB-2 response payload.
type SignupResult struct {
	SignupID              string
	State                 domain.HandoffState
	EmailVerificationSent bool
	SlugSuggestions       []string // set when State == slug_conflict
}

// SignupService handles SB-2: POST /api/v1/signup/trial (LLD §5.4).
type SignupService struct {
	handoffs     port.HandoffStore
	limiter      port.RateLimiter
	captcha      port.CaptchaVerifier
	hasher       port.PasswordHasher
	orchestrator *HandoffOrchestrator
	idgen        port.IDGenerator
	clock        port.Clock
	hmacKey      []byte
	reserved     map[string]bool
	policy       domain.PasswordPolicy
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
) *SignupService {
	return &SignupService{
		handoffs: handoffs, limiter: limiter, captcha: captcha, hasher: hasher,
		orchestrator: orchestrator, idgen: idgen, clock: clock,
		hmacKey: hmacKey, reserved: reserved, policy: policy,
	}
}

// Submit processes a trial signup request (SB-2, LLD §5.4).
// The sequence: session validation → CAPTCHA → rate limit → validation →
// resubmit check → lock → handoff → orchestrate.
func (s *SignupService) Submit(ctx context.Context, in SignupInput) (*SignupResult, error) {
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
	if ipCount > int64(maxSignupsPerIPPerHour) {
		return nil, domain.NewError(domain.ErrRateLimited, "too many signups from this IP").
			WithDetails(map[string]any{"retry_after_seconds": retryAfterSecs(ipReset), "scope": "ip"})
	}

	emailCount, emailReset, err := s.limiter.IncrEmail(ctx, emailHMAC)
	if err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "rate limiter unavailable")
	}
	if emailCount > int64(maxAttemptsPerEmailPerDay) {
		return nil, domain.NewError(domain.ErrRateLimited, "too many attempts for this email").
			WithDetails(map[string]any{"retry_after_seconds": retryAfterSecs(emailReset), "scope": "email"})
	}

	// ── 3. Server-side validation ─────────────────────────────────────────────
	if err := s.validate(in); err != nil {
		return nil, err
	}

	// Derive slug if not supplied
	slug := in.Slug
	if slug == "" {
		slug = domain.DeriveSlug(in.CompanyName)
	}

	// ── 4. Resubmit / resume check ────────────────────────────────────────────
	existing, _ := s.handoffs.Get(ctx, in.Session.SignupID)
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
	defer s.handoffs.ReleaseLock(ctx, signupID, lockToken) //nolint:errcheck

	if err := s.handoffs.Create(ctx, h); err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "handoff store unavailable")
	}

	// ── 7. Orchestrate provisioning ───────────────────────────────────────────
	orcResult, err := s.orchestrator.Run(ctx, OrchestratorInput{
		Handoff:            h,
		AdminEmail:         domain.NormaliseEmail(in.AdminEmail),
		AdminName:          strings.TrimSpace(in.AdminName),
		HashedPasswordSeed: seedJSON,
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
		sameDetails := existing.AdminEmailHMAC == emailHMAC &&
			existing.Slug == slug &&
			existing.CompanyName == in.CompanyName &&
			existing.Plan == in.Plan
		if sameDetails {
			// Retry with the same tenant_id and RP-1 idempotency key
			seed, _ := s.hasher.Hash(in.Password)
			sj, _ := seedJSON(seed)
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
		// Different details while RP-1 not confirmed → discard and start fresh
		_ = s.handoffs.Delete(ctx, existing.SignupID)
		return nil, false, nil

	case domain.StateIdentityCreated, domain.StateTenantPending, domain.StateSlugConflict:
		sameDetails := existing.AdminEmailHMAC == emailHMAC
		if !sameDetails {
			return nil, true, domain.NewError(domain.ErrInvalidState, "signup details changed after identity was created").
				WithDetails(map[string]any{"reason": "details_changed"})
		}
		// Resume — no new CAPTCHA hit or rate-limit increment
		return &SignupResult{SignupID: existing.SignupID, State: existing.State}, true, nil

	case domain.StateCompleted:
		if existing.AdminEmailHMAC == emailHMAC {
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
	if err := domain.ValidateAdminEmail(in.AdminEmail); err != nil {
		return err
	}
	if err := domain.ValidatePassword(in.Password, in.AdminEmail, s.policy); err != nil {
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
	return nil
}

func (s *SignupService) emailHMAC(email string) string {
	mac := hmac.New(sha256.New, s.hmacKey)
	mac.Write([]byte(strings.ToLower(email)))
	return hex.EncodeToString(mac.Sum(nil))
}

func retryAfterSecs(resetAt time.Time) int {
	d := time.Until(resetAt)
	if d < 0 {
		return 1
	}
	return int(d.Seconds()) + 1
}
