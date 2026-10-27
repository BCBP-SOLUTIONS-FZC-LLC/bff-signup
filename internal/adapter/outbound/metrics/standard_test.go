package metrics

// TestStandard_Registration validates the production registration order and
// asserts that every collector is exported. Mirrors iam-org-membership's
// standard_test.go (internal/adapter/outbound/metrics/standard_test.go).
//
// With METRICS_SCRAPE_OUT set, it writes the full scrape for `metricslint check`
// so the CI naming-convention script can validate suffixes and labels.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
)

// ensureRegistered initialises gincommon and calls Register() exactly once
// per test binary, matching the production startup order (cmd/server/main.go).
//
// DOMAIN NOTE: platform-gincommon v2's metric registry validates the domain
// against a fixed vocabulary [iam workflow billing tender_management
// notifications documents]. "bff" is not yet registered. Until the
// observability governance body adds "bff" to the Platform Observability
// Registry, tests use OBSERVABILITY_DOMAIN=workflow as a placeholder.
// Production deployment requires a platform registry PR to add "bff" first
// (mirrors the same workaround in internal/adapter/inbound/http/router_test.go).
func ensureRegistered(t *testing.T) {
	t.Helper()
	t.Setenv("OBSERVABILITY_DOMAIN", "workflow") // placeholder; replace with "bff" once registry adds it
	t.Setenv("APP_ENV", "test")
	// gincommon.ObservabilityMiddlewares fixes the {domain,service,environment}
	// const labels; Register() then builds all collectors on that identity.
	gincommon.ObservabilityMiddlewares(gincommon.Config{ServiceName: "bff-signup"})
	Register("test")
}

func TestStandard_AllCollectorsRegistered(t *testing.T) {
	ensureRegistered(t)

	// Exercise every collector so the scrape includes all label sets.
	exerciseEveryCollector(t)

	gathered, err := gincommon.MetricsGatherer().Gather()
	require.NoError(t, err)
	names := map[string]bool{}
	for _, mf := range gathered {
		names[mf.GetName()] = true
	}

	// Tier 1 (Proposed — shadow-emitted, not yet authoritative)
	assert.True(t, names["platform_dependency_request_seconds"],
		"platform_dependency_request_seconds must be exported (Tier 1 Proposed)")

	// Tier 3 — all LLD §11.2 metrics
	for _, want := range []string{
		"bff_signup_form_started_total",
		"bff_signup_form_submitted_total",
		"bff_signup_provisioning_succeeded_total",
		"bff_signup_provisioning_failed_total",
		"bff_signup_rate_limited_total",
		"bff_signup_captcha_challenged_total",
		"bff_signup_handoff_resumed_total",
		"bff_signup_dependency_call_duration_seconds",
		"bff_signup_password_hash_duration_seconds",
	} {
		assert.True(t, names[want], "%s must be exported (LLD §11.2)", want)
	}

	// Verify no version label on any bff_signup_* or platform_* series
	// (build version belongs only on platform_build_info — rule 8).
	// Go runtime metrics (go_info, go_gc_*) are excluded — they carry their
	// own version label by design and are not under the platform standard.
	for _, mf := range gathered {
		name := mf.GetName()
		if !strings.HasPrefix(name, "platform_") &&
			!strings.HasPrefix(name, "bff_") &&
			!strings.HasPrefix(name, "iam_") {
			continue // skip runtime metrics (go_*, process_*, etc.)
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "version" {
					assert.Equal(t, "platform_build_info", name,
						"version label must only appear on platform_build_info")
				}
			}
		}
	}

	// Optionally write scrape output for metricslint check (CI).
	if out := os.Getenv("METRICS_SCRAPE_OUT"); out != "" {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil)
		req.Header.Set("Accept", "text/plain")
		gincommon.MetricsHandler().ServeHTTP(rec, req)
		require.NoError(t, os.WriteFile(out, rec.Body.Bytes(), 0o600))
	}
}

// exerciseEveryCollector records one sample on every collector with
// representative production label values, so the scrape for metricslint
// includes all label sets (an unsampled vec has no series).
func exerciseEveryCollector(t *testing.T) {
	t.Helper()
	IncFormStarted("trial")
	IncFormSubmitted("trial")
	IncProvisioningSucceeded("trial", "inline")
	IncProvisioningSucceeded("trial", "resume")
	IncProvisioningSucceeded("trial", "slug_change")
	IncProvisioningFailed("trial", "org_membership", "exhausted")
	IncProvisioningFailed("trial", "realm_provisioner", "unavailable")
	IncRateLimited("ip")
	IncRateLimited("email")
	IncRateLimited("session")
	IncRateLimited("poll")
	IncCaptchaChallenged("pass")
	IncCaptchaChallenged("fail")
	IncCaptchaChallenged("error")
	IncHandoffResumed("poll")
	IncHandoffResumed("resubmit")
	IncHandoffResumed("slug_change")
	ObserveDependencyCall("realm_provisioner", "provision_trial", 500*time.Millisecond, nil, nil)
	ObserveDependencyCall("org_membership", "provision_tenant", 200*time.Millisecond, &http.Response{StatusCode: 503}, nil)
	ObservePasswordHash(150 * time.Millisecond)

	// DependencyErrorReason (used by call sites to classify errors)
	assert.Equal(t, "transport", DependencyErrorReason(assert.AnError))
}
