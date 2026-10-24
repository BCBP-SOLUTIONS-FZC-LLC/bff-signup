package service

// PaidSignupService handles SB-5: POST /api/v1/signup/paid (Phase 1.5, LLD §19).
//
// Flow (Commercial Domain HLD §8.3, D-2 resolved):
//   CAPTCHA → rate limit → validate → allocate (payment_pending)
//   → Billing.AuthorizeAndCharge (payment_confirmed)
//   → RP-2 provision-direct (identity_created)
//   → I-1 provision-tenant (completed | tenant_pending)
//
// The service is only wired when PAID_SIGNUP_ENABLED=true; the /paid route
// returns 404 until that flag is set.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

// PaidSignupInput is the validated SB-5 request.
type PaidSignupInput struct {
	CompanyName   string
	Slug          string
	AdminName     string
	AdminEmail    string
	Password      string // plain; zeroed after hashing (SB-INV-2)
	Plan          domain.Plan
	Seats         int    // initial seat count; minimum 1
	PaymentToken  string // Stripe payment-method token, browser-tokenised — no PAN
	AcceptTerms   bool
	TermsVersion  string
	CaptchaToken  string
	DefaultLocale string
	ClientIP      string
	UserAgent     string
	Session       *port.Session
}

// PaidSignupResult is the SB-5 response payload.
type PaidSignupResult struct {
	SignupID              string
	State                 domain.HandoffState
	EmailVerificationSent bool
	SlugSuggestions       []string
}

// PaidSignupService orchestrates the direct-paid signup provisioning sequence.
type PaidSignupService struct {
	handoffs            port.HandoffStore
	limiter             port.RateLimiter
	captcha             port.CaptchaVerifier
	hasher              port.PasswordHasher
	billing             port.BillingClient
	rp                  port.RealmProvisioner
	om                  port.OrgMembership
	idgen               port.IDGenerator
	clock               port.Clock
	hmacKey             []byte
	reserved            map[string]bool
	policy              domain.PasswordPolicy
	currentTermsVersion string
}

func NewPaidSignupService(
	handoffs port.HandoffStore,
	limiter port.RateLimiter,
	captcha port.CaptchaVerifier,
	hasher port.PasswordHasher,
	billing port.BillingClient,
	rp port.RealmProvisioner,
	om port.OrgMembership,
	idgen port.IDGenerator,
	clock port.Clock,
	hmacKey []byte,
	reserved map[string]bool,
	policy domain.PasswordPolicy,
	currentTermsVersion string,
) *PaidSignupService {
	return &PaidSignupService{
		handoffs: handoffs, limiter: limiter, captcha: captcha, hasher: hasher,
		billing: billing, rp: rp, om: om, idgen: idgen, clock: clock,
		hmacKey: hmacKey, reserved: reserved, policy: policy,
		currentTermsVersion: currentTermsVersion,
	}
}

