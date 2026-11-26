package valkey

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestNewClient_TracesCommandsWithoutKeys(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	mr := miniredis.RunT(t)
	rdb := NewClient("redis://" + mr.Addr())
	t.Cleanup(func() { _ = rdb.Close() })

	ctx, parent := otel.Tracer("test").Start(context.Background(), "request")
	require.NoError(t, rdb.Set(ctx, "sbff:sess:secret-sid", "v", 0).Err())
	assert.ErrorIs(t, rdb.Get(ctx, "sbff:missing").Err(), redis.Nil)
	_, err := rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		p.Incr(ctx, "sbff:rl:ip:1.2.3.4")
		p.Expire(ctx, "sbff:rl:ip:1.2.3.4", 0)
		return nil
	})
	require.NoError(t, err)
	parent.End()

	byName := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range rec.Ended() {
		byName[s.Name()] = s
		for _, kv := range s.Attributes() {
			assert.NotContains(t, kv.Value.AsString(), "sbff:", "keys must never be span attributes")
		}
	}
	require.Contains(t, byName, "valkey set")
	require.Contains(t, byName, "valkey get")
	require.Contains(t, byName, "valkey pipeline")
	assert.Equal(t, parent.SpanContext().TraceID(), byName["valkey set"].SpanContext().TraceID())
	assert.Equal(t, codes.Unset, byName["valkey get"].Status().Code, "redis.Nil (not found) is not an error")
}
