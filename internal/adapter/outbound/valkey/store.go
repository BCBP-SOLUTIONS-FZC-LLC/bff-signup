package valkey

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// Ping checks Valkey connectivity (used by /readyz).
func Ping(ctx context.Context, rdb *redis.Client) error {
	return rdb.Ping(ctx).Err()
}

// Key builders (LLD §4.1)
func sessKey(sid string) string   { return "sbff:sess:" + sid }
func handoffKey(id string) string { return "sbff:handoff:" + id }
func lockKey(id string) string    { return "sbff:lock:" + id }
