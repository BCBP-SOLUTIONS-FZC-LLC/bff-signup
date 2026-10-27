//go:build integration

package integration_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/valkey"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

// valkeyClient returns a Redis client backed by:
//  1. The real Valkey at VALKEY_URL (CI service container or 'make docker-up'), OR
//  2. An in-process miniredis server when Valkey is not reachable.
//
// miniredis (same approach as iam-org-membership/internal/adapter/outbound/valkey/cache_test.go)
// implements the Redis protocol in pure Go — no Docker required — so tests
// always run locally without skipping.
func valkeyClient(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv("VALKEY_URL")
	if url == "" {
		url = "redis://localhost:6379/0"
	}
	opts, err := redis.ParseURL(url)
	require.NoError(t, err)
	rdb := redis.NewClient(opts)

	if pingErr := rdb.Ping(context.Background()).Err(); pingErr != nil {
		// Real Valkey unreachable — fall back to in-process miniredis.
		_ = rdb.Close()
		mr := miniredis.RunT(t) // stopped automatically when t ends
		rdb = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	}

	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func TestSessionStore_CreateGetDelete(t *testing.T) {
	rdb := valkeyClient(t)
	store := valkey.NewSessionStore(rdb)
	ctx := context.Background()

	sess := &port.Session{
		SID:       "integ-sid-001",
		CSRFHash:  "sha256hash",
		SignupID:  "01JAB0000000000000000000",
		CreatedAt: time.Now().Unix(),
	}

	require.NoError(t, store.Create(ctx, sess))

	got, err := store.Get(ctx, sess.SID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, sess.SID, got.SID)
	assert.Equal(t, sess.CSRFHash, got.CSRFHash)
	assert.Equal(t, sess.SignupID, got.SignupID)

	require.NoError(t, store.Delete(ctx, sess.SID))
	gone, err := store.Get(ctx, sess.SID)
	require.NoError(t, err)
	assert.Nil(t, gone, "session must be gone after delete")
}

func TestHandoffStore_CreateUpdateGet(t *testing.T) {
	rdb := valkeyClient(t)
	store := valkey.NewHandoffStore(rdb)
	ctx := context.Background()

	h := &domain.Handoff{
		SignupID:  "integ-signup-001",
		TenantID:  "tenant-id-001",
		State:     domain.StateAllocated,
		Slug:      "acme",
		Plan:      domain.PlanPro,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	require.NoError(t, store.Create(ctx, h))

	h.State = domain.StateIdentityCreated
	h.KeycloakUserID = "kc-user-id-001"
	require.NoError(t, store.Update(ctx, h))

	got, err := store.Get(ctx, h.SignupID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, domain.StateIdentityCreated, got.State)
	assert.Equal(t, "kc-user-id-001", got.KeycloakUserID)

	require.NoError(t, store.Delete(ctx, h.SignupID))
}

func TestHandoffStore_Lock_AcquireAndRelease(t *testing.T) {
	rdb := valkeyClient(t)
	store := valkey.NewHandoffStore(rdb)
	ctx := context.Background()

	signupID := "integ-lock-001"

	// First acquire succeeds
	token, acquired, err := store.AcquireLock(ctx, signupID)
	require.NoError(t, err)
	assert.True(t, acquired)
	assert.NotEmpty(t, token)

	// Second acquire while held fails
	_, acquired2, err := store.AcquireLock(ctx, signupID)
	require.NoError(t, err)
	assert.False(t, acquired2, "lock must not be acquired while held")

	// Release with correct token
	require.NoError(t, store.ReleaseLock(ctx, signupID, token))

	// Now acquirable again
	_, acquired3, err := store.AcquireLock(ctx, signupID)
	require.NoError(t, err)
	assert.True(t, acquired3, "lock must be acquirable after release")

	// Cleanup
	_ = store.ReleaseLock(ctx, signupID, token)
}

func TestRateLimiter_IncrAndWindow(t *testing.T) {
	rdb := valkeyClient(t)
	limiter := valkey.NewRateLimiter(rdb)
	ctx := context.Background()

	// Use a unique key so parallel tests don't interfere
	ip := "integ-test-ip-192.0.2.1"

	// Clean up any existing counter
	rdb.Del(ctx, "sbff:rl:ip:"+ip)

	count1, reset1, err := limiter.IncrIP(ctx, ip)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count1)
	assert.True(t, reset1.After(time.Now()))

	count2, _, err := limiter.IncrIP(ctx, ip)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count2)

	// Cleanup
	rdb.Del(ctx, "sbff:rl:ip:"+ip)
}

func TestRateLimiter_AllIncrMethods(t *testing.T) {
	rdb := valkeyClient(t)
	limiter := valkey.NewRateLimiter(rdb)
	ctx := context.Background()

	emailHMAC := "integ-test-email-hmac-001"
	sessionIP := "integ-test-sess-ip-10.0.0.1"
	sid := "integ-test-poll-sid-001"

	rdb.Del(ctx, "sbff:rl:email:"+emailHMAC)
	rdb.Del(ctx, "sbff:rl:sess:"+sessionIP)
	rdb.Del(ctx, "sbff:rl:poll:"+sid)

	emailCount, _, err := limiter.IncrEmail(ctx, emailHMAC)
	require.NoError(t, err)
	assert.Equal(t, int64(1), emailCount)

	sessCount, _, err := limiter.IncrSession(ctx, sessionIP)
	require.NoError(t, err)
	assert.Equal(t, int64(1), sessCount)

	pollCount, _, err := limiter.IncrPoll(ctx, sid)
	require.NoError(t, err)
	assert.Equal(t, int64(1), pollCount)

	rdb.Del(ctx, "sbff:rl:email:"+emailHMAC)
	rdb.Del(ctx, "sbff:rl:sess:"+sessionIP)
	rdb.Del(ctx, "sbff:rl:poll:"+sid)
}

func TestPing_ReturnsNilWhenReachable(t *testing.T) {
	rdb := valkeyClient(t)
	err := valkey.Ping(context.Background(), rdb)
	assert.NoError(t, err)
}

func TestSessionStore_Refresh(t *testing.T) {
	rdb := valkeyClient(t)
	store := valkey.NewSessionStore(rdb)
	ctx := context.Background()

	sess := &port.Session{
		SID:       "integ-refresh-sid-001",
		CSRFHash:  "hash",
		SignupID:  "01JAB0000000000000000000",
		CreatedAt: time.Now().Unix(),
	}
	require.NoError(t, store.Create(ctx, sess))
	require.NoError(t, store.Refresh(ctx, sess.SID))

	got, err := store.Get(ctx, sess.SID)
	require.NoError(t, err)
	require.NotNil(t, got)

	require.NoError(t, store.Delete(ctx, sess.SID))
}
