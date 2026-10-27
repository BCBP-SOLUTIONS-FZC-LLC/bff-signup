package valkey

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alicebob/miniredis/v2"
)

func newTestRateLimiter(t *testing.T) (*RateLimiter, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := NewClient(mr.Addr()) // production constructor — identical to iam-org-membership's New(mr.Addr())
	t.Cleanup(func() { _ = rdb.Close() })
	return NewRateLimiter(rdb), mr
}

func TestRateLimiter_IncrIP_CountsAndReturnsResetTime(t *testing.T) {
	limiter, _ := newTestRateLimiter(t)
	ctx := context.Background()

	count1, reset1, err := limiter.IncrIP(ctx, "1.2.3.4")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count1)
	assert.True(t, reset1.After(time.Now()))

	count2, _, err := limiter.IncrIP(ctx, "1.2.3.4")
	require.NoError(t, err)
	assert.Equal(t, int64(2), count2)
}

func TestRateLimiter_IncrEmail_IncrSession_IncrPoll(t *testing.T) {
	limiter, _ := newTestRateLimiter(t)
	ctx := context.Background()

	emailCount, _, err := limiter.IncrEmail(ctx, "hash-of-email")
	require.NoError(t, err)
	assert.Equal(t, int64(1), emailCount)

	sessCount, _, err := limiter.IncrSession(ctx, "10.0.0.1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), sessCount)

	pollCount, _, err := limiter.IncrPoll(ctx, "sid-001")
	require.NoError(t, err)
	assert.Equal(t, int64(1), pollCount)
}

func TestRateLimiter_DifferentKeys_IndependentCounters(t *testing.T) {
	limiter, _ := newTestRateLimiter(t)
	ctx := context.Background()

	for range 3 {
		_, _, err := limiter.IncrIP(ctx, "1.2.3.4")
		require.NoError(t, err)
	}

	// A different IP starts at 1 — counters are fully independent.
	count, _, err := limiter.IncrIP(ctx, "5.6.7.8")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestRateLimiter_TTL_Applied(t *testing.T) {
	limiter, mr := newTestRateLimiter(t)
	ctx := context.Background()

	_, _, err := limiter.IncrIP(ctx, "9.9.9.9")
	require.NoError(t, err)

	// Fast-forward past the 1-hour IP rate-limit window.
	mr.FastForward(61 * time.Minute)

	// Counter resets after TTL.
	count, _, err := limiter.IncrIP(ctx, "9.9.9.9")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "counter must restart at 1 after TTL expiry")
}
