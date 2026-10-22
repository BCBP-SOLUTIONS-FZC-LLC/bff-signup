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
	pipe := r.rdb.Pipeline()
	incrCmd := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, time.Time{}, err
	}
	return incrCmd.Val(), time.Now().Add(ttl), nil
}
