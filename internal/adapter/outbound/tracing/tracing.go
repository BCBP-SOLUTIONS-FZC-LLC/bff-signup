// Package tracing is bff-signup's only door to distributed tracing. Every span
// it creates comes from platform-gincommon's tracer (gincommon.NewTracer, the
// TracerProvider installed by gincommon.InitTracingWithConfig and
// ObservabilityMiddlewares), and trace context is propagated with
// gincommon.InjectTraceContext — so all traces pass through platform-gincommon.
//
// It is also the only non-test package allowed to import OpenTelemetry types
// (check-log-sink.sh, TRACES-2): gincommon exports Tracer, Span, RecordSpanError
// and InjectTraceContext, but not span options or attribute constructors, so
// those few type-level uses are confined here.
//
// Attribute values must never carry personal data or secrets (SB-INV-2, LLD
// §11.3): no email, name, password, token, cookie, Valkey key or URL query.
package tracing

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
)

// scope is the instrumentation scope of every span this service starts.
const scope = "bff-signup"

// tracer returns gincommon's tracer for this service, looked up when a span
// starts (OpenTelemetry caches it), so spans always use the TracerProvider that
// is current then — never one captured before gincommon installed its own.
func tracer() gincommon.Tracer { return gincommon.NewTracer(scope) }

// Annotate sets string attributes on the span carried by ctx, given as
// key, value pairs (an odd trailing key is ignored). No-op without a recording
// span. Use it for IDs and states only, e.g. Annotate(ctx, "signup.id", id).
func Annotate(ctx context.Context, kv ...string) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	span.SetAttributes(stringAttrs(kv)...)
}

// StartClientSpan starts a client span named name, as a child of the span in
// ctx, with string attributes given as key, value pairs. The returned end
// function records err (when non-nil) on the span and ends it.
func StartClientSpan(ctx context.Context, name string, kv ...string) (context.Context, func(err error)) {
	ctx, span := tracer().Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(stringAttrs(kv)...))
	return ctx, func(err error) {
		if err != nil {
			gincommon.RecordSpanError(ctx, err, "dependency call failed")
		}
		span.End()
	}
}

// Transport returns an http.RoundTripper for outbound calls to dependency
// (the same logical name as the metrics label, e.g. "realm_provisioner").
// Each request gets a client span "<METHOD> <dependency>"; with propagate set,
// the W3C traceparent is injected so the callee joins the trace. Use
// propagate=false for third parties (Turnstile) that should not see our trace IDs.
func Transport(dependency string, propagate bool) http.RoundTripper {
	return &transport{base: http.DefaultTransport, dependency: dependency, propagate: propagate}
}

type transport struct {
	base       http.RoundTripper
	dependency string
	propagate  bool
}

// RoundTrip implements http.RoundTripper. The request is cloned before headers
// are added, as the RoundTripper contract requires.
func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, end := StartClientSpan(req.Context(), req.Method+" "+t.dependency,
		"http.request.method", req.Method,
		"server.address", req.URL.Hostname(),
		"url.path", req.URL.Path,
		"peer.service", t.dependency,
	)
	out := req.Clone(ctx)
	if t.propagate {
		gincommon.InjectTraceContext(ctx, out.Header)
	}
	resp, err := t.base.RoundTrip(out)
	if resp != nil {
		trace.SpanFromContext(ctx).SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
		if err == nil && resp.StatusCode >= http.StatusInternalServerError {
			end(errors.New("HTTP " + strconv.Itoa(resp.StatusCode)))
			return resp, nil
		}
	}
	end(err)
	return resp, err
}

func stringAttrs(kv []string) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		attrs = append(attrs, attribute.String(kv[i], kv[i+1]))
	}
	return attrs
}
