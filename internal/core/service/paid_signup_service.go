package service

// PaidSignupService handles SB-5: POST /api/v1/signup/paid (direct paid signup, LLD §19).
//
// Flow (Commercial Domain HLD §8.3, D-2 resolved):
//   CAPTCHA → rate limit → validate → allocate (payment_pending)
//   → Billing.AuthorizeAndCharge (payment_confirmed)
//   → RP-2 provision-direct (identity_created)
//   → I-1 provision-tenant (completed | tenant_pending)
//
// The service is always constructed; only the /paid route is gated: it returns
// 404 until PAID_SIGNUP_ENABLED=true (Billing go-live step 4, LLD §19.5).

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/metrics"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/tracing"
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
	// Set only when State is payment_action_required: the browser completes
	// 3-D Secure with PaymentAction.ClientSecret, then re-POSTs SB-5 unchanged.
	PaymentAction    *port.PaymentAction
	PaymentExpiresAt string
	// PaymentHoldUntil: the handoff is kept until then (Billing's expires_at +
	// domain.PaymentHoldGrace); the handler holds the session as long.
	PaymentHoldUntil time.Time
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
	orchestrator        *HandoffOrchestrator
	idgen               port.IDGenerator
	clock               port.Clock
	hmacKey             []byte
	reserved            map[string]bool
	policy              domain.PasswordPolicy
	currentTermsVersion string
	// Rate limits (RATE_SIGNUPS_PER_IP_PER_HOUR, RATE_ATTEMPTS_PER_EMAIL_PER_DAY).
	// Defaults match the config defaults; main.go sets them with WithRateLimits.
	signupsPerIPPerHour    int
	attemptsPerEmailPerDay int
}

func NewPaidSignupService(
	handoffs port.HandoffStore,
	limiter port.RateLimiter,
	captcha port.CaptchaVerifier,
	hasher port.PasswordHasher,
	billing port.BillingClient,
	rp port.RealmProvisioner,
	om port.OrgMembership,
	orchestrator *HandoffOrchestrator,
	idgen port.IDGenerator,
	clock port.Clock,
	hmacKey []byte,
	reserved map[string]bool,
	policy domain.PasswordPolicy,
	currentTermsVersion string,
) *PaidSignupService {
	return &PaidSignupService{
		handoffs: handoffs, limiter: limiter, captcha: captcha, hasher: hasher,
		billing: billing, rp: rp, om: om, orchestrator: orchestrator,
		idgen: idgen, clock: clock,
		hmacKey: hmacKey, reserved: reserved, policy: policy,
		currentTermsVersion:    currentTermsVersion,
		signupsPerIPPerHour:    maxSignupsPerIPPerHour,
		attemptsPerEmailPerDay: maxAttemptsPerEmailPerDay,
	}
}

// WithRateLimits sets the configured SB-5 rate limits (the same RATE_* values
// SB-2 uses). Non-positive values keep the defaults.
func (s *PaidSignupService) WithRateLimits(signupsPerIPPerHour, attemptsPerEmailPerDay int) *PaidSignupService {
	if signupsPerIPPerHour > 0 {
		s.signupsPerIPPerHour = signupsPerIPPerHour
	}
	if attemptsPerEmailPerDay > 0 {
		s.attemptsPerEmailPerDay = attemptsPerEmailPerDay
	}
	return s
}

