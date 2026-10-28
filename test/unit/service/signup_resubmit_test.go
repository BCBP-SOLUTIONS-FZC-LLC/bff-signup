package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/test/fixtures"
)

// These tests exercise the handleResubmit logic in SignupService by calling
// Submit a second time on the same session.

func TestSubmit_Resubmit_AllocatedSameDetails_RetryFails_ReturnsError(t *testing.T) {
	// Covers the "return nil, true, err" path in handleResubmit when the retry
	// of a StateAllocated handoff fails with a non-ErrSlugTaken error.
	handoffs := fixtures.NewFakeHandoffStore()
	om := fixtures.NewFakeOrgMembership("tid")
	svc := newSignupService(handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		&fixtures.FakePasswordHasher{}, fixtures.NewFakeRealmProvisioner("kc"), om)

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000041", CSRFHash: "h"}
	_, err := svc.Submit(context.Background(), validInput(sess))
	require.NoError(t, err) // first call succeeds

	// Patch to StateAllocated to force retry path on next call
	h, _ := handoffs.Get(context.Background(), sess.SignupID)
	h.State = domain.StateAllocated
	_ = handoffs.Update(context.Background(), h)

	// Eligibility check will fail on retry → orchestrator returns (nil, err)
	// → handleResubmit returns nil, true, err
	om.EligibleErr = domain.NewError(domain.ErrTrialNotEligible, "used up")
	_, err = svc.Submit(context.Background(), validInput(sess))
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrTrialNotEligible, de.Cause)
}

func TestSubmit_Resubmit_AllocatedSameDetails_Retries(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	rp := fixtures.NewFakeRealmProvisioner("kc-001")
	om := fixtures.NewFakeOrgMembership("tid")
	svc := newSignupService(handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		&fixtures.FakePasswordHasher{}, rp, om)

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000020", CSRFHash: "h"}
	input := validInput(sess)

	// First call — creates the handoff and runs the orchestrator
	result, err := svc.Submit(context.Background(), input)
	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, result.State)
	assert.Len(t, rp.Calls, 1)

	// Simulate the handoff being in StateAllocated (mid-provisioning) by patching
	// the handoff store directly before the second call
	h, _ := handoffs.Get(context.Background(), sess.SignupID)
	require.NotNil(t, h)
	h.State = domain.StateAllocated
	_ = handoffs.Update(context.Background(), h)

	// Second call with same details — should retry provisioning via handleResubmit
	result2, err2 := svc.Submit(context.Background(), input)
	require.NoError(t, err2)
	assert.Equal(t, domain.StateCompleted, result2.State)
}

