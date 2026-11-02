package service_test

// More unit tests for paths that had none: the orchestrator when the context is
// cancelled or the handoff store / I-1 misbehave, and the paid service's slug
// derivation, hashing failure, weak password and handoff-lock failure.

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

func runWithContext(ctx context.Context, t *testing.T, handoffs *fixtures.FakeHandoffStore, rp *fixtures.FakeRealmProvisioner, om *fixtures.FakeOrgMembership) error {
	t.Helper()
	h := allocatedHandoff()
	require.NoError(t, handoffs.Create(context.Background(), h))
	_, err := newOrchestrator(handoffs, rp, om).Run(ctx, orchInput(h))
	return err
}

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestOrchestrator_EligibilityDown_CancelledContextStopsBeforeRetry(t *testing.T) {
	rp := fixtures.NewFakeRealmProvisioner("kc")
	om := fixtures.NewFakeOrgMembership("tid")
	om.EligibleSeq = []error{rpUnavailable()}

	err := runWithContext(cancelledContext(), t, fixtures.NewFakeHandoffStore(), rp, om)

	assert.True(t, isCause(err, domain.ErrDependencyUnavailable))
	assert.Equal(t, 1, om.EligibleCalls, "no retry once the context is cancelled")
	assert.Empty(t, rp.Calls)
}

func TestOrchestrator_RP1Down_CancelledContextStopsBeforeRetry(t *testing.T) {
	rp := fixtures.NewFakeRealmProvisioner("kc")
	rp.Err = rpUnavailable()

	err := runWithContext(cancelledContext(), t, fixtures.NewFakeHandoffStore(), rp, fixtures.NewFakeOrgMembership("tid"))

	assert.True(t, isCause(err, domain.ErrDependencyUnavailable))
	assert.Len(t, rp.Calls, 1, "no retry once the context is cancelled")
}

func TestOrchestrator_I1_CancelledContextMakesNoCallAndStaysPending(t *testing.T) {
	rp := fixtures.NewFakeRealmProvisioner("kc")
	om := fixtures.NewFakeOrgMembership("tid")
	om.I1Err = rpUnavailable()

	err := runWithContext(cancelledContext(), t, fixtures.NewFakeHandoffStore(), rp, om)

	// Not an error: the handoff is left tenant_pending and the browser polls SB-3.
	assert.NoError(t, err)
	assert.Empty(t, om.I1Calls, "no I-1 call once the context is cancelled")
}

func TestOrchestrator_I1Down_ContextExpiresDuringBackoffStaysPending(t *testing.T) {
	rp := fixtures.NewFakeRealmProvisioner("kc")
	om := fixtures.NewFakeOrgMembership("tid")
	om.I1Err = rpUnavailable()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := runWithContext(ctx, t, fixtures.NewFakeHandoffStore(), rp, om)

	assert.NoError(t, err)
	assert.Len(t, om.I1Calls, 1, "first call fails, then the backoff is cut short by the deadline")
}

func TestOrchestrator_I1ReturnsDifferentTenantID_IsInternalError(t *testing.T) {
	rp := fixtures.NewFakeRealmProvisioner("kc")
	om := fixtures.NewFakeOrgMembership("tid")
	om.I1TenantID = "some-other-tenant"

	err := runWithContext(context.Background(), t, fixtures.NewFakeHandoffStore(), rp, om)

	assert.True(t, isCause(err, domain.ErrInternal))
}

func TestOrchestrator_HandoffStoreDownAfterRP1_Is503(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	rp := fixtures.NewFakeRealmProvisioner("kc")
	h := allocatedHandoff()
	require.NoError(t, handoffs.Create(context.Background(), h))
	handoffs.UpdateErr = errors.New("valkey down")

	_, err := newOrchestrator(handoffs, rp, fixtures.NewFakeOrgMembership("tid")).Run(context.Background(), orchInput(h))

	assert.True(t, isCause(err, domain.ErrDependencyUnavailable))
}

// ── paid signup ──────────────────────────────────────────────────────────────