// Submit processes a direct paid signup request (SB-5, LLD §19).
//
// A repeat SB-5 on the same session resumes the prior paid attempt instead of
// allocating a new tenant (SB-INV-3, BL-OQ-8): Billing keys B-I1 on
// "charge:<tenant_id>", so every retry (after a BFF timeout, a Billing 503, or
// 3-D Secure) must reach Billing with the same tenant_id, key and body, or a
// second charge could start.
func (s *PaidSignupService) Submit(ctx context.Context, in PaidSignupInput) (*PaidSignupResult, error) {
	in.CompanyName = domain.NormaliseCompanyName(in.CompanyName)
	signupID := in.Session.SignupID
	emailHMAC := emailHMAC(in.AdminEmail, s.hmacKey)
	metrics.IncFormSubmitted("paid") // LLD §11.2
	// Business attributes on the request span (LLD §11.3). IDs and state only.
	tracing.Annotate(ctx, "signup.id", signupID, "signup.type", "paid")

	// ── 0. Prior attempt on this session ─────────────────────────────────────
	// Cheap read of the session's own key (not attacker-chosen). A read error
	// is ignored here; step 5 reads again under the lock and returns 503.
	prior, _ := s.handoffs.Get(ctx, signupID)
	if prior.BlocksOtherSignupType(domain.SignupTypePaid) {
		return nil, otherSignupInProgress(prior)
	}
	if prior != nil && prior.Type == domain.SignupTypePaid && hmacEqual(prior.AdminEmailHMAC, emailHMAC) {
		// Identity already exists: report progress only. No downstream call, so
		// no CAPTCHA and no rate-limit budget (same rule as SB-2, OBS-40).
		switch prior.State { //nolint:exhaustive
		case domain.StateIdentityCreated, domain.StateTenantPending, domain.StateCompleted:
			return &PaidSignupResult{SignupID: signupID, State: prior.State, EmailVerificationSent: true}, nil
		case domain.StateSlugConflict:
			return &PaidSignupResult{
				SignupID:        signupID,
				State:           domain.StateSlugConflict,
				SlugSuggestions: domain.SuggestSlugs(prior.Slug, prior.CompanyName, nil),
			}, domain.NewError(domain.ErrSlugTaken, "slug is taken; choose a new one with SB-4")
		}
	}
	// A payment resume (3-D Secure re-POST or retry) still calls Billing, so it
	// needs CAPTCHA (SB-INV-5), but it is the same attempt and does not use up
	// rate-limit budget.
	paymentResume := prior != nil && prior.Type == domain.SignupTypePaid &&
		(prior.IsPaymentResumable() || prior.State == domain.StatePaymentConfirmed)

	// ── 1. CAPTCHA (SB-INV-5) ────────────────────────────────────────────────
	if err := s.captcha.Verify(ctx, in.CaptchaToken, in.ClientIP); err != nil {
		return nil, err
	}

	// ── 2. Rate limits ────────────────────────────────────────────────────────
	if !paymentResume {
		if err := s.checkRateLimits(ctx, in.ClientIP, emailHMAC); err != nil {
			return nil, err
		}
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
	paymentRefHMAC := hmacHex(in.PaymentToken, s.hmacKey)

	// A resume whose details cannot match is refused before the costly hash.
	// The authoritative check repeats under the lock (step 5).
	if paymentResume {
		if err := samePaymentDetails(prior, emailHMAC, in.Plan, in.Seats, paymentRefHMAC, slug, in.CompanyName); err != nil {
			return nil, err
		}
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

	// ── 5. Allocate or resume the handoff (SB-INV-3: one tenant_id per attempt) ─
	lockToken, acquired, err := s.handoffs.AcquireLock(ctx, signupID)
	if err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "handoff store unavailable")
	}
	if !acquired {
		return nil, domain.NewError(domain.ErrInvalidState, "another request is already processing this signup").
			WithDetails(map[string]any{"retry_after_ms": 500})
	}
	defer s.handoffs.ReleaseLock(ctx, signupID, lockToken) //nolint:errcheck

	current, err := s.handoffs.Get(ctx, signupID)
	if err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "handoff store unavailable")
	}
	// A trial attempt on this session (written by SB-2 since step 0) is never
	// overwritten: it may already own a Keycloak user or a tenant.
	if current.BlocksOtherSignupType(domain.SignupTypePaid) {
		return nil, otherSignupInProgress(current)
	}

	if current != nil && current.Type == domain.SignupTypePaid &&
		!current.IsPaymentResumable() && current.State != domain.StatePaymentConfirmed {
		// The paid attempt has moved past payment (identity created, tenant
		// pending, slug conflict, completed or failed). Allocating a new tenant_id
		// here would start a second charge (SB-INV-3). Same rule as SB-2 (G-1).
		if hmacEqual(current.AdminEmailHMAC, emailHMAC) {
			return &PaidSignupResult{SignupID: signupID, State: current.State, EmailVerificationSent: true}, nil
		}
		return nil, domain.NewError(domain.ErrInvalidState, "signup details changed after payment").
			WithDetails(map[string]any{"reason": "details_changed"})
	}

	var h *domain.Handoff
	if current != nil && current.Type == domain.SignupTypePaid &&
		(current.IsPaymentResumable() || current.State == domain.StatePaymentConfirmed) {
		if err := samePaymentDetails(current, emailHMAC, in.Plan, in.Seats, paymentRefHMAC, slug, in.CompanyName); err != nil {
			return nil, err
		}
		h = current
		// Slug, name, locale (and, once paid, email) are not part of the B-I1 body,
		// so the resumed attempt takes the new values — unless RP-2 has already been
		// called (samePaymentDetails then requires them unchanged).
		h.AdminEmailHMAC = emailHMAC
		h.Slug = slug
		h.CompanyName = in.CompanyName
		h.DefaultLocale = in.DefaultLocale
		h.UpdatedAt = s.clock.Now()
		if err := s.handoffs.Update(ctx, h); err != nil {
			return nil, domain.NewError(domain.ErrDependencyUnavailable, "handoff store unavailable")
		}
	} else {
		now := s.clock.Now()
		h = &domain.Handoff{
			SignupID:       signupID,
			TenantID:       s.idgen.NewUUIDv7(),
			Type:           domain.SignupTypePaid,
			State:          domain.StatePaymentPending,
			Slug:           slug,
			CompanyName:    in.CompanyName,
			Plan:           in.Plan,
			DefaultLocale:  in.DefaultLocale,
			AdminEmailHMAC: emailHMAC,
			PaymentRefHMAC: paymentRefHMAC,
			LicensedSeats:  in.Seats,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := s.handoffs.Create(ctx, h); err != nil {
			return nil, domain.NewError(domain.ErrDependencyUnavailable, "handoff store unavailable")
		}
	}
	tenantID := h.TenantID
	tracing.Annotate(ctx, "tenant.id", tenantID, "handoff.state", string(h.State))

	// ── 6. Billing B-I1: authorize and charge (BL-OQ-8) ──────────────────────
	if h.State != domain.StatePaymentConfirmed {
		res, err := s.charge(ctx, h, in.PaymentToken)
		if err != nil || res != nil {
			return res, err
		}
	}

	termsAcceptance := port.TermsAcceptance{
		Version:    in.TermsVersion,
		AcceptedAt: termsAcceptedAt.UTC().Format(time.RFC3339),
		IPAddress:  in.ClientIP,
		UserAgent:  in.UserAgent,
	}

	// ── 7. Slug pre-check (same PUB-1 guard as trial, fail-open) ─────────────
	// The handoff stays in payment_confirmed: no Keycloak user exists yet, so
	// SB-4 cannot apply a new slug. The browser resubmits SB-5 with a new slug;
	// that resume skips Billing and reuses the paid subscription.
	if err := s.om.CheckSlugFree(ctx, slug); err != nil {
		suggestions := domain.SuggestSlugs(slug, in.CompanyName, nil)
		return &PaidSignupResult{
			SignupID:        signupID,
			State:           domain.StateSlugConflict,
			SlugSuggestions: suggestions,
		}, err
	}

	// ── 8. RP-2: provision-direct (creates dedicated realm + Keycloak user) ──
	// RP-2 runs before I-1 so owner_user_id is available (mirrors SB-INV-4).
	// Mark the attempt first: from now on the key signup:<tenant_id>:rp2 may have
	// a result at RP, which RP replays by key, so slug, name and email must not
	// change on a resubmit (samePaymentDetails) — or the realm and the tenant
	// would disagree (SB-INV-8).
	if !h.RP2Attempted {
		h.RP2Attempted = true
		h.UpdatedAt = s.clock.Now()
		if err := s.handoffs.Update(ctx, h); err != nil {
			return nil, domain.NewError(domain.ErrDependencyUnavailable, "handoff store unavailable")
		}
	}
	rpResp, err := s.rp.ProvisionDirect(ctx, port.ProvisionDirectRequest{
		TenantID:              tenantID,
		TenantName:            in.CompanyName,
		TenantSlug:            slug,
		AdminEmail:            domain.NormaliseEmail(in.AdminEmail),
		AdminName:             strings.TrimSpace(in.AdminName),
		HashedPasswordSeed:    seedJSON,
		Plan:                  string(in.Plan),
		BillingSubscriptionID: h.BillingSubscriptionID,
		TermsAcceptance:       termsAcceptance,
	}, fmt.Sprintf("signup:%s:rp2", tenantID))
	if err != nil {
		return s.rp2Failed(ctx, h, slug, in.CompanyName, err)
	}

	h.KeycloakUserID = rpResp.KeycloakUserID
	h.State = domain.StateIdentityCreated
	h.UpdatedAt = s.clock.Now()
	if err := s.handoffs.Update(ctx, h); err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "handoff store unavailable")
	}

	// ── 9. I-1: provision-tenant (inline with retries, same as trial path) ───
	i1Result, err := s.orchestrator.runI1(ctx, h)
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
		EmailVerificationSent: rpResp.EmailVerificationSent == nil || *rpResp.EmailVerificationSent,
	}, nil
}

