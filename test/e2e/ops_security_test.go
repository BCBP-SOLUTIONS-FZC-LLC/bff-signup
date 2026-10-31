//go:build e2e

package e2e_test

// Stage 2, batch 5: health, request id, metrics, no-PII in metrics and logs, CORS,
// ignored inbound identity headers.
//
// Needs two optional settings (tests are skipped when missing):
//
//	E2E_METRICS_URL  default http://127.0.0.1:9090
//	E2E_LOG_FILE     the service log, e.g. ~/bff-run.log (service started with
//	                 `make run 2>&1 | tee ~/bff-run.log`)
//
// Not automated here (stay manual): H-03/H-04 (Valkey stopped), M-07/M-08
// (Turnstile), O-03 and S-05 (unit tests), O-RED05 (reads Valkey keys),
// S-03 (arch-lint), X-01/X-02 (other services).

import (
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var metricsURL = env("E2E_METRICS_URL", "http://127.0.0.1:9090")

func getMetrics(t *testing.T) string {
	t.Helper()
	r, err := rawDo(t.Context(), http.MethodGet, metricsURL+"/metrics", "", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, r.status)
	return r.text()
}

// metricSum adds up every sample of metric `name` whose line contains `labels`.
func metricSum(text, name, labels string) float64 {
	var sum float64
	for _, ln := range strings.Split(text, "\n") {
		if !strings.HasPrefix(ln, name) || strings.HasPrefix(ln, "#") {
			continue
		}
		rest := ln[len(name):]
		if rest == "" || (rest[0] != '{' && rest[0] != ' ') {
			continue // a longer metric name that only starts the same
		}
		if labels != "" && !strings.Contains(ln, labels) {
			continue
		}
		f := strings.Fields(ln)
		v, err := strconv.ParseFloat(f[len(f)-1], 64)
		if err == nil {
			sum += v
		}
	}
	return sum
}

// ── health ──────────────────────────────────────────────────────────────────

func TestH01_HealthzIs200(t *testing.T) {
	r, err := rawDo(t.Context(), http.MethodGet, baseURL+"/healthz", "", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, r.status)
}

func TestH02_ReadyzIs200WhenValkeyIsUp(t *testing.T) {
	r, err := rawDo(t.Context(), http.MethodGet, baseURL+"/readyz", "", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, r.status, r.text())
}

// ── request id on every response ────────────────────────────────────────────

func TestI01_RequestIDOnSuccessAndErrorResponses(t *testing.T) {
	check := func(name string, req *http.Request) {
		resp, err := httpc.Do(req)
		require.NoError(t, err, name)
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		assert.NotEmpty(t, resp.Header.Get("X-Request-Id"), "%s (status %d)", name, resp.StatusCode)
	}
	mk := func(method, path string, hdr map[string]string) *http.Request {
		req, err := http.NewRequestWithContext(t.Context(), method, baseURL+path, nil)
		require.NoError(t, err)
		req.Header.Set("x-envoy-external-address", randomIP())
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		return req
	}
	check("200 session", mk(http.MethodGet, sessionPath, nil))
	check("401 trial without cookie", mk(http.MethodPost, trialPath, map[string]string{"Origin": origin, "Content-Type": "application/json"}))
	check("404 unknown path", mk(http.MethodGet, "/api/v1/signup/nope/nope/nope", nil))
	check("404 paid disabled", mk(http.MethodPost, "/api/v1/signup/paid", map[string]string{"Origin": origin}))
}

// ── metrics ─────────────────────────────────────────────────────────────────

func TestM01_MetricsOnPort9090(t *testing.T) {
	assert.Contains(t, getMetrics(t), "bff_signup_")
}

func TestM02_MetricsNotOnMainPort(t *testing.T) {
	r, err := rawDo(t.Context(), http.MethodGet, baseURL+"/metrics", "", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, r.status)
}

func TestM03_AllBusinessMetricsRegistered(t *testing.T) {
	text := getMetrics(t)
	for _, name := range []string{
		"bff_signup_form_started_total", "bff_signup_form_submitted_total",
		"bff_signup_provisioning_succeeded_total", "bff_signup_provisioning_failed_total",
		"bff_signup_rate_limited_total", "bff_signup_captcha_challenged_total",
		"bff_signup_handoff_resumed_total", "bff_signup_dependency_call_duration_seconds",
		"bff_signup_password_hash_duration_seconds",
	} {
		assert.Contains(t, text, "# HELP "+name, name)
	}
}

func TestM05_FormStartedIncreasesOnSB1(t *testing.T) {
	before := metricSum(getMetrics(t), "bff_signup_form_started_total", `mode="trial"`)

	newSession(t, randomIP(), nil)

	after := metricSum(getMetrics(t), "bff_signup_form_started_total", `mode="trial"`)
	assert.Equal(t, before+1, after)
}

func TestM06_RateLimitedSessionIncreasesOn21stSession(t *testing.T) {
	ip := randomIP()
	for i := 0; i < 20; i++ {
		require.Equal(t, http.StatusOK, getSession(t, ip).status)
	}
	before := metricSum(getMetrics(t), "bff_signup_rate_limited_total", `scope="session"`)

	require.Equal(t, http.StatusTooManyRequests, getSession(t, ip).status)

	after := metricSum(getMetrics(t), "bff_signup_rate_limited_total", `scope="session"`)
	assert.Equal(t, before+1, after)
}

func TestM09_PasswordHashDurationObservedAfterSB2(t *testing.T) {
	before := metricSum(getMetrics(t), "bff_signup_password_hash_duration_seconds_count", "")
	s := newSession(t, randomIP(), nil)
	require.Equal(t, http.StatusCreated, s.postTrial(t, s.trialPayload(nil)).status)

	after := metricSum(getMetrics(t), "bff_signup_password_hash_duration_seconds_count", "")

	assert.Greater(t, after, before)
}

// M-04 / M-10: after a signup, /metrics holds no e-mail, IP, tenant or user id.
func TestM04_M10_MetricsCarryNoPII(t *testing.T) {
	ip := randomIP()
	s := newSession(t, ip, nil)
	p := s.trialPayload(nil)
	r := s.postTrial(t, p)
	require.Equal(t, http.StatusCreated, r.status, r.text())

	text := getMetrics(t)

	assert.NotContains(t, text, p["admin_email"].(string))
	assert.NotContains(t, text, ip)
	assert.NotContains(t, text, "@example.test")
	for _, label := range []string{"tenant_id=", "user_id=", "email=", "ip=", "signup_id="} {
		assert.NotContains(t, text, label)
	}
}

// ── security headers / identity headers ─────────────────────────────────────

func TestS01_NoCORSHeaders(t *testing.T) {
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+sessionPath, nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("x-envoy-external-address", randomIP())
	resp, err := httpc.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	for k := range resp.Header {
		assert.False(t, strings.HasPrefix(strings.ToLower(k), "access-control-"), "unexpected header %s", k)
	}
}

// S-02: x-user-id / x-tenant-id sent by the browser are ignored.
func TestS02_InboundIdentityHeadersAreIgnored(t *testing.T) {
	evilTenant := "11111111-1111-4111-8111-111111111111"
	s := newSession(t, randomIP(), map[string]string{
		"x-user-id":   "22222222-2222-4222-8222-222222222222",
		"x-tenant-id": evilTenant,
	})

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusCreated, r.status, r.text())
	assert.NotContains(t, r.text(), evilTenant)
}

