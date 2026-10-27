package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/test/fixtures"
)

func newPaidSignupSvc(
	handoffs *fixtures.FakeHandoffStore,
	limiter *fixtures.FakeRateLimiter,
	captcha *fixtures.FakeCaptchaVerifier,
	billing *fixtures.FakeBillingClient,
	rp *fixtures.FakeRealmProvisioner,
	om *fixtures.FakeOrgMembership,
) *service.PaidSignupService {
	clk := fixtures.NewFakeClock()
	idgen := fixtures.NewFakeIDGenerator()
	return service.NewPaidSignupService(
		handoffs, limiter, captcha, &fixtures.FakePasswordHasher{},
		billing, rp, om, idgen, clk,
		[]byte("test-hmac-key-32-bytes-minimum!!"),
		nil,
		domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true},
		"2026-09",
	)
}

func validPaidInput(sess *port.Session) service.PaidSignupInput {
	return service.PaidSignupInput{
		CompanyName:   "Acme Bidding Co",
		Slug:          "acme",
		AdminName:     "Dana Owner",
		AdminEmail:    "owner@acme.example",
		Password:      "SecurePass999@",
		Plan:          domain.PlanPro,
		Seats:         5,
		PaymentToken:  "tok_stripe_test_001",
		AcceptTerms:   true,
		TermsVersion:  "2026-09",
		CaptchaToken:  "valid-token",
		DefaultLocale: "en-US",
		ClientIP:      "1.2.3.4",
		Session:       sess,
	}
}

func TestPaidSubmit_HappyPath(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	billing := fixtures.NewFakeBillingClient("sub-001")
	rp := fixtures.NewFakeRealmProvisioner("kc-direct-001")
	om := fixtures.NewFakeOrgMembership("tenant-paid-001")

	svc := newPaidSignupSvc(handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{}, billing, rp, om)
	sess := &port.Session{SID: "sid-paid", SignupID: "01JAB0000000000000000010", CSRFHash: "h"}

	result, err := svc.Submit(context.Background(), validPaidInput(sess))
	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, result.State)
	assert.True(t, result.EmailVerificationSent)

	// Billing must have been called
	require.Len(t, billing.Calls, 1)
	assert.Equal(t, "pro", billing.Calls[0].Plan)
	assert.Equal(t, 5, billing.Calls[0].Seats)

	// RP-2 must have been called (SB-INV-4: RP before I-1)
	require.Len(t, rp.DirectCalls, 1)
	assert.Equal(t, "sub-001", rp.DirectCalls[0].BillingSubscriptionID)

	// I-1 must have been called
	require.Len(t, om.I1Calls, 1)
	assert.Equal(t, rp.DirectCalls[0].Plan, om.I1Calls[0].Plan)
}

func TestPaidSubmit_CaptchaFails(t *testing.T) {
	svc := newPaidSignupSvc(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(),
		&fixtures.FakeCaptchaVerifier{Err: domain.NewError(domain.ErrCaptchaFailed, "rejected")},
		fixtures.NewFakeBillingClient("sub"), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000011", CSRFHash: "h"}

	_, err := svc.Submit(context.Background(), validPaidInput(sess))
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrCaptchaFailed, de.Cause)
}

func TestPaidSubmit_BillingFails_DeletesHandoff(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	billing := fixtures.NewFakeBillingClient("")
	billing.Err = domain.NewError(domain.ErrDependencyUnavailable, "payment service down")

	svc := newPaidSignupSvc(handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		billing, fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"))
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000012", CSRFHash: "h"}

	_, err := svc.Submit(context.Background(), validPaidInput(sess))
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)

	// Handoff must have been deleted (billing failure cleanup, LLD §19)
	got, _ := handoffs.Get(context.Background(), sess.SignupID)
	assert.Nil(t, got, "handoff must be deleted after billing failure")
}

func TestPaidSubmit_ValidationFails_MissingPaymentToken(t *testing.T) {
	svc := newPaidSignupSvc(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeBillingClient("sub"), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000013", CSRFHash: "h"}
	input := validPaidInput(sess)
	input.PaymentToken = ""

	_, err := svc.Submit(context.Background(), input)
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrValidationFailed, de.Cause)
}

func TestPaidSubmit_IPRateLimitExceeded(t *testing.T) {
	limiter := fixtures.NewFakeRateLimiter()
	svc := newPaidSignupSvc(
		fixtures.NewFakeHandoffStore(), limiter, &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeBillingClient("sub"), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000030p", CSRFHash: "h"}

	// Exhaust the IP rate limit
	for range 3 {
		_, _ = svc.Submit(context.Background(), validPaidInput(sess))
	}
	_, err := svc.Submit(context.Background(), validPaidInput(sess))
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrRateLimited, de.Cause)
}

