// Package metrics registers Prometheus counters and histograms for the
// bff-signup service, per the Enterprise Platform Observability Standard's
// three-tier hierarchy:
//
//   - Tier 1 — platform_* : a concept common across MULTIPLE DOMAINS
//     (IAM, Workflow, Billing, BFF, …) with identical semantics.
//     Carries no domain or service name in the metric itself; "domain",
//     "service", "environment" are injected centrally as ConstLabels.
//   - Tier 2 — bff_* : a concept shared across multiple services WITHIN
//     the bff domain. Currently unused — only one BFF service exists.
//   - Tier 3 — bff_signup_* : behaviour unique to this service (signup
//     funnel, handoff state machine, rate limiting, CAPTCHA, etc.).
//     The service identity is baked into the name itself.
//
// Every counter ends in _total; every histogram ends in _seconds. The
// "domain"/"service"/"environment" labels are never left to individual
// Inc()/Observe() call sites — they are baked in once via ConstLabels by
// gincommon's constructors (rule 8). No collector carries a version label;
// the build version lives only on platform_build_info.
//
// Tier-1 platform_* collectors come from the central Platform Observability
// Registry via gincommon.NewShared*Vec. Tier-3 bff_signup_* collectors use
// gincommon.NewCounterVec / NewHistogramVec, which validate the bff_signup_
// prefix, suffixes and prohibited labels and inject the identity const labels.
// "bff" was added to the platform-gincommon domain registry in v2.2.0 (PR #49).
//
// Registration order: call Register() after gincommon.ObservabilityMiddlewares
// (which fixes the {domain, service, environment} identity) and before
// /metrics is served. Mirrors iam-org-membership/internal/adapter/outbound/metrics.
package metrics

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
)

var (
	// ── Tier 1 — platform_* (shared across domains) ─────────────────────

	// DependencyRequestSeconds (platform_dependency_request_seconds{
	// dependency,operation,outcome}, Proposed) times synchronous calls to
	// external services: RP-1, I-1, eligibility, PUB-1, Turnstile, Catalog.
	// Proposed — authoritative alerting source is bff_signup_dependency_call_
	// duration_seconds (Tier 3 predecessor) until registry ratification.
	DependencyRequestSeconds *prometheus.HistogramVec

	// ── Tier 3 — bff_signup_* (unique to this service) ───────────────────

	// FormStarted (bff_signup_form_started_total{mode}) counts SB-1 sessions
	// successfully created. mode="trial" in Phase 1; "paid" in Phase 1.5.
	// LLD §11.2.
	FormStarted *prometheus.CounterVec

	// FormSubmitted (bff_signup_form_submitted_total{mode}) counts SB-2
	// requests received (before any processing — a baseline to compute
	// funnel drop-off). LLD §11.2.
	FormSubmitted *prometheus.CounterVec

	// ProvisioningSucceeded (bff_signup_provisioning_succeeded_total{mode,strategy})
	// counts signups that reached state=completed. strategy is "inline" (I-1
	// succeeded within the SB-2 request), "resume" (SB-3 poll completed it),
	// or "slug_change" (SB-4 completed it). LLD §11.2.
	// Note: label was renamed path→strategy because "path" is a prohibited label
	// in the platform registry (unbounded cardinality risk for URL-like paths).
	ProvisioningSucceeded *prometheus.CounterVec

	// ProvisioningFailed (bff_signup_provisioning_failed_total{mode,stage,reason})
	// counts signups stuck in state=failed (15-min resume window exhausted).
	// stage is one of: eligibility, realm_provisioner, org_membership.
	// reason is one of: unavailable, contract_error, exhausted. LLD §11.2.
	ProvisioningFailed *prometheus.CounterVec

	// RateLimited (bff_signup_rate_limited_total{scope}) counts requests
	// rejected by rate limits. scope is: ip, email, session, poll. LLD §11.2.
	RateLimited *prometheus.CounterVec

	// CaptchaChallenged (bff_signup_captcha_challenged_total{result}) counts
	// Turnstile siteverify calls. result is: pass, fail, error. LLD §11.2.
	CaptchaChallenged *prometheus.CounterVec

	// HandoffResumed (bff_signup_handoff_resumed_total{trigger}) counts I-1
	// retry attempts on an existing handoff. trigger is: poll (SB-3),
	// resubmit (SB-2 on an existing session), slug_change (SB-4). LLD §11.2.
	HandoffResumed *prometheus.CounterVec

	// DependencyCallDurationSeconds (bff_signup_dependency_call_duration_seconds
	// {dependency,outcome}) is the Tier 3 predecessor of
	// platform_dependency_request_seconds (Proposed — registry.go).
	// Authoritative alerting/SLO source for dependency latency until the Tier 1
	// entry is ratified. dependency: realm_provisioner, org_membership,
	// turnstile, catalog. outcome: success, error. LLD §11.2.
	DependencyCallDurationSeconds *prometheus.HistogramVec

	// passwordHashVec is the backing HistogramVec for PasswordHashDurationSeconds.
	// No dynamic labels — WithLabelValues() is called with no args. Using Vec
	// rather than a singleton histogram because gincommon.NewHistogramVec is the
	// only available no-label histogram constructor in platform-gincommon v2.
	passwordHashVec *prometheus.HistogramVec
)

