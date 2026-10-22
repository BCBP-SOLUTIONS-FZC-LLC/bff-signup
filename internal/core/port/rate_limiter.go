package port

import (
	"context"
	"time"
)

// RateLimiter enforces the per-IP, per-email, per-session and per-poll limits
// using Valkey INCR counters (LLD §10.2, §4.1).
type RateLimiter interface {
	// IncrIP increments the per-IP new-signup counter and returns (count, resetAt).
	IncrIP(ctx context.Context, ip string) (count int64, resetAt time.Time, err error)
	// IncrEmail increments the per-email attempt counter.
	IncrEmail(ctx context.Context, emailHMAC string) (count int64, resetAt time.Time, err error)
	// IncrSession increments the per-IP session-start counter.
	IncrSession(ctx context.Context, ip string) (count int64, resetAt time.Time, err error)
	// IncrPoll increments the per-session status-poll counter.
	IncrPoll(ctx context.Context, sid string) (count int64, resetAt time.Time, err error)
}