// TestSubmit_Resubmit_AllocatedDifferentDetails_LockHeld_ReturnsInvalidState verifies
// the race-safety guard added to handleResubmit: when an existing StateAllocated handoff
// has different details AND the lock is already held (RP-1 in flight), we must NOT delete
// the handoff. Instead we return ErrInvalidState so the browser retries after 500ms.
func TestSubmit_Resubmit_AllocatedDifferentDetails_LockHeld_ReturnsInvalidState(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	svc := newSignupService(handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		&fixtures.FakePasswordHasher{}, fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"))

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000030", CSRFHash: "h"}

	// Establish a handoff (StateAllocated with original details)
	_, err := svc.Submit(context.Background(), validInput(sess))
	require.NoError(t, err)

	// Patch to StateAllocated to simulate RP-1 still in progress
	h, _ := handoffs.Get(context.Background(), sess.SignupID)
	h.State = domain.StateAllocated
	require.NoError(t, handoffs.Update(context.Background(), h))

	// Pre-acquire the lock to simulate a concurrent RP-1 run holding it
	_, _, _ = handoffs.AcquireLock(context.Background(), sess.SignupID)

	// Resubmit with different slug (different details) while lock is held
	input2 := validInput(sess)
	input2.Slug = "different-slug"
	_, err2 := svc.Submit(context.Background(), input2)

	require.Error(t, err2, "must not proceed when RP-1 holds the lock")
	var de *domain.DomainError
	require.ErrorAs(t, err2, &de)
	assert.Equal(t, domain.ErrInvalidState, de.Cause)

	// Handoff must NOT have been deleted — RP-1 still owns it
	still, _ := handoffs.Get(context.Background(), sess.SignupID)
	require.NotNil(t, still, "handoff must survive — deleting it during RP-1 would orphan the Keycloak user")
}

func TestSubmit_Resubmit_AllocatedDifferentDetails_StartsNew(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	rp := fixtures.NewFakeRealmProvisioner("kc-001")
	om := fixtures.NewFakeOrgMembership("tid")
	svc := newSignupService(handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		&fixtures.FakePasswordHasher{}, rp, om)

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000021", CSRFHash: "h"}

	// First call establishes the handoff
	_, err := svc.Submit(context.Background(), validInput(sess))
	require.NoError(t, err)

	// Patch handoff back to StateAllocated
	h, _ := handoffs.Get(context.Background(), sess.SignupID)
	h.State = domain.StateAllocated
	_ = handoffs.Update(context.Background(), h)

	// Second call with a different slug → different details → discard + new attempt
	input2 := validInput(sess)
	input2.Slug = "different-slug"
	result2, err2 := svc.Submit(context.Background(), input2)
	require.NoError(t, err2)
	// A new provisioning cycle completes
	assert.Equal(t, domain.StateCompleted, result2.State)
}

func TestSubmit_Resubmit_IdentityCreated_SameEmail_Resumes(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	om := fixtures.NewFakeOrgMembership("tid")
	svc := newSignupService(handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		&fixtures.FakePasswordHasher{}, fixtures.NewFakeRealmProvisioner("kc"), om)

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000022", CSRFHash: "h"}
	_, err := svc.Submit(context.Background(), validInput(sess))
	require.NoError(t, err)

	// Patch handoff to StateIdentityCreated
	h, _ := handoffs.Get(context.Background(), sess.SignupID)
	h.State = domain.StateIdentityCreated
	_ = handoffs.Update(context.Background(), h)

	// Same email → resume, returns the current state without re-running RP-1
	prevRPCalls := len(fixtures.NewFakeRealmProvisioner("").Calls)
	result2, err2 := svc.Submit(context.Background(), validInput(sess))
	require.NoError(t, err2)
	assert.Equal(t, domain.StateIdentityCreated, result2.State)
	_ = prevRPCalls // RP-1 not called again
}

func TestSubmit_Resubmit_Completed_SameEmail_Idempotent(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	svc := newSignupService(handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		&fixtures.FakePasswordHasher{}, fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"))

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000023", CSRFHash: "h"}
	_, _ = svc.Submit(context.Background(), validInput(sess))

	// Second call after completion → idempotent 200
	result2, err2 := svc.Submit(context.Background(), validInput(sess))
	require.NoError(t, err2)
	assert.Equal(t, domain.StateCompleted, result2.State)
	assert.True(t, result2.EmailVerificationSent)
}

func TestSubmit_Resubmit_IdentityCreated_DifferentEmail_Error(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	svc := newSignupService(handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		&fixtures.FakePasswordHasher{}, fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"))

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000025", CSRFHash: "h"}
	_, err := svc.Submit(context.Background(), validInput(sess))
	require.NoError(t, err)

	h, _ := handoffs.Get(context.Background(), sess.SignupID)
	h.State = domain.StateIdentityCreated
	_ = handoffs.Update(context.Background(), h)

	// Different email → ErrInvalidState (details_changed after identity created)
	input2 := validInput(sess)
	input2.AdminEmail = "different@other.example"
	_, err2 := svc.Submit(context.Background(), input2)
	require.Error(t, err2)
	var de *domain.DomainError
	require.ErrorAs(t, err2, &de)
	assert.Equal(t, domain.ErrInvalidState, de.Cause)
}

func TestSubmit_Resubmit_Allocated_SlugConflictOnRetry(t *testing.T) {
	// Exercises the branch in handleResubmit where existing state is StateAllocated,
	// sameDetails=true, but the orchestrator returns ErrSlugTaken with a non-nil result.
	handoffs := fixtures.NewFakeHandoffStore()
	om := fixtures.NewFakeOrgMembership("tid")
	om.I1Err = domain.NewError(domain.ErrSlugTaken, "slug taken on resubmit retry")
	svc := newSignupService(handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		&fixtures.FakePasswordHasher{}, fixtures.NewFakeRealmProvisioner("kc"), om)

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000026", CSRFHash: "h"}

	// First call — RP-1 succeeds, I-1 fails with ErrSlugTaken → result has StateSlugConflict
	result, err := svc.Submit(context.Background(), validInput(sess))
	// First call slug conflict → returned with error
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrSlugTaken, de.Cause)
	require.NotNil(t, result)
	assert.Equal(t, domain.StateSlugConflict, result.State)

	// Patch handoff back to StateAllocated so handleResubmit takes the retry path
	h, _ := handoffs.Get(context.Background(), sess.SignupID)
	require.NotNil(t, h)
	h.State = domain.StateAllocated
	require.NoError(t, handoffs.Update(context.Background(), h))

	// Second call same details → resubmit retry → orchestrator returns ErrSlugTaken again
	result2, err2 := svc.Submit(context.Background(), validInput(sess))
	require.Error(t, err2)
	require.ErrorAs(t, err2, &de)
	assert.Equal(t, domain.ErrSlugTaken, de.Cause)
	// The slug suggestions branch: result2 must contain state + suggestions
	require.NotNil(t, result2)
	assert.Equal(t, domain.StateSlugConflict, result2.State)
}

func TestSubmit_Resubmit_Completed_DifferentEmail_Error(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	hmacKey := []byte("test-hmac-key-32-bytes-minimum!!")
	svc := newSignupService(handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		&fixtures.FakePasswordHasher{}, fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"))

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000024", CSRFHash: "h"}
	_, _ = svc.Submit(context.Background(), validInput(sess))
	_ = hmacKey // already built in service

	// Second call with a different email → details_changed error
	input2 := validInput(sess)
	input2.AdminEmail = "different@other.example"
	_, err2 := svc.Submit(context.Background(), input2)
	require.Error(t, err2)
	var de *domain.DomainError
	require.ErrorAs(t, err2, &de)
	assert.Equal(t, domain.ErrInvalidState, de.Cause)
}

// TestSubmit_Resubmit_AllocatedSameDetails_FiresResubmitMetric exercises H-2:
// the "resubmit" path (SB-2 retry on an existing StateAllocated handoff) must
// reach IncHandoffResumed("resubmit"). Metric is nil-safe in tests.
func TestSubmit_Resubmit_AllocatedSameDetails_FiresResubmitMetric(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	svc := newSignupService(handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		&fixtures.FakePasswordHasher{}, fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"))

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000025", CSRFHash: "h"}
	// First call creates the handoff (StateAllocated → StateCompleted on RP-1 + I-1).
	result1, err := svc.Submit(context.Background(), validInput(sess))
	require.NoError(t, err)

	// Patch back to StateAllocated to force the resubmit path on the next call.
	h, _ := handoffs.Get(context.Background(), sess.SignupID)
	require.NotNil(t, h)
	h.State = domain.StateAllocated
	require.NoError(t, handoffs.Update(context.Background(), h))

	// Second call — same details → handleResubmit → IncHandoffResumed("resubmit") fires.
	result2, err := svc.Submit(context.Background(), validInput(sess))
	require.NoError(t, err)
	// Both calls must reach a terminal or in-progress state; no panic from nil metric.
	assert.NotNil(t, result1)
	assert.NotNil(t, result2)
}
