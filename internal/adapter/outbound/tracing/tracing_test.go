package tracing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// recordSpans installs an SDK provider with an in-memory recorder, standing in
// for what gincommon.InitTracingWithConfig installs in production.
func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTracerProvider(prevTP); otel.SetTextMapPropagator(prevProp) })
	return rec
}

func attr(s sdktrace.ReadOnlySpan, key string) attribute.Value {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value
		}
	}
	return attribute.Value{}
}

func TestTransport_ClientSpanAndTraceparent(t *testing.T) {
	rec := recordSpans(t)
	var gotTraceparent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceparent = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	ctx, parent := tracer().Start(context.Background(), "request")
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/v1/internal/tenants?x=secret", nil)
	resp, err := (&http.Client{Transport: Transport("org_membership", true)}).Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	parent.End()

	assert.NotEmpty(t, gotTraceparent, "callee must receive the W3C traceparent")
	assert.Empty(t, req.Header.Get("traceparent"), "the caller's request is not mutated")

	spans := rec.Ended()
	require.Len(t, spans, 2)
	client := spans[0]
	assert.Equal(t, "POST org_membership", client.Name())
	assert.Equal(t, trace.SpanKindClient, client.SpanKind())
	assert.Equal(t, parent.SpanContext().SpanID(), client.Parent().SpanID(), "child of the request span")
	assert.Equal(t, int64(201), attr(client, "http.response.status_code").AsInt64())
	assert.Equal(t, "/api/v1/internal/tenants", attr(client, "url.path").AsString(), "no query string recorded")
	assert.Equal(t, codes.Unset, client.Status().Code)
	assert.Contains(t, gotTraceparent, client.SpanContext().TraceID().String())
}

func TestTransport_NoPropagationToThirdParties(t *testing.T) {
	recordSpans(t)
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.Header.Get("traceparent") }))
	defer srv.Close()

	ctx, parent := tracer().Start(context.Background(), "request")
	defer parent.End()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, nil)
	resp, err := (&http.Client{Transport: Transport("turnstile", false)}).Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()

	assert.Empty(t, got, "Turnstile must not receive our trace IDs")
}

func TestTransport_5xxAndTransportErrorsMarkTheSpanFailed(t *testing.T) {
	rec := recordSpans(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer srv.Close()
	client := &http.Client{Transport: Transport("billing", true)}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, nil)
	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()

	req, _ = http.NewRequestWithContext(context.Background(), http.MethodPost, "http://127.0.0.1:1", nil)
	resp, err = client.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)

	spans := rec.Ended()
	require.Len(t, spans, 2)
	assert.Equal(t, codes.Error, spans[0].Status().Code, "503")
	assert.Equal(t, codes.Error, spans[1].Status().Code, "connection refused")
}

func TestAnnotate(t *testing.T) {
	rec := recordSpans(t)
	Annotate(context.Background(), "signup.id", "x") // no span: no-op, no panic

	ctx, span := tracer().Start(context.Background(), "request")
	Annotate(ctx, "signup.id", "01JAB", "handoff.state", "completed", "dangling")
	span.End()

	s := rec.Ended()[0]
	assert.Equal(t, "01JAB", attr(s, "signup.id").AsString())
	assert.Equal(t, "completed", attr(s, "handoff.state").AsString())
	assert.Len(t, s.Attributes(), 2, "an odd trailing key is ignored")
}