// Submit processes a direct paid signup request (SB-5, LLD §19).
func (s *PaidSignupService) Submit(ctx context.Context, in PaidSignupInput) (*PaidSignupResult, error) {
	// ── 1. CAPTCHA (SB-INV-5) ────────────────────────────────────────────────
	if err := s.captcha.Verify(ctx, in.CaptchaToken, in.ClientIP); err != nil {
		return nil, err
	}

	// ── 2. Rate limits ────────────────────────────────────────────────────────
	emailHMAC := emailHMAC(in.AdminEmail, s.hmacKey)

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

	// ── 3. Validation ─────────────────────────────────────────────────────────
	if err := s.validate(in); err != nil {
		return nil, err
	}
	termsAcceptedAt := s.clock.Now()

	slug := in.Slug
	if slug == "" {
		slug = domain.DeriveSlug(in.CompanyName)
	}

	// ── 4. Hash password (SB-INV-2) ──────────────────────────────────────────
	seed, err := s.hasher.Hash(in.Password)
	if err != nil {
		return nil, domain.NewError(domain.ErrInternal, "password hashing failed")
	}
	seedJSON, err := seedJSON(seed)
	if err != nil {
		return nil, domain.NewError(domain.ErrInternal, "credential serialisation failed")
	}

	// ── 5. Allocate handoff (SB-INV-3: one tenant_id per attempt) ────────────
	tenantID := s.idgen.NewUUIDv7()
	signupID := in.Session.SignupID
	now := s.clock.Now()

	h := &domain.Handoff{
		SignupID:       signupID,
		TenantID:       tenantID,
		Type:           domain.SignupTypePaid,
		State:          domain.StatePaymentPending,
		Slug:           slug,
		CompanyName:    in.CompanyName,
		Plan:           in.Plan,
		DefaultLocale:  in.DefaultLocale,
		AdminEmailHMAC: emailHMAC,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

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

	// ── 6. Billing: authorize and charge (Commercial HLD §8.3) ───────────────
	chargeResp, err := s.billing.AuthorizeAndCharge(ctx, port.ChargeRequest{
		TenantID:       tenantID,
		PaymentToken:   in.PaymentToken,
		Plan:           string(in.Plan),
		Seats:          in.Seats,
		IdempotencyKey: fmt.Sprintf("charge:%s", tenantID),
	})
	if err != nil {
		// Billing failure is terminal for this attempt — delete so the user can retry.
		_ = s.handoffs.Delete(ctx, signupID)
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "payment service unavailable")
	}

	h.BillingSubscriptionID = chargeResp.BillingSubscriptionID
	h.State = domain.StatePaymentConfirmed
	h.UpdatedAt = s.clock.Now()
	if err := s.handoffs.Update(ctx, h); err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "handoff store unavailable")
	}

	termsAcceptance := port.TermsAcceptance{
		Version:    in.TermsVersion,
		AcceptedAt: termsAcceptedAt.UTC().Format(time.RFC3339),
		IPAddress:  in.ClientIP,
		UserAgent:  in.UserAgent,
	}

	// ── 7. Slug pre-check (same PUB-1 guard as trial, fail-open) ─────────────
	if err := s.om.CheckSlugFree(ctx, slug); err != nil {
		suggestions := domain.SuggestSlugs(slug, in.CompanyName, nil)
		h.State = domain.StateSlugConflict
		h.UpdatedAt = s.clock.Now()
		_ = s.handoffs.Update(ctx, h)
		return &PaidSignupResult{
			SignupID:        signupID,
			State:           domain.StateSlugConflict,
			SlugSuggestions: suggestions,
		}, err
	}

	// ── 8. RP-2: provision-direct (creates dedicated realm + Keycloak user) ──
	// RP-2 runs before I-1 so owner_user_id is available (mirrors SB-INV-4).
	rpResp, err := s.rp.ProvisionDirect(ctx, port.ProvisionDirectRequest{
		TenantID:              tenantID,
		TenantName:            in.CompanyName,
		TenantSlug:            slug,
		AdminEmail:            domain.NormaliseEmail(in.AdminEmail),
		AdminName:             strings.TrimSpace(in.AdminName),
		HashedPasswordSeed:    seedJSON,
		Plan:                  string(in.Plan),
		BillingSubscriptionID: chargeResp.BillingSubscriptionID,
		TermsAcceptance:       termsAcceptance,
	}, fmt.Sprintf("signup:%s:rp2", tenantID))
	if err != nil {
		if isDomainCause(err, domain.ErrEmailUnavailable) {
			_ = s.handoffs.Delete(ctx, signupID)
		}
		return nil, err
	}

	h.KeycloakUserID = rpResp.KeycloakUserID
	h.State = domain.StateIdentityCreated
	h.UpdatedAt = s.clock.Now()
	if err := s.handoffs.Update(ctx, h); err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "handoff store unavailable")
	}

	// ── 9. I-1: provision-tenant (inline with retries, same as trial path) ───
	// Re-use the trial orchestrator's runI1 via a shared HandoffOrchestrator.
	// The orchestrator is wired identically for both paths.
	orchestrator := &HandoffOrchestrator{
		handoffs: s.handoffs,
		rp:       s.rp,
		om:       s.om,
		clock:    s.clock,
	}
	i1Result, err := orchestrator.runI1(ctx, h)
	if err != nil {
		if i1Result != nil && isDomainCause(err, domain.ErrSlugTaken) {
			return &PaidSignupResult{
				SignupID:        signupID,
				State:           i1Result.State,
				SlugSuggestions: i1Result.SlugSuggestions,
			}, err
		}
		return nil, err
	}

	return &PaidSignupResult{
		SignupID:              signupID,
		State:                 i1Result.State,
		EmailVerificationSent: rpResp.EmailVerificationSent,
	}, nil
}

func (s *PaidSignupService) validate(in PaidSignupInput) error {
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
	if in.Seats < 1 {
		return domain.NewError(domain.ErrValidationFailed, "seats must be at least 1")
	}
	if strings.TrimSpace(in.PaymentToken) == "" {
		return domain.NewError(domain.ErrValidationFailed, "payment_token is required")
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
