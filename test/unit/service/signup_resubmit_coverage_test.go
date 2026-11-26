package service_test

// signup_resubmit_coverage_test.go covers handleResubmit branches that require
// specialised store wrappers or unusual handoff states to reach:
//
//   - line 303: re-read after lock fails → ErrDependencyUnavailable
//   - line 306: state changed between lock acquire and re-read → ErrInvalidState
//   - line 359: StateIdentityCreated same email via full handleResubmit path
//   - line 369: fall-through for states not in switch (StateFailed)

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/test/fixtures"
)

// ── store wrappers ─────────────────────────────────────────────────────────────

// errOnNthGetStore wraps FakeHandoffStore and returns an error on the N-th Get call
// (0-indexed). All other calls delegate to the inner store.
type errOnNthGetStore struct {
	*fixtures.FakeHandoffStore
	failAt int
	mu     sync.Mutex
	n      int
}

func (s *errOnNthGetStore) Get(ctx context.Context, id string) (*domain.Handoff, error) {
	s.mu.Lock()
	cur := s.n
	s.n++
	s.mu.Unlock()
	if cur == s.failAt {
		return nil, errors.New("store temporarily unavailable")
	}
	return s.FakeHandoffStore.Get(ctx, id)
}

// lockThenChangeStateStore wraps FakeHandoffStore and advances the handoff state to
// StateIdentityCreated inside AcquireLock — simulating another goroutine completing the
// provisioning between our initial read and our re-read after the lock is taken.
type lockThenChangeStateStore struct {
	*fixtures.FakeHandoffStore
}

func (s *lockThenChangeStateStore) AcquireLock(ctx context.Context, id string) (string, bool, error) {
	token, acquired, err := s.FakeHandoffStore.AcquireLock(ctx, id)
	if acquired && err == nil {
		h, getErr := s.Get(ctx, id)
		if getErr == nil && h != nil {
			h.State = domain.StateIdentityCreated
			_ = s.Update(ctx, h)
		}
	}
	return token, acquired, err
}

// ── helper ────────────────────────────────────────────────────────────────────

func newSignupSvcWith(store port.HandoffStore) *service.SignupService {
	clk := fixtures.NewFakeClock()
	rp := fixtures.NewFakeRealmProvisioner("kc-cov")
	om := fixtures.NewFakeOrgMembership("tid-cov")
	orch := service.NewHandoffOrchestrator(store, rp, om, clk, 10*time.Millisecond)
	return service.NewSignupService(
		store, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		&fixtures.FakePasswordHasher{},
		orch, fixtures.NewFakeIDGenerator(), clk,
		[]byte("test-hmac-key-32-bytes-minimum!!"), nil,
		domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true},
		"2026-09", 3, 5,
	)
}

// ── tests ─────────────────────────────────────────────────────────────────────

// TestSubmit_Resubmit_StateFailed_TreatsAsNewAttempt covers handleResubmit line 369:
// StateFailed is not matched by any switch case → fall-through → `return nil, false, nil`.
// The caller (Submit) then treats it as a fresh attempt and completes normally.
func TestSubmit_Resubmit_StateFailed_TreatsAsNewAttempt(t *testing.T) {
	inner := fixtures.NewFakeHandoffStore()
	svc := newSignupSvcWith(inner)

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000F01", CSRFHash: "h"}

	// First Submit — creates the handoff with the correct email HMAC.
	_, err := svc.Submit(context.Background(), validInput(sess))
	require.NoError(t, err)

	// Patch to StateFailed — not in any handleResubmit switch case.
	h, _ := inner.Get(context.Background(), sess.SignupID)
	h.State = domain.StateFailed
	require.NoError(t, inner.Update(context.Background(), h))

	// Second Submit: fast path skips StateFailed; handleResubmit hits line 369
	// (fall-through), handled=false → fresh attempt completes.
	result, err2 := svc.Submit(context.Background(), validInput(sess))
	require.NoError(t, err2)
	assert.Equal(t, domain.StateCompleted, result.State)
}