// rp2Failed handles an RP-2 error. The handoff is always kept in
// payment_confirmed: the subscription is paid, so a resubmit must reuse it and
// never charge again. When RP has definitively refused this attempt (email
// already registered, realm name taken), the RP-2 freeze is lifted so the
// resubmit may change the email or slug; RP lets a failed key be reclaimed.
func (s *PaidSignupService) rp2Failed(ctx context.Context, h *domain.Handoff, slug, companyName string, err error) (*PaidSignupResult, error) {
	if isDomainCause(err, domain.ErrEmailUnavailable) || isDomainCause(err, domain.ErrSlugTaken) {
		h.RP2Attempted = false
		h.UpdatedAt = s.clock.Now()
		if uerr := s.handoffs.Update(ctx, h); uerr != nil {
			return nil, domain.NewError(domain.ErrDependencyUnavailable, "handoff store unavailable")
		}
	}
	switch {
	case isDomainCause(err, domain.ErrSlugTaken):
		// RP-2 REALM_ALREADY_DEDICATED: realm tenant-<slug> exists (PUB-1 is
		// fail-open and cannot see leftover realms). Same handling as PUB-1.
		return &PaidSignupResult{
			SignupID:        h.SignupID,
			State:           domain.StateSlugConflict,
			SlugSuggestions: domain.SuggestSlugs(slug, companyName, nil),
		}, err
	case isDomainCause(err, domain.ErrOperationInProgress):
		// RP is still running this key: the browser resubmits the same body.
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "account setup is still in progress").
			WithDetails(map[string]any{"retry_after_seconds": 2})
	}
	return nil, err
}

