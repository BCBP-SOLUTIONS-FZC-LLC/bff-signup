package valkey

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const valkeyOpTimeout = 50 * time.Millisecond

// runOp executes fn under a 50 ms deadline, retrying once on transient errors
// (LLD §9.3: Valkey ops 50 ms, 1 retry on network error).
// redis.Nil is a valid "key not found" response — never retried.
// If the parent context is already done the first error is returned immediately.
func runOp(parent context.Context, fn func(context.Context) error) error {
	var lastErr error
	for range 2 {
		opCtx, cancel := context.WithTimeout(parent, valkeyOpTimeout)
		err := fn(opCtx)
		cancel()
		if err == nil || errors.Is(err, redis.Nil) {
			return err
		}
		lastErr = err
		if parent.Err() != nil {
			break
		}
	}
	return lastErr
}

// Ping checks Valkey connectivity (used by /readyz).
func Ping(ctx context.Context, rdb *redis.Client) error {
	return rdb.Ping(ctx).Err()
}

// Key builders (LLD §4.1)
func sessKey(sid string) string   { return "sbff:sess:" + sid }
func handoffKey(id string) string { return "sbff:handoff:" + id }
func lockKey(id string) string    { return "sbff:lock:" + id }
