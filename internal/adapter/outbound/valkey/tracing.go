package valkey

import (
	"context"
	"errors"
	"net"
	"strconv"

	"github.com/redis/go-redis/v9"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/tracing"
)

// tracingHook gives every Valkey command (and pipeline) a client span through
// platform-gincommon's tracer (package tracing), so Valkey latency shows up in
// the request trace (LLD §11.3). Keys and arguments are never recorded: they
// hold session IDs, signup IDs and email HMACs.
type tracingHook struct{}

var _ redis.Hook = tracingHook{}

func (tracingHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return next(ctx, network, addr)
	}
}

func (tracingHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		ctx, end := tracing.StartClientSpan(ctx, "valkey "+cmd.Name(),
			"db.system.name", "redis", "db.operation.name", cmd.Name())
		err := next(ctx, cmd)
		end(spanErr(err))
		return err
	}
}

func (tracingHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		ctx, end := tracing.StartClientSpan(ctx, "valkey pipeline",
			"db.system.name", "redis", "db.operation.batch.size", strconv.Itoa(len(cmds)))
		err := next(ctx, cmds)
		end(spanErr(err))
		return err
	}
}

// spanErr drops redis.Nil: "key not found" is a normal answer, not a failure.
func spanErr(err error) error {
	if errors.Is(err, redis.Nil) {
		return nil
	}
	return err
}