// TestSubmit_Resubmit_AllocatedSameDetails_RereadsError covers handleResubmit line 303:
// Get fails after AcquireLock → `return nil, true, ErrDependencyUnavailable`.
// The errOnNthGetStore fails on call index 4 (fast-path:0, main:1 for first Submit;
// fast-path:2, main:3, re-read:4 for the second Submit).
func TestSubmit_Resubmit_AllocatedSameDetails_RereadsError(t *testing.T) {
	inner := fixtures.NewFakeHandoffStore()
	store := &errOnNthGetStore{FakeHandoffStore: inner, failAt: 4}
	svc := newSignupSvcWith(store)

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000F02", CSRFHash: "h"}

	// First Submit (counter n=0,1) → creates handoff → StateCompleted.
	_, err := svc.Submit(context.Background(), validInput(sess))
	require.NoError(t, err)

	// Patch to StateAllocated via inner store (doesn't increment the counter).
	h, _ := inner.Get(context.Background(), sess.SignupID)
	h.State = domain.StateAllocated
	require.NoError(t, inner.Update(context.Background(), h))

	// Second Submit: n=2 (fast-path, StateAllocated skips fast-path check),
	// n=3 (main Get→StateAllocated→handleResubmit→same details→lock),
	// n=4 (re-read fails) → line 303 ErrDependencyUnavailable.
	_, err2 := svc.Submit(context.Background(), validInput(sess))
	require.Error(t, err2)
	var de *domain.DomainError
	require.ErrorAs(t, err2, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

// TestSubmit_Resubmit_AllocatedSameDetails_StateChangedAfterLock covers handleResubmit
// line 306: re-read returns a state ≠ StateAllocated → `return nil, true, ErrInvalidState`.
// The lockThenChangeStateStore advances the handoff to StateIdentityCreated inside
// AcquireLock, so the subsequent re-read sees a different state.
func TestSubmit_Resubmit_AllocatedSameDetails_StateChangedAfterLock(t *testing.T) {
	inner := fixtures.NewFakeHandoffStore()
	store := &lockThenChangeStateStore{FakeHandoffStore: inner}
	svc := newSignupSvcWith(store)

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000F03", CSRFHash: "h"}

	// First Submit → creates handoff → StateCompleted.
	_, err := svc.Submit(context.Background(), validInput(sess))
	require.NoError(t, err)

	// Patch to StateAllocated via inner store.
	h, _ := inner.Get(context.Background(), sess.SignupID)
	h.State = domain.StateAllocated
	require.NoError(t, inner.Update(context.Background(), h))

	// Second Submit: handleResubmit → StateAllocated same details → AcquireLock
	// (wrapper advances state to StateIdentityCreated) → re-read → StateIdentityCreated
	// ≠ StateAllocated → line 306 ErrInvalidState.
	_, err2 := svc.Submit(context.Background(), validInput(sess))
	require.Error(t, err2)
	var de *domain.DomainError
	require.ErrorAs(t, err2, &de)
	assert.Equal(t, domain.ErrInvalidState, de.Cause)
	assert.Equal(t, 500, de.Details["retry_after_ms"])
}

// TestSubmit_Resubmit_IdentityCreated_SameEmail_ViaFullPath covers handleResubmit line 359:
// StateIdentityCreated + same email → `return &SignupResult{...State...}, true, nil`.
// The errOnNthGetStore fails on call index 2 (the fast-path Get of the second Submit),
// forcing the full handleResubmit path instead of the early return at line 122.
func TestSubmit_Resubmit_IdentityCreated_SameEmail_ViaFullPath(t *testing.T) {
	inner := fixtures.NewFakeHandoffStore()
	// First Submit reads 3 times: fast path (n=0), main (n=1), under-lock re-read (n=2).
	store := &errOnNthGetStore{FakeHandoffStore: inner, failAt: 3}
	svc := newSignupSvcWith(store)

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000F04", CSRFHash: "h"}

	// First Submit (n=0,1,2) → creates handoff → StateCompleted.
	_, err := svc.Submit(context.Background(), validInput(sess))
	require.NoError(t, err)

	// Patch to StateIdentityCreated via inner store.
	h, _ := inner.Get(context.Background(), sess.SignupID)
	h.State = domain.StateIdentityCreated
	require.NoError(t, inner.Update(context.Background(), h))

	// Second Submit: n=3 (fast-path Get fails → fast-path skipped),
	// n=4 (main Get → StateIdentityCreated → handleResubmit → same email).
	result, err2 := svc.Submit(context.Background(), validInput(sess))
	require.NoError(t, err2)
	assert.Equal(t, domain.StateIdentityCreated, result.State)
}
