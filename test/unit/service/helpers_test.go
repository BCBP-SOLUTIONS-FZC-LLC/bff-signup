package service_test

// helpers_test.go covers internal service helper branches not hit by the main service tests:
//   - retryAfterSecs: negative duration → returns 1
//   - isDomainCause: strings.Contains fallback (non-DomainError)
//   - stateToStatus: StateAllocated → default branch (no action, no poll)
//   - handleResubmit: StateRejected → treat as new attempt (returns nil, false)
//   - PaidSignupService: TenantPending result, RP-2 ErrEmailUnavailable cleanup, I-1 slug conflict

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/test/fixtures"
)

// TestRetryAfterSecs_NegativeDuration exercises the `if d < 0 { return 1 }` branch in retryAfterSecs.
// We trigger it indirectly via SignupService.Submit: the limiter returns count>limit
// with a reset time already in the past, so time.Until(resetAt) < 0.
func TestRetryAfterSecs_NegativeDuration(t *testing.T) {
	pastLimiter := &pastResetRateLimiter{}

	svc := service.NewSignupService(
		fixtures.NewFakeHandoffStore(),
		pastLimiter,
		&fixtures.FakeCaptchaVerifier{},
		&fixtures.FakePasswordHasher{},
		service.NewHandoffOrchestrator(
			fixtures.NewFakeHandoffStore(),
			fixtures.NewFakeRealmProvisioner("kc"),
			fixtures.NewFakeOrgMembership("tid"),
			fixtures.NewFakeClock(),
		),
		fixtures.NewFakeIDGenerator(),
		fixtures.NewFakeClock(),
		[]byte("test-hmac-key-32-bytes-minimum!!"),
		nil,
		domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true},
		"2026-09",
	)

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000090", CSRFHash: "h"}
	_, err := svc.Submit(context.Background(), validInput(sess))
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrRateLimited, de.Cause)

	// Details must contain retry_after_seconds=1 (clamped from negative duration)
	require.NotNil(t, de.Details)
	assert.Equal(t, 1, de.Details["retry_after_seconds"])
}

// pastResetRateLimiter always returns count=999 (exceeds any limit) and a reset time in the past.
type pastResetRateLimiter struct{}

func (l *pastResetRateLimiter) IncrIP(_ context.Context, _ string) (int64, time.Time, error) {
	return 999, time.Now().Add(-1 * time.Hour), nil
}
func (l *pastResetRateLimiter) IncrEmail(_ context.Context, _ string) (int64, time.Time, error) {
	return 999, time.Now().Add(-1 * time.Hour), nil
}
func (l *pastResetRateLimiter) IncrSession(_ context.Context, _ string) (int64, time.Time, error) {
	return 999, time.Now().Add(-1 * time.Hour), nil
}
func (l *pastResetRateLimiter) IncrPoll(_ context.Context, _ string) (int64, time.Time, error) {
	return 999, time.Now().Add(-1 * time.Hour), nil
}

// TestIsDomainCause_StringsContainsFallback exercises the strings.Contains branch of isDomainCause.
// HandoffOrchestrator.callI1Once calls isDomainCause(err, ErrSlugTaken). When the error is a
// plain error (not *DomainError) whose text contains ErrSlugTaken.Error(), strings.Contains fires.
func TestIsDomainCause_StringsContainsFallback(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	om := &plainErrOrgMembership{plainErr: &slugTakenPlainErr{}}
	clk := fixtures.NewFakeClock()
	h := &domain.Handoff{
		SignupID:       "01JAB0000000000000000091",
		TenantID:       "tid-091",
		State:          domain.StateSlugConflict,
		KeycloakUserID: "kc-001",
		Slug:           "acme",
		CompanyName:    "Acme",
		Plan:           domain.PlanPro,
		CreatedAt:      clk.Now(),
		UpdatedAt:      clk.Now(),
	}
	require.NoError(t, handoffs.Create(context.Background(), h))

	orch := service.NewHandoffOrchestrator(handoffs, fixtures.NewFakeRealmProvisioner("kc"), om, clk)
	// ChangeSlug → callI1Once → isDomainCause(plainErr, ErrSlugTaken) → strings.Contains → true
	result, err := orch.ChangeSlug(context.Background(), h, "newslug")
	require.Error(t, err)
	require.NotNil(t, result)
	assert.Equal(t, domain.StateSlugConflict, result.State)
}

