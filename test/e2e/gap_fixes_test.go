//go:build e2e

package e2e_test

// Stage 1: one end-to-end test per gap that was fixed on test/gap-fix.
//
// Not black-box testable, so covered elsewhere:
//   - SB2-66 (log fields)       unit: internal/adapter/inbound/http/log_fields_test.go; live: read the service log
//   - S-05   (zero the password) unit: test/unit/adapter/hasher_test.go (memory cannot be inspected over HTTP)
//   - O-03   (span attributes)  unit: test/unit/service/span_attributes_test.go (needs a trace collector live)

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SB2-07: an unknown JSON field is rejected with 400.
func TestSB2_07_UnknownFieldRejected(t *testing.T) {
	s := newSession(t, randomIP(), nil)

	r := s.postTrial(t, s.trialPayload(map[string]any{"extra_field": "x"}))

	assert.Equal(t, http.StatusBadRequest, r.status, r.text())
	assert.Contains(t, strings.ToLower(r.text()), "validation")
}

// SB2-H10: a body of exactly 16,384 bytes is processed normally.
func TestSB2_H10_BodyAtExactly16384Bytes(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	payload := s.trialPayload(map[string]any{"company_name": "Acme Bidding Co"})
	pad := 16384 - len(mustJSON(t, payload))
	require.Positive(t, pad)
	payload["company_name"] = "Acme Bidding Co" + strings.Repeat(" ", pad)
	body := mustJSON(t, payload)
	require.Len(t, body, 16384)

	r := s.do(t, http.MethodPost, trialPath, "application/json", body)

	assert.Equal(t, http.StatusCreated, r.status, r.text())
}

// SB2-H11: no Content-Type on a POST is rejected with 400.
func TestSB2_H11_MissingContentTypeRejected(t *testing.T) {
	s := newSession(t, randomIP(), nil)

	r := s.do(t, http.MethodPost, trialPath, "", mustJSON(t, s.trialPayload(nil)))

	assert.Equal(t, http.StatusBadRequest, r.status, r.text())
}

// SB2-H12: Content-Type text/plain is rejected with 400.
func TestSB2_H12_TextPlainContentTypeRejected(t *testing.T) {
	s := newSession(t, randomIP(), nil)

	r := s.do(t, http.MethodPost, trialPath, "text/plain", mustJSON(t, s.trialPayload(nil)))

	assert.Equal(t, http.StatusBadRequest, r.status, r.text())
}

// SB2-48: an unexpected 4xx from I-1 returns 500 and I-1 is called only once (no retry).
func TestSB2_48_I1ContractErrorFailsFast(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, omURL, "i1", "unexpected_4xx", 5)

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusInternalServerError, r.status, r.text())
	assert.Equal(t, 4, pinTTL(t, omURL, "i1"), "I-1 must be called exactly once (5 -> 4 left)")
}

// SB2-56 and I-03: two SB-2 requests at the same time on one session; the
// second must get 409 invalid_state with details.retry_after_ms.
func TestSB2_56_ConcurrentDoubleSubmitGets409(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	payload := s.trialPayload(nil)
	pin(t, rpURL, "rp1", "slow", 1)

	var wg sync.WaitGroup
	results := make([]reply, 2)
	wg.Add(2)
	go func() { defer wg.Done(); results[0] = s.postTrial(t, payload) }()
	time.Sleep(300 * time.Millisecond)
	go func() { defer wg.Done(); results[1] = s.postTrial(t, payload) }()
	wg.Wait()

	assert.Equal(t, http.StatusConflict, results[1].status, results[1].text())
	assert.Contains(t, results[1].text(), "retry_after_ms")
	assert.Contains(t, strings.ToLower(results[1].text()), "invalid_state")
}

// SB2-I01: repeating SB-2 with the same e-mail after the identity exists only
// resumes: it never hits the rate limit (3 per IP per hour) and never returns 429.
func TestSB2_I01_ResumeDoesNotUseRateLimitBudget(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	payload := s.trialPayload(nil)
	pin(t, omURL, "i1", "slow", 5) // I-1 never finishes, so the identity exists but the tenant does not

	first := s.postTrial(t, payload)
	require.NotEqual(t, http.StatusCreated, first.status, "setup: I-1 must not complete; got %s", first.text())
	clearPins(t, omURL)

	for i := 1; i <= 5; i++ {
		r := s.postTrial(t, payload)
		assert.Equal(t, http.StatusAccepted, r.status, "resume #%d: %s", i, r.text())
	}
}

// SB4-12: when I-1 is still slow during a slug change, SB-4 returns 202 and
// the signup stays pollable as "provisioning" (state tenant_pending).
func TestSB4_12_SlugChangeWithSlowI1Returns202(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, omURL, "i1", "slug_already_taken", 1)
	first := s.postTrial(t, s.trialPayload(nil))
	require.Equal(t, http.StatusConflict, first.status, "setup: expected slug_taken; got %s", first.text())
	pin(t, omURL, "i1", "slow", 5)

	r := s.do(t, http.MethodPost, "/api/v1/signup/"+s.signupID+"/slug", "application/json",
		mustJSON(t, map[string]any{"slug": "e2e-" + randomTag()}))

	assert.Equal(t, http.StatusAccepted, r.status, r.text())
	st := s.getStatus(t)
	assert.Equal(t, http.StatusOK, st.status, st.text())
	assert.Contains(t, st.text(), "provisioning")
}

// S-04: without the Envoy header, a forged X-Forwarded-For must not choose the
// rate-limit key. Four signups with four different fake XFF values still share
// one key (the real connection address), so the 4th hits the 3-per-hour limit.
// Note: all of these use the real client address, so re-running this test many
// times within one hour also uses up that address's session limit (20 per hour).
func TestS04_ForgedForwardedForDoesNotChangeRateLimitKey(t *testing.T) {
	got429 := false
	for i := 0; i < 4; i++ {
		s := newSession(t, "", map[string]string{"X-Forwarded-For": randomIP()})
		if s.postTrial(t, s.trialPayload(nil)).status == http.StatusTooManyRequests {
			got429 = true
		}
	}

	assert.True(t, got429, "a forged X-Forwarded-For must not give each request its own rate-limit key")
}

// I-02: an SB-3 poll re-issues the session cookie with the full 30-minute Max-Age.
func TestI02_StatusPollSlidesSessionCookie(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	require.Equal(t, http.StatusCreated, s.postTrial(t, s.trialPayload(nil)).status)

	r := s.getStatus(t)

	require.Equal(t, http.StatusOK, r.status, r.text())
	c := r.cookie(cookieSID)
	require.NotNil(t, c, "SB-3 must re-issue the %s cookie", cookieSID)
	assert.Equal(t, 1800, c.MaxAge)
}

// SB3-14: with Valkey stopped, SB-3 returns 503 (not 401). Needs Docker, so it
// only runs when E2E_VALKEY_CONTAINER names the Valkey container.
func TestSB3_14_ValkeyDownReturns503(t *testing.T) {
	container := os.Getenv("E2E_VALKEY_CONTAINER")
	if container == "" {
		t.Skip("set E2E_VALKEY_CONTAINER=<valkey container name> to run this test")
	}
	s := newSession(t, randomIP(), nil)
	require.Equal(t, http.StatusCreated, s.postTrial(t, s.trialPayload(nil)).status)

	require.NoError(t, exec.CommandContext(t.Context(), "docker", "stop", container).Run())
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "docker", "start", container).Run()
		time.Sleep(2 * time.Second)
	})

	r := s.getStatus(t)

	assert.Equal(t, http.StatusServiceUnavailable, r.status, r.text())
}
