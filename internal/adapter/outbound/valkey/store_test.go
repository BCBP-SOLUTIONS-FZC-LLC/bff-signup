package valkey

// Mirrors the pattern used in iam-org-membership/internal/adapter/outbound/valkey/cache_test.go:
// miniredis spins up an in-process Redis server — no Docker, no build tags.

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRDB starts an in-process miniredis and returns a client built with
// NewClient — the same constructor used in production — so timeout defaults
// are verified here too. Matches the iam-org-membership pattern exactly:
//
//	mr := miniredis.RunT(t)
//	c := New(mr.Addr())    ← iam-org-membership
//	rdb := NewClient(mr.Addr())  ← bff-signup (same idea, same one-liner)
func newTestRDB(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := NewClient(mr.Addr()) // production constructor — verifies timeout defaults
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

// ── store.go — runOp, Ping, key builders ──────────────────────────────────────

func TestRunOp_SucceedsOnFirstTry(t *testing.T) {
	rdb := newTestRDB(t)
	called := 0
	err := runOp(context.Background(), func(ctx context.Context) error {
		called++
		return rdb.Ping(ctx).Err()
	})
	require.NoError(t, err)
	assert.Equal(t, 1, called, "should succeed without a retry")
}

func TestRunOp_RetriesOnTransientError(t *testing.T) {
	// First call fails, second succeeds.
	attempts := 0
	err := runOp(context.Background(), func(_ context.Context) error {
		attempts++
		if attempts < 2 {
			return redis.ErrClosed // transient — triggers retry
		}
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 2, attempts, "should retry once on transient error")
}

func TestRunOp_CancelledContextStopsRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already done

	attempts := 0
	err := runOp(ctx, func(_ context.Context) error {
		attempts++
		return redis.ErrClosed
	})
	require.Error(t, err)
	assert.Equal(t, 1, attempts, "should stop after first error when context is done")
}

func TestPing_SucceedsWhenServerUp(t *testing.T) {
	rdb := newTestRDB(t)
	err := Ping(context.Background(), rdb)
	assert.NoError(t, err)
}

func TestKeyBuilders_Format(t *testing.T) {
	assert.Equal(t, "sbff:sess:abc", sessKey("abc"))
	assert.Equal(t, "sbff:handoff:xyz", handoffKey("xyz"))
	assert.Equal(t, "sbff:lock:xyz", lockKey("xyz"))
}

func TestNewClient_TimeoutDefaultsMatchSpec(t *testing.T) {
	// Mirrors iam-org-membership TestVC_CACHE_002_TimeoutsHaveTightDefaults.
	// A slow Valkey must degrade to a fast miss, not stall the request.
	mr := miniredis.RunT(t)
	rdb := NewClient(mr.Addr())
	t.Cleanup(func() { _ = rdb.Close() })

	opts := rdb.Options()
	assert.Equal(t, 100*time.Millisecond, opts.DialTimeout, "DialTimeout must be 100ms")
	assert.Equal(t, 50*time.Millisecond, opts.ReadTimeout, "ReadTimeout must be 50ms")
	assert.Equal(t, 50*time.Millisecond, opts.WriteTimeout, "WriteTimeout must be 50ms")
}

// TestPing_FailsAfterServerStop mirrors iam-org-membership TestVC_CACHE_010_HealthWhenServerDown.
// LLD §14.2: "fail-closed on Valkey stop" (N-7: Valkey unavailable → 503, fail-closed).
func TestPing_FailsAfterServerStop(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := NewClient(mr.Addr())
	t.Cleanup(func() { _ = rdb.Close() })

	assert.NoError(t, Ping(context.Background(), rdb), "must be reachable while server is up")
	mr.Close() // tear down the server
	assert.Error(t, Ping(context.Background(), rdb), "must error after server is stopped — fail-closed")
}

func TestRunOp_RedisNilIsNotRetried(t *testing.T) {
	calls := 0
	err := runOp(context.Background(), func(_ context.Context) error {
		calls++
		return redis.Nil // a "miss" — not transient, should not retry
	})
	assert.ErrorIs(t, err, redis.Nil)
	assert.Equal(t, 1, calls, "redis.Nil must NOT be retried")
}