// slugTakenPlainErr is a plain error (not *DomainError) whose text contains ErrSlugTaken.Error().
type slugTakenPlainErr struct{}

func (e *slugTakenPlainErr) Error() string {
	return domain.ErrSlugTaken.Error() + " (plain wrapper)"
}

// plainErrOrgMembership returns a plain (non-DomainError) error from ProvisionTenant.
type plainErrOrgMembership struct {
	plainErr error
}

func (o *plainErrOrgMembership) CheckEligibility(_ context.Context, _ string) error { return nil }
func (o *plainErrOrgMembership) CheckSlugFree(_ context.Context, _ string) error    { return nil }
func (o *plainErrOrgMembership) ProvisionTenant(_ context.Context, _ port.ProvisionTenantRequest) (*port.ProvisionTenantResponse, error) {
	return nil, o.plainErr
}

// TestStateToStatus_AllocatedState exercises the default branch in stateToStatus (no action, no poll).
// StateAllocated is not matched by any case in the switch, so PollAfterMs and Action stay zero.
func TestStateToStatus_AllocatedState(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	clk := fixtures.NewFakeClock()
	h := &domain.Handoff{
		SignupID:       "01JAB0000000000000000092",
		TenantID:       "tid-092",
		State:          domain.StateAllocated,
		Slug:           "acme",
		CompanyName:    "Acme",
		Plan:           domain.PlanPro,
		AdminEmailHMAC: "hmac",
		KeycloakUserID: "kc-001",
		CreatedAt:      clk.Now(),
		UpdatedAt:      clk.Now(),
	}
	require.NoError(t, handoffs.Create(context.Background(), h))

	svc := service.NewStatusService(
		handoffs,
		fixtures.NewFakeRateLimiter(),
		service.NewHandoffOrchestrator(
			handoffs,
			fixtures.NewFakeRealmProvisioner("kc"),
			fixtures.NewFakeOrgMembership("tid"),
			clk,
		),
		clk,
		nil,
	)

	result, err := svc.GetStatus(context.Background(),
		"01JAB0000000000000000092", "01JAB0000000000000000092", "sid")
	require.NoError(t, err)
	assert.Equal(t, domain.StateAllocated, result.State)
	assert.Empty(t, result.Action)
	assert.Zero(t, result.PollAfterMs)
}

// TestHandleResubmit_StateRejected exercises the StateRejected branch in handleResubmit
// which returns (nil, false, nil) so the caller falls through to a fresh attempt.
func TestHandleResubmit_StateRejected_TreatsAsNewAttempt(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	clk := fixtures.NewFakeClock()

	existing := &domain.Handoff{
		SignupID:       "01JAB0000000000000000093",
		TenantID:       "tid-093",
		State:          domain.StateRejected,
		Slug:           "acme",
		CompanyName:    "Acme",
		Plan:           domain.PlanPro,
		AdminEmailHMAC: "old-hmac",
		CreatedAt:      clk.Now(),
		UpdatedAt:      clk.Now(),
	}
	require.NoError(t, handoffs.Create(context.Background(), existing))

	svc := newSignupService(
		handoffs, fixtures.NewFakeRateLimiter(),
		&fixtures.FakeCaptchaVerifier{}, &fixtures.FakePasswordHasher{},
		fixtures.NewFakeRealmProvisioner("kc-new"), fixtures.NewFakeOrgMembership("tid-new"),
	)

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000093", CSRFHash: "h"}
	// With StateRejected handoff in store → handleResubmit returns false → new attempt completes
	result, err := svc.Submit(context.Background(), validInput(sess))
	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, result.State)
}

