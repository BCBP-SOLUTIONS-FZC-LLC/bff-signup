package service_test

import (
	"context"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/test/fixtures"
)

// recordedSpan starts a request span, returns its context and a function that
// ends it and returns the attributes it collected.
func recordedSpan(t *testing.T) (context.Context, func() map[string]string) {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	ctx, span := tp.Tracer("test").Start(t.Context(), "request")
	require.True(t, trace.SpanFromContext(ctx).SpanContext().IsValid())
	return ctx, func() map[string]string {
		span.End()
		ended := sr.Ended()
		require.Len(t, ended, 1)
		out := map[string]string{}
		for _, kv := range ended[0].Attributes() {
			out[string(kv.Key)] = kv.Value.Emit()
		}
		return out
	}
}

// O-03 / OBS-54: SB-2 puts signup.id, tenant.id and handoff.state on the span,
// and nothing personal.
func TestSubmit_SetsBusinessSpanAttributes_NoPII(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	svc := newSignupService(handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		&fixtures.FakePasswordHasher{}, fixtures.NewFakeRealmProvisioner("kc-001"), fixtures.NewFakeOrgMembership("tid"))
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000070", CSRFHash: "h"}
	in := validInput(sess)

	ctx, finish := recordedSpan(t)
	result, err := svc.Submit(ctx, in)
	require.NoError(t, err)
	attrs := finish()

	assert.Equal(t, sess.SignupID, attrs["signup.id"])
	assert.NotEmpty(t, attrs["tenant.id"])
	assert.Equal(t, string(result.State), attrs["handoff.state"])
	for k, v := range attrs {
		for _, secret := range []string{in.AdminEmail, in.AdminName, in.Password} {
			assert.False(t, strings.Contains(v, secret), "span attribute %s must not carry personal data", k)
		}
	}
}

// O-03 / OBS-54: an SB-3 poll puts signup.id, tenant.id and handoff.state on the span.
func TestGetStatus_SetsBusinessSpanAttributes(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	clk := fixtures.NewFakeClock()
	orch := service.NewHandoffOrchestrator(handoffs, fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"), clk, 0)
	svc := service.NewStatusService(handoffs, fixtures.NewFakeRateLimiter(), orch, clk, nil, 60)
	h := &domain.Handoff{SignupID: "01JAB0000000000000000071", TenantID: "tenant-71", State: domain.StateCompleted}
	require.NoError(t, handoffs.Create(t.Context(), h))

	ctx, finish := recordedSpan(t)
	_, err := svc.GetStatus(ctx, h.SignupID, h.SignupID, "sid")
	require.NoError(t, err)
	attrs := finish()

	assert.Equal(t, "01JAB0000000000000000071", attrs["signup.id"])
	assert.Equal(t, "tenant-71", attrs["tenant.id"])
	assert.Equal(t, "completed", attrs["handoff.state"])
}