// ──────────────────────────────────────────────────────────────────────────────
// Nil-safe helpers — identical call convention to iam-org-membership:
// collectors are nil until Register() runs; these helpers are safe to call
// before registration (e.g. in unit tests that never call Register()).
// ──────────────────────────────────────────────────────────────────────────────

// IncFormStarted records one SB-1 session successfully created.
func IncFormStarted(mode string) {
	if FormStarted != nil {
		FormStarted.WithLabelValues(mode).Inc()
	}
}

// IncFormSubmitted records one SB-2 request received.
func IncFormSubmitted(mode string) {
	if FormSubmitted != nil {
		FormSubmitted.WithLabelValues(mode).Inc()
	}
}

// IncProvisioningSucceeded records one completed signup.
// strategy: "inline" | "resume" | "slug_change".
func IncProvisioningSucceeded(mode, strategy string) {
	if ProvisioningSucceeded != nil {
		ProvisioningSucceeded.WithLabelValues(mode, strategy).Inc()
	}
}

// IncProvisioningFailed records one signup that exhausted the resume window.
// stage: "eligibility" | "realm_provisioner" | "org_membership".
// reason: "unavailable" | "contract_error" | "exhausted".
func IncProvisioningFailed(mode, stage, reason string) {
	if ProvisioningFailed != nil {
		ProvisioningFailed.WithLabelValues(mode, stage, reason).Inc()
	}
}

// IncRateLimited records one rate-limit rejection.
// scope: "ip" | "email" | "session" | "poll".
func IncRateLimited(scope string) {
	if RateLimited != nil {
		RateLimited.WithLabelValues(scope).Inc()
	}
}

// IncCaptchaChallenged records one Turnstile siteverify call.
// result: "pass" | "fail" | "error".
func IncCaptchaChallenged(result string) {
	if CaptchaChallenged != nil {
		CaptchaChallenged.WithLabelValues(result).Inc()
	}
}

// IncHandoffResumed records one I-1 retry on an existing handoff.
// trigger: "poll" | "resubmit" | "slug_change".
func IncHandoffResumed(trigger string) {
	if HandoffResumed != nil {
		HandoffResumed.WithLabelValues(trigger).Inc()
	}
}

// ObserveDependencyCall records one synchronous call to a peer service on both
// the authoritative Tier-3 bff_signup_dependency_call_duration_seconds and
// the Proposed Tier-1 platform_dependency_request_seconds.
// dependency: "realm_provisioner" | "org_membership" | "turnstile" | "catalog".
// operation: the client endpoint in snake_case (e.g. "provision_trial").
// outcome: "success" | "error" (error = transport failure or 5xx).
// resp may be nil when err is set.
func ObserveDependencyCall(dependency, operation string, dur time.Duration, resp *http.Response, err error) {
	seconds := dur.Seconds()
	outcome := "success"
	if err != nil || resp == nil || resp.StatusCode >= 500 {
		outcome = "error"
	}
	op := operationLabel(operation)
	if DependencyCallDurationSeconds != nil {
		DependencyCallDurationSeconds.WithLabelValues(dependency, outcome).Observe(seconds)
	}
	if DependencyRequestSeconds != nil {
		DependencyRequestSeconds.WithLabelValues(dependency, op, outcome).Observe(seconds)
	}
}