// TestHandleResubmit_TenantPending_DifferentEmail exercises the branch where
// existing.State is StateTenantPending and email HMAC doesn't match → ErrInvalidState.
func TestHandleResubmit_TenantPending_DifferentEmail_ReturnsError(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	svc := newSignupService(
		handoffs, fixtures.NewFakeRateLimiter(),
		&fixtures.FakeCaptchaVerifier{}, &fixtures.FakePasswordHasher{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000094", CSRFHash: "h"}
	_, err := svc.Submit(context.Background(), validInput(sess))
	require.NoError(t, err)

	h, _ := handoffs.Get(context.Background(), sess.SignupID)
	require.NotNil(t, h)
	h.State = domain.StateTenantPending
	require.NoError(t, handoffs.Update(context.Background(), h))

	input2 := validInput(sess)
	input2.AdminEmail = "different@other.example"
	_, err2 := svc.Submit(context.Background(), input2)
	require.Error(t, err2)
	var de *domain.DomainError
	require.ErrorAs(t, err2, &de)
	assert.Equal(t, domain.ErrInvalidState, de.Cause)
}

// TestSubmitPaid_TenantPending_Result exercises the paid flow ending in StateTenantPending
// (I-1 fails all retries).
func TestSubmitPaid_TenantPending_Result(t *testing.T) {
	om := fixtures.NewFakeOrgMembership("tid")
	om.I1Err = domain.NewError(domain.ErrDependencyUnavailable, "i1 down")

	svc := newPaidSignupSvc(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeBillingClient("sub-001"), fixtures.NewFakeRealmProvisioner("kc-direct"), om,
	)

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000095", CSRFHash: "h"}
	result, err := svc.Submit(context.Background(), validPaidInput(sess))
	require.NoError(t, err)
	assert.Equal(t, domain.StateTenantPending, result.State)
}

// TestSubmitPaid_RP2EmailUnavailable_DeletesHandoff tests the RP-2 email-unavailable cleanup.
func TestSubmitPaid_RP2EmailUnavailable_DeletesHandoff(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	rp := fixtures.NewFakeRealmProvisioner("kc")
	rp.DirectErr = domain.NewError(domain.ErrEmailUnavailable, "email in use")

	svc := newPaidSignupSvc(
		handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeBillingClient("sub-001"), rp, fixtures.NewFakeOrgMembership("tid"),
	)

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000096", CSRFHash: "h"}
	_, err := svc.Submit(context.Background(), validPaidInput(sess))
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrEmailUnavailable, de.Cause)

	got, _ := handoffs.Get(context.Background(), sess.SignupID)
	assert.Nil(t, got, "handoff must be deleted after RP-2 email unavailable")
}

// TestSubmitPaid_HandoffCreateFails exercises the handoff store Create error path.
func TestSubmitPaid_HandoffCreateFails(t *testing.T) {
	handoffs := &errOnCreateHandoffStore{FakeHandoffStore: fixtures.NewFakeHandoffStore()}
	clk := fixtures.NewFakeClock()
	pol := domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true}
	svc := service.NewPaidSignupService(
		handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{}, &fixtures.FakePasswordHasher{},
		fixtures.NewFakeBillingClient("sub-001"), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeIDGenerator(), clk,
		[]byte("test-hmac-key-32-bytes-minimum!!"), nil, pol, "2026-09",
	)

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000098", CSRFHash: "h"}
	_, err := svc.Submit(context.Background(), validPaidInput(sess))
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

// TestSubmitPaid_HandoffUpdateAfterBillingFails exercises the Update error path after billing.
func TestSubmitPaid_HandoffUpdateAfterBillingFails(t *testing.T) {
	handoffs := &errOnUpdateHandoffStore{FakeHandoffStore: fixtures.NewFakeHandoffStore(), failAfter: 0}
	clk := fixtures.NewFakeClock()
	pol := domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true}
	svc := service.NewPaidSignupService(
		handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{}, &fixtures.FakePasswordHasher{},
		fixtures.NewFakeBillingClient("sub-001"), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeIDGenerator(), clk,
		[]byte("test-hmac-key-32-bytes-minimum!!"), nil, pol, "2026-09",
	)

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000099", CSRFHash: "h"}
	_, err := svc.Submit(context.Background(), validPaidInput(sess))
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

// errOnCreateHandoffStore wraps FakeHandoffStore and fails on Create.
type errOnCreateHandoffStore struct {
	*fixtures.FakeHandoffStore
}

func (s *errOnCreateHandoffStore) Create(_ context.Context, _ *domain.Handoff) error {
	return errors.New("store error")
}

// errOnUpdateHandoffStore wraps FakeHandoffStore and fails on Update after failAfter calls.
type errOnUpdateHandoffStore struct {
	*fixtures.FakeHandoffStore
	failAfter int
	calls     int
}

func (s *errOnUpdateHandoffStore) Update(ctx context.Context, h *domain.Handoff) error {
	if s.calls >= s.failAfter {
		s.calls++
		return errors.New("store update error")
	}
	s.calls++
	return s.FakeHandoffStore.Update(ctx, h)
}

// TestSubmitPaid_HandoffUpdateAfterRP2Fails exercises the Update error after RP-2 completes.
func TestSubmitPaid_HandoffUpdateAfterRP2Fails(t *testing.T) {
	// failAfter=1: first Update (after billing) succeeds; second Update (after RP-2) fails
	handoffs := &errOnUpdateHandoffStore{FakeHandoffStore: fixtures.NewFakeHandoffStore(), failAfter: 1}
	clk := fixtures.NewFakeClock()
	pol := domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true}
	svc := service.NewPaidSignupService(
		handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{}, &fixtures.FakePasswordHasher{},
		fixtures.NewFakeBillingClient("sub-001"), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeIDGenerator(), clk,
		[]byte("test-hmac-key-32-bytes-minimum!!"), nil, pol, "2026-09",
	)

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000100", CSRFHash: "h"}
	_, err := svc.Submit(context.Background(), validPaidInput(sess))
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

// TestSubmitPaid_LockNotAcquired exercises the lock contention path in PaidSignupService.Submit.
func TestSubmitPaid_LockNotAcquired(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	// Pre-acquire the lock to simulate contention
	_, _, _ = handoffs.AcquireLock(context.Background(), "01JAB0000000000000000101")

	clk := fixtures.NewFakeClock()
	pol := domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true}
	svc := service.NewPaidSignupService(
		handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{}, &fixtures.FakePasswordHasher{},
		fixtures.NewFakeBillingClient("sub-001"), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeIDGenerator(), clk,
		[]byte("test-hmac-key-32-bytes-minimum!!"), nil, pol, "2026-09",
	)

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000101", CSRFHash: "h"}
	_, err := svc.Submit(context.Background(), validPaidInput(sess))
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInvalidState, de.Cause)
}

// TestSubmitPaid_I1SlugConflict exercises the I-1 slug-conflict path in PaidSignupService.Submit.
func TestSubmitPaid_I1SlugConflict(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	om := fixtures.NewFakeOrgMembership("tid")
	om.I1Err = domain.NewError(domain.ErrSlugTaken, "slug taken by another tenant")

	svc := newPaidSignupSvc(
		handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeBillingClient("sub-001"), fixtures.NewFakeRealmProvisioner("kc-direct"), om,
	)

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000097", CSRFHash: "h"}
	result, err := svc.Submit(context.Background(), validPaidInput(sess))
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrSlugTaken, de.Cause)
	require.NotNil(t, result)
	assert.Equal(t, domain.StateSlugConflict, result.State)
	assert.NotEmpty(t, result.SlugSuggestions)
}