// ── logs (need E2E_LOG_FILE) ────────────────────────────────────────────────

func logSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	require.NoError(t, err)
	return fi.Size()
}

func logSince(t *testing.T, path string, offset int64) string {
	t.Helper()
	time.Sleep(500 * time.Millisecond) // let the service flush its log lines
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	_, err = f.Seek(offset, io.SeekStart)
	require.NoError(t, err)
	b, err := io.ReadAll(f)
	require.NoError(t, err)
	return string(b)
}

// O-RED01..04, O-01, O-02: one signup, then read only the log lines it wrote.
func TestO_LogsHaveIDsAndNoSecrets(t *testing.T) {
	path := os.Getenv("E2E_LOG_FILE")
	if path == "" {
		t.Skip("set E2E_LOG_FILE to the service log file to run the log checks")
	}
	offset := logSize(t, path)
	s := newSession(t, randomIP(), nil)
	tag := randomTag()
	p := s.trialPayload(map[string]any{
		"password":      "LogCheck#Pass" + tag,
		"admin_name":    "Logcheck Person " + tag,
		"captcha_token": "captcha-" + tag,
	})
	r := s.postTrial(t, p)
	require.Equal(t, http.StatusCreated, r.status, r.text())

	log := logSince(t, path, offset)

	// O-RED01..04 / O-02: no secrets and no full e-mail in the log.
	for name, secret := range map[string]string{
		"plain password":  p["password"].(string),
		"admin name":      p["admin_name"].(string),
		"captcha token":   p["captcha_token"].(string),
		"CSRF token":      s.csrf,
		"session cookie":  s.sid,
		"full e-mail":     p["admin_email"].(string),
		"hashed seed key": "hashed_password_seed",
	} {
		assert.NotContains(t, log, secret, name)
	}

	// O-01: the signup log lines carry request_id and trace_id.
	found := false
	for _, ln := range strings.Split(log, "\n") {
		if strings.Contains(ln, s.signupID) {
			found = true
			assert.Contains(t, ln, "request_id", ln)
			assert.Contains(t, ln, "trace_id", ln)
		}
	}
	assert.True(t, found, "no log line mentions signup_id %s", s.signupID)
}
