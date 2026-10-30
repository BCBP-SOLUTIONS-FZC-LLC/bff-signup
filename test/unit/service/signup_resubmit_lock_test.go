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

// SB2-56 / I-03 / OBS-37: a second SB-2 on an allocated handoff must not re-run
// RP-1 while another request holds the handoff lock. It gets 409 invalid_state
// with details.retry_after_ms instead (LLD §9.2).
func TestSubmit_Resubmit_AllocatedSameDetails_LockHeld_ReturnsInvalidStateWithoutRP1(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	rp := fixtures.NewFakeRealmProvisioner("kc-001")
	svc := newSignupService(handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		&fixtures.FakePasswordHasher{}, rp, fixtures.NewFakeOrgMembership("tid"))

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000056", CSRFHash: "h"}
	input := validInput(sess)

	_, err := svc.Submit(context.Background(), input)
	require.NoError(t, err)
	require.Len(t, rp.Calls, 1)

	// Put the handoff back to allocated (RP-1 still in flight) and hold the lock
	// as the first request would.
	h, _ := handoffs.Get(context.Background(), sess.SignupID)
	h.State = domain.StateAllocated
	require.NoError(t, handoffs.Update(context.Background(), h))
	_, acquired, _ := handoffs.AcquireLock(context.Background(), sess.SignupID)
	require.True(t, acquired)

	_, err2 := svc.Submit(context.Background(), input)

	require.Error(t, err2)
	var de *domain.DomainError
	require.ErrorAs(t, err2, &de)
	assert.Equal(t, domain.ErrInvalidState, de.Cause)
	assert.Equal(t, 500, de.Details["retry_after_ms"])
	assert.Len(t, rp.Calls, 1, "RP-1 must not be called again while the lock is held")
}

// Control: with the lock free, the same resubmit still resumes provisioning.
func TestSubmit_Resubmit_AllocatedSameDetails_LockFree_StillResumes(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	rp := fixtures.NewFakeRealmProvisioner("kc-001")
	svc := newSignupService(handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		&fixtures.FakePasswordHasher{}, rp, fixtures.NewFakeOrgMembership("tid"))

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000057", CSRFHash: "h"}
	input := validInput(sess)

	_, err := svc.Submit(context.Background(), input)
	require.NoError(t, err)

	h, _ := handoffs.Get(context.Background(), sess.SignupID)
	h.State = domain.StateAllocated
	require.NoError(t, handoffs.Update(context.Background(), h))

	result, err2 := svc.Submit(context.Background(), input)
	require.NoError(t, err2)
	assert.Equal(t, domain.StateCompleted, result.State)

	// The lock is released afterwards, so a third request can take it.
	_, acquired, _ := handoffs.AcquireLock(context.Background(), sess.SignupID)
	assert.True(t, acquired, "lock must be released after the resubmit finishes")
}
