package valkey

// Failure paths of the three stores: the server goes away mid-operation (the
// error must reach the caller, LLD N-7 fail-closed) and a stored value is not
// valid JSON (the error must reach the caller, never a half-filled struct).

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandoffStore_Ping_UpThenDown(t *testing.T) {
	store, mr := newTestHandoffStore(t)
	ctx := context.Background()

	assert.NoError(t, store.Ping(ctx))
	mr.Close()
	assert.Error(t, store.Ping(ctx))
}

func TestHandoffStore_WritesAndLockFailWhenServerDown(t *testing.T) {
	store, mr := newTestHandoffStore(t)
	ctx := context.Background()
	h := sampleHandoff()
	mr.Close()

	assert.Error(t, store.Create(ctx, h))
	assert.Error(t, store.Update(ctx, h))
	assert.Error(t, store.Delete(ctx, h.SignupID))
	_, acquired, err := store.AcquireLock(ctx, h.SignupID)
	assert.Error(t, err)
	assert.False(t, acquired)
	assert.Error(t, store.ReleaseLock(ctx, h.SignupID, "token"))
}

func TestHandoffStore_Get_CorruptValueIsAnError(t *testing.T) {
	store, mr := newTestHandoffStore(t)
	require.NoError(t, mr.Set(handoffKey("01JAB0000000000000000001"), "{not json"))

	got, err := store.Get(context.Background(), "01JAB0000000000000000001")

	assert.Error(t, err)
	assert.Nil(t, got)
}

func TestSessionStore_WritesFailWhenServerDown(t *testing.T) {
	store, mr := newTestSessionStore(t)
	ctx := context.Background()
	mr.Close()

	assert.Error(t, store.Create(ctx, testSession()))
	assert.Error(t, store.Refresh(ctx, "sid"))
	assert.Error(t, store.Delete(ctx, "sid"))
}

func TestSessionStore_Get_CorruptValueIsAnError(t *testing.T) {
	store, mr := newTestSessionStore(t)
	require.NoError(t, mr.Set(sessKey("sid-x"), "{not json"))

	got, err := store.Get(context.Background(), "sid-x")

	assert.Error(t, err)
	assert.Nil(t, got)
}

func TestRateLimiter_FailsWhenServerDown(t *testing.T) {
	limiter, mr := newTestRateLimiter(t)
	mr.Close()

	count, _, err := limiter.IncrIP(context.Background(), "1.2.3.4")

	assert.Error(t, err, "a limiter that cannot count must not silently allow the request")
	assert.Zero(t, count)
}