func TestPaidSubmit_EmailRateLimitExceeded(t *testing.T) {
	// maxAttemptsPerEmailPerDay = 5; IP limit = 3 per hour.
	// Use different IPs per call so IP limit is never hit while email count builds.
	handoffs := fixtures.NewFakeHandoffStore()
	limiter := fixtures.NewFakeRateLimiter()
	billing := fixtures.NewFakeBillingClient("sub")
	rp := fixtures.NewFakeRealmProvisioner("kc")
	om := fixtures.NewFakeOrgMembership("tid")
	svc := newPaidSignupSvc(handoffs, limiter, &fixtures.FakeCaptchaVerifier{}, billing, rp, om)

	// 5 calls with unique IPs + unique SignupIDs build email count to 5 (within limit).
	for i := range 5 {
		sess := &port.Session{SID: "sid", SignupID: fmt.Sprintf("01JAB00000000000000010%dp", i), CSRFHash: "h"}
		input := validPaidInput(sess)
		input.ClientIP = fmt.Sprintf("10.0.0.%d", i+1)
		_, _ = svc.Submit(context.Background(), input)
	}
	// 6th call: email count (6) > limit (5) → ErrRateLimited
	sess := &port.Session{SID: "sid6", SignupID: "01JAB000000000000000106p", CSRFHash: "h"}
	input := validPaidInput(sess)
	input.ClientIP = "10.0.0.6"
	_, err := svc.Submit(context.Background(), input)
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrRateLimited, de.Cause)
}

func TestPaidSubmit_LockNotAcquired_ReturnsInvalidState(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	svc := newPaidSignupSvc(handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeBillingClient("sub"), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"))

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000050p", CSRFHash: "h"}
	_, _, _ = handoffs.AcquireLock(context.Background(), sess.SignupID)

	_, err := svc.Submit(context.Background(), validPaidInput(sess))
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInvalidState, de.Cause)
}

func TestPaidSubmit_RateLimiterIPError(t *testing.T) {
	svc := newPaidSignupSvcWithLimiter(fixtures.NewFakeHandoffStore(), &paidIPErrLimiter{})
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000032p", CSRFHash: "h"}
	_, err := svc.Submit(context.Background(), validPaidInput(sess))
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

func newPaidSignupSvcWithLimiter(handoffs *fixtures.FakeHandoffStore, limiter port.RateLimiter) *service.PaidSignupService {
	clk := fixtures.NewFakeClock()
	return service.NewPaidSignupService(
		handoffs, limiter, &fixtures.FakeCaptchaVerifier{}, &fixtures.FakePasswordHasher{},
		fixtures.NewFakeBillingClient("sub"), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeIDGenerator(), clk,
		[]byte("test-hmac-key-32-bytes-minimum!!"), nil,
		domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true},
		"2026-09",
	)
}

// paidEmailErrLimiter returns an error from IncrEmail to test the
// ErrDependencyUnavailable path in PaidSignupService.Submit.
type paidEmailErrLimiter struct{}

func (l *paidEmailErrLimiter) IncrIP(_ context.Context, _ string) (int64, time.Time, error) {
	return 1, time.Now().Add(time.Hour), nil // IP passes
}
func (l *paidEmailErrLimiter) IncrEmail(_ context.Context, _ string) (int64, time.Time, error) {
	return 0, time.Time{}, errors.New("redis down")
}
func (l *paidEmailErrLimiter) IncrSession(_ context.Context, _ string) (int64, time.Time, error) {
	return 1, time.Now().Add(time.Hour), nil
}
func (l *paidEmailErrLimiter) IncrPoll(_ context.Context, _ string) (int64, time.Time, error) {
	return 1, time.Now().Add(time.Hour), nil
}