// ObservePasswordHash records one PBKDF2-SHA512 hash duration.
func ObservePasswordHash(dur time.Duration) {
	if passwordHashVec != nil {
		passwordHashVec.WithLabelValues().Observe(dur.Seconds())
	}
}

// DependencyErrorReason classifies a transport error as "timeout" or "transport".
func DependencyErrorReason(err error) string {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	return "transport"
}

// operationLabel normalises an operation name to snake_case for Prometheus
// label consistency (callers may use kebab-case endpoint names).
func operationLabel(op string) string { return strings.ReplaceAll(op, "-", "_") }

// ──────────────────────────────────────────────────────────────────────────────
// Registration
// ──────────────────────────────────────────────────────────────────────────────

var (
	registerMu   sync.Mutex
	registerDone bool
)

// Register builds and registers every collector onto gincommon's registerer.
// Call after gincommon.ObservabilityMiddlewares (which fixes the {domain,
// service, environment} identity) and before /metrics is served.
// Idempotent; no-op until gincommon is initialised.
func Register(_ string) {
	registerMu.Lock()
	defer registerMu.Unlock()
	if registerDone || len(gincommon.MetricsConstLabels()) == 0 {
		return
	}
	registerShared()
	registerServiceMetrics()
	prewarm()
	registerDone = true
}

// registerShared builds Tier 1 platform_* collectors from the central registry.
func registerShared() {
	DependencyRequestSeconds = mustShared(gincommon.NewSharedHistogramVec(nil, "platform_dependency_request_seconds"))
}

// mustShared returns the collector gincommon built, or the existing one if
// platform-events registered it first (same descriptors → reuse, not error).
func mustShared[C prometheus.Collector](c C, err error) C {
	if err == nil {
		return c
	}
	var are prometheus.AlreadyRegisteredError
	if errors.As(err, &are) {
		if existing, ok := are.ExistingCollector.(C); ok {
			return existing
		}
	}
	panic("metrics: register shared platform collector: " + err.Error())
}

// must panics on a Tier 3 construction error — a wrong name, suffix or label
// is a programming error caught at startup. Mirrors iam-org-membership.
func must[C any](c C, err error) C {
	if err != nil {
		panic("metrics: register service collector: " + err.Error())
	}
	return c
}