// charge calls B-I1 and moves the handoff on. It returns (nil, nil) when the
// payment is confirmed and provisioning should continue; otherwise the result
// or error to hand back to the browser.
func (s *PaidSignupService) charge(ctx context.Context, h *domain.Handoff, paymentToken string) (*PaidSignupResult, error) {
	resp, err := s.billing.AuthorizeAndCharge(ctx, port.ChargeRequest{
		TenantID:       h.TenantID,
		PaymentToken:   paymentToken,
		Plan:           string(h.Plan),
		Seats:          h.LicensedSeats,
		IdempotencyKey: fmt.Sprintf("charge:%s", h.TenantID),
	})
	if err != nil {
		// Delete only when Billing says definitively that nothing payable exists
		// for this tenant_id: declined, 3-D Secure expired, or price not available.
		// Every other error — timeout, 5xx, REQUEST_IN_PROGRESS, and unexpected
		// replies (ErrInternal: an empty 201, an unknown status or code) — may
		// follow a charge, so the handoff is kept and a retry reuses the key.
		if isDomainCause(err, domain.ErrPaymentDeclined) || isDomainCause(err, domain.ErrChargeExpired) ||
			isDomainCause(err, domain.ErrValidationFailed) {
			_ = s.handoffs.Delete(ctx, h.SignupID)
		}
		return nil, err
	}

	switch resp.Outcome {
	case port.ChargeActionRequired:
		h.State = domain.StatePaymentActionRequired
		h.BillingSubscriptionID = resp.BillingSubscriptionID
		// Hold the handoff past Billing's 3-D Secure window (Billing hand-off
		// SB-1): a late re-POST must find the same tenant_id and charge key. An
		// unreadable expires_at, or one beyond Billing's window (plus clock skew),
		// falls back to Billing's 30-minute window from now: the hold stays bounded.
		now := s.clock.Now()
		expiresAt, perr := time.Parse(time.RFC3339, resp.ExpiresAt)
		if perr != nil || expiresAt.After(now.Add(billingActionWindow+billingClockSkew)) {
			expiresAt = now.Add(billingActionWindow)
		}
		h.PaymentExpiresAt = expiresAt.UTC()
		h.UpdatedAt = s.clock.Now()
		if err := s.handoffs.Update(ctx, h); err != nil {
			return nil, domain.NewError(domain.ErrDependencyUnavailable, "handoff store unavailable")
		}
		return &PaidSignupResult{
			SignupID:         h.SignupID,
			State:            domain.StatePaymentActionRequired,
			PaymentAction:    resp.Action,
			PaymentExpiresAt: resp.ExpiresAt,
			PaymentHoldUntil: h.PaymentHoldUntil(),
		}, nil
	case port.ChargeAlreadyExists:
		// B-I1 is only ever called with this attempt's own key and body, so a
		// subscription under another key or body is not this attempt's record.
		// Using its id could provision against an unpaid subscription.
		return nil, domain.NewError(domain.ErrInvalidState, "a different payment is already recorded for this signup")
	case port.ChargeConfirmed:
		// Paid: record it below and continue to RP-2.
	}

	h.BillingSubscriptionID = resp.BillingSubscriptionID
	h.State = domain.StatePaymentConfirmed
	h.UpdatedAt = s.clock.Now()
	if err := s.handoffs.Update(ctx, h); err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "handoff store unavailable")
	}
	return nil, nil
}

