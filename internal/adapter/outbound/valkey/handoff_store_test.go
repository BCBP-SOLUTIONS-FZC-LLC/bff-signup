package valkey

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
)

func newTestHandoffStore(t *testing.T) (*HandoffStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := NewClient(mr.Addr()) // production constructor — identical to iam-org-membership's New(mr.Addr())
	t.Cleanup(func() { _ = rdb.Close() })
	return NewHandoffStore(rdb), mr
}

func sampleHandoff() *domain.Handoff {
	return &domain.Handoff{
		SignupID:  "01JAB0000000000000000001",
		TenantID:  "tenant-uuid-001",
		State:     domain.StateAllocated,
		Slug:      "acme",
		Plan:      domain.PlanPro,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}

func TestHandoffStore_CreateGetUpdate(t *testing.T) {
	store, _ := newTestHandoffStore(t)
	ctx := context.Background()
	h := sampleHandoff()

	require.NoError(t, store.Create(ctx, h))

	got, err := store.Get(ctx, h.SignupID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, domain.StateAllocated, got.State)

	h.State = domain.StateIdentityCreated
	h.KeycloakUserID = "kc-user-001"
	require.NoError(t, store.Update(ctx, h))

	updated, err := store.Get(ctx, h.SignupID)
	require.NoError(t, err)
	assert.Equal(t, domain.StateIdentityCreated, updated.State)
	assert.Equal(t, "kc-user-001", updated.KeycloakUserID)
}

func TestHandoffStore_GetMiss_ReturnsNil(t *testing.T) {
	store, _ := newTestHandoffStore(t)
	got, err := store.Get(context.Background(), "no-such-id")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestHandoffStore_Delete_RemovesKey(t *testing.T) {
	store, _ := newTestHandoffStore(t)
	ctx := context.Background()
	h := sampleHandoff()

	require.NoError(t, store.Create(ctx, h))
	require.NoError(t, store.Delete(ctx, h.SignupID))

	gone, err := store.Get(ctx, h.SignupID)
	require.NoError(t, err)
	assert.Nil(t, gone)
}

func TestHandoffStore_Update_CompletedHandoff_UsesShorterTTL(t *testing.T) {
	store, mr := newTestHandoffStore(t)
	ctx := context.Background()
	h := sampleHandoff()

	require.NoError(t, store.Create(ctx, h))

	h.State = domain.StateCompleted
	require.NoError(t, store.Update(ctx, h))

	// Advance 11 minutes — completed TTL is 10 min, so the key must be gone.
	mr.FastForward(11 * time.Minute)

	got, err := store.Get(ctx, h.SignupID)
	require.NoError(t, err)
	assert.Nil(t, got, "completed handoff must expire after 10 min TTL")
}

func TestHandoffStore_Lock_AcquireAndRelease(t *testing.T) {
	store, _ := newTestHandoffStore(t)
	ctx := context.Background()
	signupID := "01JAB0000000000000000002"

	token, acquired, err := store.AcquireLock(ctx, signupID)
	require.NoError(t, err)
	assert.True(t, acquired)
	assert.NotEmpty(t, token)

	_, acquired2, err := store.AcquireLock(ctx, signupID)
	require.NoError(t, err)
	assert.False(t, acquired2, "lock must not be acquired while held")

	require.NoError(t, store.ReleaseLock(ctx, signupID, token))

	_, acquired3, err := store.AcquireLock(ctx, signupID)
	require.NoError(t, err)
	assert.True(t, acquired3, "lock must be acquirable after release")

	_ = store.ReleaseLock(ctx, signupID, token)
}

// TestHandoffStore_GetErrorWhenServerDown mirrors iam-org-membership TestVC_CACHE_012.
// LLD §14.2 fail-closed: Get() must propagate transport errors, never silently return nil
// (nil would make the service skip the handoff lookup → wrong flow / state corruption).
func TestHandoffStore_GetErrorWhenServerDown(t *testing.T) {
	store, mr := newTestHandoffStore(t)
	ctx := context.Background()
	h := sampleHandoff()

	require.NoError(t, store.Create(ctx, h))
	mr.Close() // tear down the server

	got, err := store.Get(ctx, h.SignupID)
	assert.Error(t, err, "transport error must propagate — fail-closed (LLD N-7)")
	assert.Nil(t, got, "nil must be returned alongside the error")
}

func TestHandoffStore_ReleaseLock_WrongToken_IsNoOp(t *testing.T) {
	store, _ := newTestHandoffStore(t)
	ctx := context.Background()
	signupID := "01JAB0000000000000000003"

	token, acquired, err := store.AcquireLock(ctx, signupID)
	require.NoError(t, err)
	assert.True(t, acquired)

	// Release with wrong token — lock must remain held.
	require.NoError(t, store.ReleaseLock(ctx, signupID, "wrong-token"))

	_, acquired2, err := store.AcquireLock(ctx, signupID)
	require.NoError(t, err)
	assert.False(t, acquired2, "lock must still be held after wrong-token release")

	_ = store.ReleaseLock(ctx, signupID, token)
}

// The lock must outlive the longest request holding it (config.HandoffLockTTL).
func TestHandoffStore_WithLockTTL(t *testing.T) {
	store, mr := newTestHandoffStore(t)
	ctx := context.Background()

	_, ok, err := store.WithLockTTL(35*time.Second).AcquireLock(ctx, "sid-ttl")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, 35*time.Second, mr.TTL("sbff:lock:sid-ttl"))

	mr.FastForward(20 * time.Second) // past the old 15 s
	_, ok, err = store.AcquireLock(ctx, "sid-ttl")
	require.NoError(t, err)
	assert.False(t, ok, "a paid request is still running: no second holder")

	assert.Same(t, store, store.WithLockTTL(0), "non-positive keeps the current TTL")
}

// Billing hand-off SB-1: while a 3-D Secure window is open the handoff lives
// until expires_at + 15 min, so a late re-POST finds the same tenant_id and key.
func TestHandoffStore_HoldsPastThe3DSecureWindow(t *testing.T) {
	store, mr := newTestHandoffStore(t)
	ctx := context.Background()
	h := sampleHandoff()
	h.State = domain.StatePaymentActionRequired
	h.PaymentExpiresAt = time.Now().Add(30 * time.Minute)
	require.NoError(t, store.Update(ctx, h))

	ttl := mr.TTL("sbff:handoff:" + h.SignupID)
	assert.Greater(t, ttl, 44*time.Minute, "expires_at - now + 15 min")

	mr.FastForward(31 * time.Minute) // expires_at + 1 min
	got, err := store.Get(ctx, h.SignupID)
	require.NoError(t, err)
	require.NotNil(t, got, "a re-POST after the window must still resume this handoff")
	assert.Equal(t, h.TenantID, got.TenantID)
}

func TestHandoffStore_TTLWithoutWindow(t *testing.T) {
	store, mr := newTestHandoffStore(t)
	ctx := context.Background()
	h := sampleHandoff()
	require.NoError(t, store.Update(ctx, h))
	assert.Equal(t, 30*time.Minute, mr.TTL("sbff:handoff:"+h.SignupID))

	h.State = domain.StateCompleted
	require.NoError(t, store.Update(ctx, h))
	assert.Equal(t, 10*time.Minute, mr.TTL("sbff:handoff:"+h.SignupID))
}

// A completed paid handoff stays as long as its held session, so a duplicate
// SB-5 reports the state instead of allocating a new tenant_id.
func TestHandoffStore_CompletedKeepsPaymentHold(t *testing.T) {
	store, mr := newTestHandoffStore(t)
	h := sampleHandoff()
	h.State = domain.StateCompleted
	h.PaymentExpiresAt = time.Now().Add(28 * time.Minute)
	require.NoError(t, store.Update(context.Background(), h))
	assert.Greater(t, mr.TTL("sbff:handoff:"+h.SignupID), 42*time.Minute)
}