// registerServiceMetrics builds all Tier 3 bff_signup_* collectors using
// gincommon.NewCounterVec / NewHistogramVec — the same constructors used by
// iam-org-membership for its iam_org_membership_* metrics. These validate the
// bff_signup_ prefix, _total/_seconds suffixes and prohibited labels, and
// inject the {domain, service, environment} const labels automatically.
// "bff" was added to the platform-gincommon domain registry in v2.2.0 (PR #49),
// making this identical to iam-org-membership's pattern.
func registerServiceMetrics() {
	FormStarted = must(gincommon.NewCounterVec(nil,
		"bff_signup_form_started_total",
		"SB-1 sessions successfully created, by mode (trial in Phase 1; paid in Phase 1.5). Baseline for signup funnel (LLD §11.2).",
		"mode"))

	FormSubmitted = must(gincommon.NewCounterVec(nil,
		"bff_signup_form_submitted_total",
		"SB-2 requests received before any processing, by mode. Paired with bff_signup_provisioning_succeeded_total for funnel drop-off (LLD §11.2).",
		"mode"))

	ProvisioningSucceeded = must(gincommon.NewCounterVec(nil,
		"bff_signup_provisioning_succeeded_total",
		"Signups that reached state=completed, by mode and strategy: inline (I-1 succeeded within SB-2), resume (SB-3 poll completed it), slug_change (SB-4 completed it). LLD §11.2.",
		"mode", "strategy"))

	ProvisioningFailed = must(gincommon.NewCounterVec(nil,
		"bff_signup_provisioning_failed_total",
		"Signups stuck in state=failed after exhausting the 15-min resume window, by mode, stage (eligibility|realm_provisioner|org_membership) and reason (unavailable|contract_error|exhausted). LLD §11.2.",
		"mode", "stage", "reason"))

	RateLimited = must(gincommon.NewCounterVec(nil,
		"bff_signup_rate_limited_total",
		"Requests rejected by abuse controls, by scope: ip (3/h per IP, SB-2), email (5/24h, SB-2), session (20/h per IP, SB-1), poll (60/min per session, SB-3). LLD §11.2.",
		"scope"))

	CaptchaChallenged = must(gincommon.NewCounterVec(nil,
		"bff_signup_captcha_challenged_total",
		"Turnstile siteverify calls, by result: pass (success=true), fail (success=false), error (transport/HTTP error). LLD §11.2.",
		"result"))

	HandoffResumed = must(gincommon.NewCounterVec(nil,
		"bff_signup_handoff_resumed_total",
		"I-1 retry attempts on an existing handoff, by trigger: poll (SB-3), resubmit (SB-2 same-session retry), slug_change (SB-4). LLD §11.2.",
		"trigger"))

	// Tier 3 predecessor of the Proposed Tier 1 platform_dependency_request_seconds.
	// Authoritative alerting/SLO source until registry ratification (registry.go).
	DependencyCallDurationSeconds = must(gincommon.NewHistogramVec(nil,
		"bff_signup_dependency_call_duration_seconds",
		"Wall time of synchronous calls to external dependencies (RP-1, I-1, eligibility, PUB-1, Turnstile, Catalog), by dependency and outcome. Authoritative until platform_dependency_request_seconds is ratified (registry.go). LLD §11.2.",
		[]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 3},
		"dependency", "outcome"))

	passwordHashVec = must(gincommon.NewHistogramVec(nil,
		"bff_signup_password_hash_duration_seconds",
		"PBKDF2-SHA512 hashing time per SB-2 request (~100–200 ms CPU; drives HPA sizing and SLO-2 budget). LLD §11.2, §10.6, §13.2.",
		[]float64{0.05, 0.1, 0.15, 0.2, 0.3, 0.5, 1.0}))
}

// prewarm creates zero-value series for bounded label combinations so
// counters appear at 0 on startup rather than only after the first event (OBS-8).
func prewarm() {
	// FormStarted / FormSubmitted
	for _, mode := range []string{"trial", "paid"} {
		FormStarted.WithLabelValues(mode)
		FormSubmitted.WithLabelValues(mode)
	}

	// ProvisioningSucceeded
	for _, mode := range []string{"trial", "paid"} {
		for _, strategy := range []string{"inline", "resume", "slug_change"} {
			ProvisioningSucceeded.WithLabelValues(mode, strategy)
		}
	}

	// ProvisioningFailed
	for _, mode := range []string{"trial", "paid"} {
		for _, stage := range []string{"eligibility", "realm_provisioner", "org_membership"} {
			for _, reason := range []string{"unavailable", "contract_error", "exhausted"} {
				ProvisioningFailed.WithLabelValues(mode, stage, reason)
			}
		}
	}

	// RateLimited
	for _, scope := range []string{"ip", "email", "session", "poll"} {
		RateLimited.WithLabelValues(scope)
	}

	// CaptchaChallenged
	for _, result := range []string{"pass", "fail", "error"} {
		CaptchaChallenged.WithLabelValues(result)
	}

	// HandoffResumed
	for _, trigger := range []string{"poll", "resubmit", "slug_change"} {
		HandoffResumed.WithLabelValues(trigger)
	}

	// DependencyCallDurationSeconds
	for _, dep := range []string{"realm_provisioner", "org_membership", "turnstile", "catalog"} {
		for _, outcome := range []string{"success", "error"} {
			DependencyCallDurationSeconds.WithLabelValues(dep, outcome)
		}
	}
}
