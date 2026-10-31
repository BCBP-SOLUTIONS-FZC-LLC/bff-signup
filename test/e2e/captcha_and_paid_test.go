//go:build e2e

package e2e_test

// Stage 2, batch 7: CAPTCHA (Turnstile) cases and SB5-02, which need the service
// started in a special mode. Each test runs only in its own mode and is skipped
// otherwise, so the normal run (CAPTCHA_SKIP=true) is not affected.
//
// Cloudflare publishes dummy Turnstile secrets for testing (they accept any
// token, the answer depends on the secret). The service calls the real
// siteverify endpoint, so this needs internet access.
//
//	E2E_CAPTCHA_MODE=pass      secret 1x...AA, expected hostname example.com
//	E2E_CAPTCHA_MODE=fail      secret 2x...AA, expected hostname example.com
//	E2E_CAPTCHA_MODE=mismatch  secret 1x...AA, expected hostname localhost
//	E2E_CAPTCHA_MODE=down      secret 1x...AA, TURNSTILE_TIMEOUT_MS=1
//	E2E_PAID_ENABLED=true      PAID_SIGNUP_ENABLED=true (CAPTCHA_SKIP=true)
//
// All modes need CAPTCHA_SKIP=false except E2E_PAID_ENABLED. See README.md.

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func needCaptchaMode(t *testing.T, mode string) {
	t.Helper()
	if got := os.Getenv("E2E_CAPTCHA_MODE"); got != mode {
		t.Skipf("needs the service started for E2E_CAPTCHA_MODE=%s (this run: %q)", mode, got)
	}
}

func captchaResult(t *testing.T, result string) float64 {
	t.Helper()
	return metricSum(getMetrics(t), "bff_signup_captcha_challenged_total", `result="`+result+`"`)
}

func turnstileCalls(t *testing.T) float64 {
	t.Helper()
	return metricSum(getMetrics(t), "bff_signup_dependency_call_duration_seconds_count", `dependency="turnstile"`)
}

// M-07: a token Turnstile accepts → result="pass" counter +1 and the signup works.
func TestM07_CaptchaPassCounterIncrements(t *testing.T) {
	needCaptchaMode(t, "pass")
	before := captchaResult(t, "pass")
	s := newSession(t, randomIP(), nil)

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusCreated, r.status, r.text())
	assert.Equal(t, before+1, captchaResult(t, "pass"))
}

// SB2-32: Turnstile rejects the token → 403 captcha_failed, result="fail" +1.
func TestSB2_32_CaptchaRejectedIs403(t *testing.T) {
	needCaptchaMode(t, "fail")
	before := captchaResult(t, "fail")
	s := newSession(t, randomIP(), nil)

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusForbidden, r.status, r.text())
	assert.Contains(t, strings.ToUpper(r.text()), "CAPTCHA_FAILED")
	assert.Equal(t, before+1, captchaResult(t, "fail"))
}

// SB2-36 and SB2-RL01 (G-2): failed CAPTCHAs never use up the rate-limit budget.
// The limit is 3 signups per IP per hour and 5 per e-mail per day. If a failed
// CAPTCHA were counted, the 4th attempt from one IP (or the 6th for one e-mail)
// would be 429 instead of 403. This proves the counters do not move; it does not
// read the exact Valkey value.
func TestSB2_36_RL01_FailedCaptchaDoesNotCountTowardLimits(t *testing.T) {
	needCaptchaMode(t, "fail")

	ip := randomIP()
	for i := 1; i <= 4; i++ {
		s := newSession(t, ip, nil)
		r := s.postTrial(t, s.trialPayload(nil))
		require.Equal(t, http.StatusForbidden, r.status, "same IP, attempt %d: %s", i, r.text())
	}

	email := "e2e+" + randomTag() + "@example.test"
	for i := 1; i <= 6; i++ {
		s := newSession(t, randomIP(), nil)
		r := s.postTrial(t, s.trialPayload(map[string]any{"admin_email": email}))
		require.Equal(t, http.StatusForbidden, r.status, "same e-mail, attempt %d: %s", i, r.text())
	}
}

// SB2-33: Turnstile accepts the token but for another hostname → 403.
func TestSB2_33_CaptchaHostnameMismatchIs403(t *testing.T) {
	needCaptchaMode(t, "mismatch")
	before := captchaResult(t, "fail")
	s := newSession(t, randomIP(), nil)

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusForbidden, r.status, r.text())
	assert.Contains(t, strings.ToUpper(r.text()), "CAPTCHA_FAILED")
	assert.Equal(t, before+1, captchaResult(t, "fail"))
}

// SB2-42 and M-08: Turnstile cannot be reached (1 ms timeout) → 503, result="error" +1.
func TestSB2_42_M08_TurnstileDownIs503(t *testing.T) {
	needCaptchaMode(t, "down")
	before := captchaResult(t, "error")
	s := newSession(t, randomIP(), nil)

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusServiceUnavailable, r.status, r.text())
	assert.Greater(t, captchaResult(t, "error"), before)
}

// SB2-42 (LLD 9.3): Turnstile is not retried. Its token is single-use, so a retry after a
// timeout could fail on a spent token and show a false 403 instead of the 503.
func TestSB2_42_TurnstileIsNotRetried(t *testing.T) {
	needCaptchaMode(t, "down")
	before := turnstileCalls(t)
	s := newSession(t, randomIP(), nil)

	r := s.postTrial(t, s.trialPayload(nil))

	require.Equal(t, http.StatusServiceUnavailable, r.status, r.text())
	assert.Equal(t, before+1, turnstileCalls(t), "LLD 9.3: no retry, so exactly one Turnstile call")
}

// SB5-02: with PAID_SIGNUP_ENABLED=true the route is wired (no longer the 404 of SB5-01).
func TestSB5_02_PaidRouteIsWiredWhenEnabled(t *testing.T) {
	if os.Getenv("E2E_PAID_ENABLED") != "true" {
		t.Skip("needs the service started with PAID_SIGNUP_ENABLED=true and E2E_PAID_ENABLED=true")
	}
	s := newSession(t, randomIP(), nil)
	p := s.trialPayload(map[string]any{"seats": 5, "payment_token": "tok_e2e"})

	r := s.do(t, http.MethodPost, "/api/v1/signup/paid", "application/json", mustJSON(t, p))

	assert.NotEqual(t, http.StatusNotFound, r.status, r.text())
	assert.NotContains(t, strings.ToLower(r.text()), "not enabled")
}