func newPaidWith(handoffs *fixtures.FakeHandoffStore, hasher *fixtures.FakePasswordHasher) *service.PaidSignupService {
	clk := fixtures.NewFakeClock()
	rp := fixtures.NewFakeRealmProvisioner("kc-direct-001")
	om := fixtures.NewFakeOrgMembership("tenant-paid-001")
	orch := service.NewHandoffOrchestrator(handoffs, rp, om, clk, 10*time.Millisecond)
	return service.NewPaidSignupService(
		handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{}, hasher,
		fixtures.NewFakeBillingClient("sub-001"), rp, om, orch, fixtures.NewFakeIDGenerator(), clk,
		[]byte("test-hmac-key-32-bytes-minimum!!"),
		nil,
		domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true},
		"2026-09",
	)
}

func TestPaidSubmit_SlugAbsentIsDerivedFromCompanyName(t *testing.T) {
	svc := newPaidWith(fixtures.NewFakeHandoffStore(), &fixtures.FakePasswordHasher{})
	in := validPaidInput(&port.Session{SID: "sid", SignupID: "01JAB0000000000000000PS1", CSRFHash: "h"})
	in.Slug = ""

	result, err := svc.Submit(context.Background(), in)

	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, result.State)
}

func TestPaidSubmit_PasswordHashingFailureIsInternalError(t *testing.T) {
	svc := newPaidWith(fixtures.NewFakeHandoffStore(), &fixtures.FakePasswordHasher{Err: errors.New("boom")})

	_, err := svc.Submit(context.Background(), validPaidInput(&port.Session{SID: "sid", SignupID: "01JAB0000000000000000PS2", CSRFHash: "h"}))

	assert.True(t, isCause(err, domain.ErrInternal))
}

func TestPaidSubmit_WeakPasswordIsValidationError(t *testing.T) {
	svc := newPaidWith(fixtures.NewFakeHandoffStore(), &fixtures.FakePasswordHasher{})
	in := validPaidInput(&port.Session{SID: "sid", SignupID: "01JAB0000000000000000PS3", CSRFHash: "h"})
	in.Password = "short"

	_, err := svc.Submit(context.Background(), in)

	assert.True(t, isCause(err, domain.ErrValidationFailed))
}

func TestPaidSubmit_HandoffLockStoreDownIs503(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	handoffs.AcquireLockErr = errors.New("valkey down")
	svc := newPaidWith(handoffs, &fixtures.FakePasswordHasher{})

	_, err := svc.Submit(context.Background(), validPaidInput(&port.Session{SID: "sid", SignupID: "01JAB0000000000000000PS4", CSRFHash: "h"}))

	assert.True(t, isCause(err, domain.ErrDependencyUnavailable))
}

func TestPaidSubmit_I1ContractErrorIsReturnedNotHidden(t *testing.T) {
	clk := fixtures.NewFakeClock()
	handoffs := fixtures.NewFakeHandoffStore()
	rp := fixtures.NewFakeRealmProvisioner("kc-direct-001")
	om := fixtures.NewFakeOrgMembership("tenant-paid-001")
	om.I1TenantID = "some-other-tenant"
	orch := service.NewHandoffOrchestrator(handoffs, rp, om, clk, 10*time.Millisecond)
	svc := service.NewPaidSignupService(
		handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{}, &fixtures.FakePasswordHasher{},
		fixtures.NewFakeBillingClient("sub-001"), rp, om, orch, fixtures.NewFakeIDGenerator(), clk,
		[]byte("test-hmac-key-32-bytes-minimum!!"), nil,
		domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true},
		"2026-09",
	)

	_, err := svc.Submit(context.Background(), validPaidInput(&port.Session{SID: "sid", SignupID: "01JAB0000000000000000PS5", CSRFHash: "h"}))

	assert.True(t, isCause(err, domain.ErrInternal))
}

func TestSubmit_ResumeAllocated_LockStoreDownIs503(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	rp := fixtures.NewFakeRealmProvisioner("kc-001")
	svc := newSignupService(handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{}, &fixtures.FakePasswordHasher{}, rp, fixtures.NewFakeOrgMembership("tid"))
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000HF3", CSRFHash: "h"}
	in := validInput(sess)
	_, err := svc.Submit(context.Background(), in)
	require.NoError(t, err)
	h, _ := handoffs.Get(context.Background(), sess.SignupID)
	h.State = domain.StateAllocated
	require.NoError(t, handoffs.Update(context.Background(), h))
	handoffs.AcquireLockErr = errors.New("valkey down")

	_, err = svc.Submit(context.Background(), in)

	assert.True(t, isCause(err, domain.ErrDependencyUnavailable))
	assert.Len(t, rp.Calls, 1, "RP-1 must not be called again without the lock")
}
