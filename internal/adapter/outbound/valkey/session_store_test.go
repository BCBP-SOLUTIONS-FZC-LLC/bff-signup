package valkey

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

func newTestSessionStore(t *testing.T) (*SessionStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := NewClient(mr.Addr()) // production constructor — identical to iam-org-membership's New(mr.Addr())
	t.Cleanup(func() { _ = rdb.Close() })
	return NewSessionStore(rdb), mr
}

func testSession() *port.Session {
	return &port.Session{
		SID:      "test-sid-001",
		CSRFHash: "sha256hash",
		SignupID: "01JAB0000000000000000000",
	}
}

func TestSessionStore_CreateGetDelete(t *testing.T) {
	store, _ := newTestSessionStore(t)
	ctx := context.Background()
	sess := testSession()

	require.NoError(t, store.Create(ctx, sess))

	got, err := store.Get(ctx, sess.SID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, sess.SID, got.SID)
	assert.Equal(t, sess.CSRFHash, got.CSRFHash)

	require.NoError(t, store.Delete(ctx, sess.SID))
	gone, err := store.Get(ctx, sess.SID)
	require.NoError(t, err)
	assert.Nil(t, gone, "session must be gone after Delete")
}

func TestSessionStore_GetMiss_ReturnsNil(t *testing.T) {
	store, _ := newTestSessionStore(t)
	got, err := store.Get(context.Background(), "no-such-sid")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestSessionStore_Refresh_ExtendsExpiry(t *testing.T) {
	store, mr := newTestSessionStore(t)
	ctx := context.Background()
	sess := testSession()

	require.NoError(t, store.Create(ctx, sess))

	// Fast-forward past half the TTL and refresh.
	mr.FastForward(10 * time.Minute)
	require.NoError(t, store.Refresh(ctx, sess.SID))

	// Fast-forward another 25 minutes — session should still exist
	// because Refresh reset the TTL to a full 30 min.
	mr.FastForward(25 * time.Minute)
	got, err := store.Get(ctx, sess.SID)
	require.NoError(t, err)
	assert.NotNil(t, got, "session must still exist after Refresh extended the TTL")
}

// TestSessionStore_GetErrorWhenServerDown mirrors iam-org-membership TestVC_CACHE_012_GetReturnsErrorOnServerDown.
// LLD §14.2 "fail-closed on Valkey stop": a transport failure propagates as an error,
// not as a silent nil (nil would let the service think the session doesn't exist → 401 instead of 503).
func TestSessionStore_GetErrorWhenServerDown(t *testing.T) {
	store, mr := newTestSessionStore(t)
	ctx := context.Background()
	sess := testSession()

	require.NoError(t, store.Create(ctx, sess))
	mr.Close() // tear down the server

	got, err := store.Get(ctx, sess.SID)
	assert.Error(t, err, "transport error must propagate — fail-closed (LLD N-7)")
	assert.Nil(t, got, "nil must be returned alongside the error")
}

func TestSessionStore_Create_TTL_Applied(t *testing.T) {
	store, mr := newTestSessionStore(t)
	ctx := context.Background()
	sess := testSession()

	require.NoError(t, store.Create(ctx, sess))

	// Advance past the 30-min TTL.
	mr.FastForward(31 * time.Minute)

	got, err := store.Get(ctx, sess.SID)
	require.NoError(t, err)
	assert.Nil(t, got, "session must be expired after TTL")
}