func (s *PaidSignupService) checkRateLimits(ctx context.Context, clientIP, emailHMAC string) error {
	ipCount, ipReset, err := s.limiter.IncrIP(ctx, clientIP)
	if err != nil {
		return domain.NewError(domain.ErrDependencyUnavailable, "rate limiter unavailable")
	}
	if ipCount > int64(s.signupsPerIPPerHour) {
		metrics.IncRateLimited("ip") // LLD §11.2
		return domain.NewError(domain.ErrRateLimited, "too many signups from this IP").
			WithDetails(map[string]any{"retry_after_seconds": retryAfterSecs(ipReset), "scope": "ip"})
	}

	emailCount, emailReset, err := s.limiter.IncrEmail(ctx, emailHMAC)
	if err != nil {
		return domain.NewError(domain.ErrDependencyUnavailable, "rate limiter unavailable")
	}
	if emailCount > int64(s.attemptsPerEmailPerDay) {
		metrics.IncRateLimited("email") // LLD §11.2
		return domain.NewError(domain.ErrRateLimited, "too many attempts for this email").
			WithDetails(map[string]any{"retry_after_seconds": retryAfterSecs(emailReset), "scope": "email"})
	}
	return nil
}

// billingActionWindow is Billing's 3-D Secure window (SCA_ABANDON_AFTER), used
// when a 402 carries no readable expires_at.
const billingActionWindow = 30 * time.Minute

// billingClockSkew is the allowance for Billing's clock when bounding expires_at.
const billingClockSkew = 5 * time.Minute

// maxSeats is Billing B-I1's licensed_seats limit (1–1000).
const maxSeats = 1000

// samePaymentDetails rejects a resumed SB-5 whose details differ from the
// attempt in progress:
//   - B-I1 retries must repeat the body (email, plan, seats, payment method);
//   - RP-2 must provision the plan and seats that were paid for;
//   - once RP-2 has been called (RP2Attempted), slug, company name and email
//     are frozen too, because RP replays signup:<tenant_id>:rp2 by key.
//
// While the payment is unconfirmed the email must match, so one session cannot
// swap the person behind a charge in flight.
func samePaymentDetails(h *domain.Handoff, emailHMAC string, plan domain.Plan, seats int, paymentRefHMAC, slug, companyName string) error {
	same := h.Plan == plan && h.LicensedSeats == seats
	switch {
	case h.State != domain.StatePaymentConfirmed:
		same = same && hmacEqual(h.AdminEmailHMAC, emailHMAC) && hmacEqual(h.PaymentRefHMAC, paymentRefHMAC)
	case h.RP2Attempted:
		same = same && hmacEqual(h.AdminEmailHMAC, emailHMAC) && h.Slug == slug && h.CompanyName == companyName
	}
	if !same {
		return domain.NewError(domain.ErrInvalidState,
			"a payment is in progress for this signup; its details cannot change").
			WithDetails(map[string]any{"state": string(h.State), "reason": "details_changed"})
	}
	return nil
}

// otherSignupInProgress is the 409 for a submit of one signup type on a session
// whose handoff belongs to the other type (trial vs paid). Overwriting it could
// orphan a Keycloak user or tenant, or lose a charge key (SB-INV-3).
func otherSignupInProgress(h *domain.Handoff) error {
	return domain.NewError(domain.ErrInvalidState,
		"another signup is already in progress in this session; finish it or start a new session").
		WithDetails(map[string]any{"state": string(h.State), "reason": "other_signup_in_progress"})
}

// hmacHex is HMAC-SHA256(key, value) as hex. Used for the payment reference so
// the handoff never holds the token itself.
func hmacHex(value string, key []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
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
	if in.Seats < 1 || in.Seats > maxSeats {
		return domain.NewError(domain.ErrValidationFailed, "seats must be between 1 and 1000")
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
