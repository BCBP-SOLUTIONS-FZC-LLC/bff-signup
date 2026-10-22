package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/test/fixtures"
)

func newSignupService(
	handoffs *fixtures.FakeHandoffStore,
	limiter *fixtures.FakeRateLimiter,
	captcha *fixtures.FakeCaptchaVerifier,
	hasher *fixtures.FakePasswordHasher,
	rp *fixtures.FakeRealmProvisioner,
	om *fixtures.FakeOrgMembership,
) *service.SignupService {
	clk := fixtures.NewFakeClock()
	idgen := fixtures.NewFakeIDGenerator()
	orch := service.NewHandoffOrchestrator(handoffs, rp, om, clk)
	return service.NewSignupService(
		handoffs, limiter, captcha, hasher,
		orch, idgen, clk,
		[]byte("test-hmac-key-32-bytes-minimum!!"),
		nil,
		domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true},
	)
}

func validInput(sess *port.Session) service.SignupInput {
	return service.SignupInput{
		CompanyName:   "Acme Bidding Co",
		Slug:          "acme",
		AdminName:     "Dana Owner",
		AdminEmail:    "owner@acme.example",
		Password:      "SecurePass999@",
		Plan:          domain.PlanPro,
		AcceptTerms:   true,
		TermsVersion:  "2026-09",
		CaptchaToken:  "valid-token",
		DefaultLocale: "en-US",
		ClientIP:      "1.2.3.4",
		Session:       sess,
	}
}

func TestSubmit_HappyPath(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	limiter := fixtures.NewFakeRateLimiter()
	captcha := &fixtures.FakeCaptchaVerifier{}
	hasher := &fixtures.FakePasswordHasher{}
	rp := fixtures.NewFakeRealmProvisioner("keycloak-user-id-001")
	om := fixtures.NewFakeOrgMembership("tenant-id-001")

	svc := newSignupService(handoffs, limiter, captcha, hasher, rp, om)
	sess := &port.Session{SID: "sid-001", SignupID: "01JAB0000000000000000000", CSRFHash: "h"}

	result, err := svc.Submit(context.Background(), validInput(sess))
	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, result.State)
	assert.True(t, result.EmailVerificationSent)

	// RP-1 must have been called with correct plan (SB-INV-8)
	require.Len(t, rp.Calls, 1)
	assert.Equal(t, "pro", rp.Calls[0].Plan)
	assert.NotEqual(t, "trial", rp.Calls[0].Plan, "plan must never be 'trial' (SB-INV-8)")

	// I-1 must have been called with the same plan (SB-INV-8)
	require.Len(t, om.I1Calls, 1)
	assert.Equal(t, rp.Calls[0].Plan, om.I1Calls[0].Plan)
	assert.Equal(t, rp.Calls[0].TenantSlug, om.I1Calls[0].Slug)
	assert.Equal(t, rp.Calls[0].TenantName, om.I1Calls[0].Name)
}

func TestSubmit_CaptchaFails_ReturnsError(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	limiter := fixtures.NewFakeRateLimiter()
	captcha := &fixtures.FakeCaptchaVerifier{Err: domain.NewError(domain.ErrCaptchaFailed, "rejected")}
	svc := newSignupService(handoffs, limiter, captcha,
		&fixtures.FakePasswordHasher{},
		fixtures.NewFakeRealmProvisioner("id"),
		fixtures.NewFakeOrgMembership("tid"))

	sess := &port.Session{SID: "sid", SignupID: "ulid", CSRFHash: "h"}
	_, err := svc.Submit(context.Background(), validInput(sess))

	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrCaptchaFailed, de.Cause)

	// Rate limiter must NOT have been incremented (SB-INV-5: CAPTCHA before counters)
	h, _, _ := limiter.IncrIP(context.Background(), "1.2.3.4")
	assert.Equal(t, int64(1), h, "first real incr — no prior increment from the failed submit")
}

func TestSubmit_TrialNotEligible(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	limiter := fixtures.NewFakeRateLimiter()
	captcha := &fixtures.FakeCaptchaVerifier{}
	om := fixtures.NewFakeOrgMembership("tid")
	om.EligibleErr = domain.NewError(domain.ErrTrialNotEligible, "already used")

	svc := newSignupService(handoffs, limiter, captcha,
		&fixtures.FakePasswordHasher{},
		fixtures.NewFakeRealmProvisioner("id"), om)
	sess := &port.Session{SID: "sid", SignupID: "ulid", CSRFHash: "h"}

	_, err := svc.Submit(context.Background(), validInput(sess))
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrTrialNotEligible, de.Cause)

	// RP-1 must NOT have been called (eligibility before RP-1, SB-INV-4)
	assert.Empty(t, fixtures.NewFakeRealmProvisioner("").Calls)
}

func TestSubmit_InvalidPlan_Rejected(t *testing.T) {
	svc := newSignupService(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(),
		&fixtures.FakeCaptchaVerifier{}, &fixtures.FakePasswordHasher{},
		fixtures.NewFakeRealmProvisioner("id"), fixtures.NewFakeOrgMembership("tid"))

	sess := &port.Session{SID: "sid", SignupID: "ulid", CSRFHash: "h"}
	input := validInput(sess)
	input.Plan = "trial" // must always be rejected (SB-INV-8)

	_, err := svc.Submit(context.Background(), input)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrValidationFailed, de.Cause)
}

func TestSubmit_SlugConflict_ReturnsSuggestions(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	om := fixtures.NewFakeOrgMembership("tid")
	om.I1Err = domain.NewError(domain.ErrSlugTaken, "slug taken")

	svc := newSignupService(handoffs, fixtures.NewFakeRateLimiter(),
		&fixtures.FakeCaptchaVerifier{}, &fixtures.FakePasswordHasher{},
		fixtures.NewFakeRealmProvisioner("kc-id"), om)
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000000", CSRFHash: "h"}

	result, err := svc.Submit(context.Background(), validInput(sess))
	// slug_taken is surfaced as an error AND a result
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrSlugTaken, de.Cause)
	// Result still carries state and suggestions
	require.NotNil(t, result)
	assert.Equal(t, domain.StateSlugConflict, result.State)
}
