package valkey

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

const (
	ipRateTTL    = 1 * time.Hour
	emailRateTTL = 24 * time.Hour
	sessRateTTL  = 1 * time.Hour
	pollRateTTL  = 1 * time.Minute
)

// incrFixedWindowScript increments a counter and — on the FIRST increment only —
// sets the TTL, creating a fixed window. Returns {count, remaining_ttl_seconds}.
// Using INCR+EXPIRE in a pipeline would renew the TTL on every call (sliding window),
// which lets callers reset the window indefinitely by spacing requests just before expiry.
var incrFixedWindowScript = redis.NewScript(`
local count = redis.call("INCR", KEYS[1])
if count == 1 then
    redis.call("EXPIRE", KEYS[1], tonumber(ARGV[1]))
end
local ttl = redis.call("TTL", KEYS[1])
return {count, ttl}
`)

// RateLimiter implements port.RateLimiter using Valkey INCR counters (LLD §10.2).
type RateLimiter struct {
	rdb *redis.Client
}

var _ port.RateLimiter = (*RateLimiter)(nil)

func NewRateLimiter(rdb *redis.Client) *RateLimiter { return &RateLimiter{rdb} }

func (r *RateLimiter) IncrIP(ctx context.Context, ip string) (int64, time.Time, error) {
	return r.incr(ctx, fmt.Sprintf("sbff:rl:ip:%s", ip), ipRateTTL)
}

func (r *RateLimiter) IncrEmail(ctx context.Context, emailHMAC string) (int64, time.Time, error) {
	return r.incr(ctx, fmt.Sprintf("sbff:rl:email:%s", emailHMAC), emailRateTTL)
}

func (r *RateLimiter) IncrSession(ctx context.Context, ip string) (int64, time.Time, error) {
	return r.incr(ctx, fmt.Sprintf("sbff:rl:sess:%s", ip), sessRateTTL)
}

func (r *RateLimiter) IncrPoll(ctx context.Context, sid string) (int64, time.Time, error) {
	return r.incr(ctx, fmt.Sprintf("sbff:rl:poll:%s", sid), pollRateTTL)
}

func (r *RateLimiter) incr(ctx context.Context, key string, ttl time.Duration) (int64, time.Time, error) {
	var count, ttlSecs int64
	err := runOp(ctx, func(opCtx context.Context) error {
		res, e := incrFixedWindowScript.Run(opCtx, r.rdb, []string{key}, int64(ttl.Seconds())).Result()
		if e != nil {
			return e
		}
		vals, ok := res.([]interface{})
		if !ok || len(vals) != 2 {
			return fmt.Errorf("rate limit script returned unexpected result")
		}
		var ok1, ok2 bool
		count, ok1 = vals[0].(int64)
		ttlSecs, ok2 = vals[1].(int64)
		if !ok1 || !ok2 {
			return fmt.Errorf("rate limit script returned non-integer values (got %T, %T)", vals[0], vals[1])
		}
		return nil
	})
	if err != nil {
		return 0, time.Time{}, err
	}
	if ttlSecs <= 0 {
		// Defensive fallback: key vanished between INCR and TTL, or has no expiry.
		ttlSecs = int64(ttl.Seconds())
	}
	resetAt := time.Now().Add(time.Duration(ttlSecs) * time.Second)
	return count, resetAt, nil
}