func TestPaidSubmit_IncrEmailError_ReturnsDependencyUnavailable(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	svc := newPaidSignupSvcWithLimiter(handoffs, &paidEmailErrLimiter{})
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000060p", CSRFHash: "h"}

	_, err := svc.Submit(context.Background(), validPaidInput(sess))
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

// paidIPErrLimiter returns an error from IncrIP.
type paidIPErrLimiter struct{}

func (l *paidIPErrLimiter) IncrIP(_ context.Context, _ string) (int64, time.Time, error) {
	return 0, time.Time{}, errors.New("rate limiter error")
}
func (l *paidIPErrLimiter) IncrEmail(_ context.Context, _ string) (int64, time.Time, error) {
	return 0, time.Time{}, nil
}
func (l *paidIPErrLimiter) IncrSession(_ context.Context, _ string) (int64, time.Time, error) {
	return 0, time.Time{}, nil
}
func (l *paidIPErrLimiter) IncrPoll(_ context.Context, _ string) (int64, time.Time, error) {
	return 0, time.Time{}, nil
}

func TestPaidSubmit_ValidationFails_BadCompanyName(t *testing.T) {
	svc := newPaidSignupSvc(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeBillingClient("sub"), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000020p", CSRFHash: "h"}
	input := validPaidInput(sess)
	input.CompanyName = "A" // too short

	_, err := svc.Submit(context.Background(), input)
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrValidationFailed, de.Cause)
}

func TestPaidSubmit_ValidationFails_BadSlug(t *testing.T) {
	svc := newPaidSignupSvc(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeBillingClient("sub"), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000021p", CSRFHash: "h"}
	input := validPaidInput(sess)
	input.Slug = "UPPER_INVALID"

	_, err := svc.Submit(context.Background(), input)
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrValidationFailed, de.Cause)
}

func TestPaidSubmit_ValidationFails_BadAdminName(t *testing.T) {
	svc := newPaidSignupSvc(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeBillingClient("sub"), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000022p", CSRFHash: "h"}
	input := validPaidInput(sess)
	input.AdminName = "" // empty = fails ValidateAdminName

	_, err := svc.Submit(context.Background(), input)
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrValidationFailed, de.Cause)
}

func TestPaidSubmit_ValidationFails_BadAdminEmail(t *testing.T) {
	svc := newPaidSignupSvc(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeBillingClient("sub"), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000023p", CSRFHash: "h"}
	input := validPaidInput(sess)
	input.AdminEmail = "not-an-email"

	_, err := svc.Submit(context.Background(), input)
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrValidationFailed, de.Cause)
}

func TestPaidSubmit_ValidationFails_InvalidPlan(t *testing.T) {
	svc := newPaidSignupSvc(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeBillingClient("sub"), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000024p", CSRFHash: "h"}
	input := validPaidInput(sess)
	input.Plan = "trial" // not a valid plan (SB-INV-8)

	_, err := svc.Submit(context.Background(), input)
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrValidationFailed, de.Cause)
}

func TestPaidSubmit_ValidationFails_TermsNotAccepted(t *testing.T) {
	svc := newPaidSignupSvc(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeBillingClient("sub"), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000025p", CSRFHash: "h"}
	input := validPaidInput(sess)
	input.AcceptTerms = false

	_, err := svc.Submit(context.Background(), input)
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrValidationFailed, de.Cause)
}

func TestPaidSubmit_ValidationFails_EmptyTermsVersion(t *testing.T) {
	svc := newPaidSignupSvc(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeBillingClient("sub"), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000026p", CSRFHash: "h"}
	input := validPaidInput(sess)
	input.TermsVersion = ""

	_, err := svc.Submit(context.Background(), input)
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrValidationFailed, de.Cause)
}

func TestPaidSubmit_ValidationFails_WrongTermsVersion(t *testing.T) {
	svc := newPaidSignupSvc(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeBillingClient("sub"), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000027p", CSRFHash: "h"}
	input := validPaidInput(sess)
	input.TermsVersion = "2020-01" // old version

	_, err := svc.Submit(context.Background(), input)
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrValidationFailed, de.Cause)
}

func TestPaidSubmit_ValidationFails_SeatsZero(t *testing.T) {
	svc := newPaidSignupSvc(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeBillingClient("sub"), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000015", CSRFHash: "h"}
	input := validPaidInput(sess)
	input.Seats = 0

	_, err := svc.Submit(context.Background(), input)
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrValidationFailed, de.Cause)
	assert.Contains(t, de.Message, "seats")
}

func TestPaidSubmit_ValidationFails_SeatsNegative(t *testing.T) {
	svc := newPaidSignupSvc(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeBillingClient("sub"), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000016", CSRFHash: "h"}
	input := validPaidInput(sess)
	input.Seats = -1

	_, err := svc.Submit(context.Background(), input)
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrValidationFailed, de.Cause)
}

func TestPaidSubmit_SlugConflictAfterBilling(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	billing := fixtures.NewFakeBillingClient("sub-002")
	om := fixtures.NewFakeOrgMembership("tid")
	om.SlugFreeErr = domain.NewError(domain.ErrSlugTaken, "slug taken")

	svc := newPaidSignupSvc(handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		billing, fixtures.NewFakeRealmProvisioner("kc-direct"), om)
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000014", CSRFHash: "h"}

	result, err := svc.Submit(context.Background(), validPaidInput(sess))
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrSlugTaken, de.Cause)
	require.NotNil(t, result)
	assert.Equal(t, domain.StateSlugConflict, result.State)
	assert.NotEmpty(t, result.SlugSuggestions)
}
