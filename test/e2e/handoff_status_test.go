//go:build e2e

package e2e_test

// Stage 2, batch 3: SB-2 downstream failures and resubmits (SB2-43..47, 49..52b,
// 54, 55, 70) and SB-3 status (SB3-01..04, 06..12, 15).
//
// How call counts are checked: a pinned mock answer has a "ttl" (number of calls
// it will answer). The ttl that is left after the request shows how many calls
// the service really made.
//
// Not automated here (stay manual): SB2-41/SB3-14-style Valkey-down cases (SB3-14
// is in Stage 1), SB2-42 (Turnstile), SB2-52a/53 (identity_created cannot be
// reached through the mocks), SB3-05 (needs 15 minutes), SB3-13 (not reachable,
// OBS-33).

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jsonField reads a top-level string field from a JSON reply body.
func jsonField(t *testing.T, r reply, key string) string {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(r.body, &m), r.text())
	v, _ := m[key].(string)
	return v
}

// completedSignup runs a normal signup and returns its session and payload.
func completedSignup(t *testing.T) (*session, map[string]any) {
	t.Helper()
	s := newSession(t, randomIP(), nil)
	p := s.trialPayload(nil)
	r := s.postTrial(t, p)
	require.Equal(t, http.StatusCreated, r.status, r.text())
	return s, p
}

// ── SB-2: happy path and downstream failures ────────────────────────────────

func TestSB2_70_FullHappyPath(t *testing.T) {
	s := newSession(t, randomIP(), nil)

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusCreated, r.status, r.text())
	assert.Equal(t, "completed", jsonField(t, r, "status"))
	assert.Contains(t, r.text(), "check_email")
}

func TestSB2_43_EligibilityDownIs503AndRetriedOnce(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, omURL, "eligibility", "unavailable", 5)

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusServiceUnavailable, r.status, r.text())
	assert.Equal(t, 3, pinTTL(t, omURL, "eligibility"), "1 call + 1 retry = 2 calls used")
}

func TestSB2_44_RP1DownIs503AndRetriedOnce(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, rpURL, "rp1", "unavailable", 5)

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusServiceUnavailable, r.status, r.text())
	assert.Equal(t, 3, pinTTL(t, rpURL, "rp1"), "1 call + 1 retry = 2 calls used")
}

func TestSB2_45_RP1UnexpectedClientErrorIs500(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, rpURL, "rp1", "unexpected_4xx", 1)

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusInternalServerError, r.status, r.text())
}

func TestSB2_46_I1FailsOnceThenSucceedsIs201(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, omURL, "i1", "unavailable", 1)

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusCreated, r.status, r.text())
	assert.Equal(t, 0, pinTTL(t, omURL, "i1"), "the pin was used")
}

func TestSB2_47_I1KeepsFailingIs202AfterThreeCalls(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, omURL, "i1", "unavailable", 10)

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusAccepted, r.status, r.text())
	assert.Equal(t, "provisioning", jsonField(t, r, "status"))
	assert.Equal(t, 7, pinTTL(t, omURL, "i1"), "1 call + 2 inline retries = 3 calls used")
}

func TestSB2_49_I1SlugTakenIs409ThenStatusAsksForNewSlug(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, omURL, "i1", "slug_already_taken", 1)

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusConflict, r.status, r.text())
	assert.Equal(t, 0, pinTTL(t, omURL, "i1"), "exactly one I-1 call, no retry")

	// SB3-04: the status now asks the user to choose another slug.
	st := s.getStatus(t)
	assert.Equal(t, http.StatusOK, st.status, st.text())
	assert.Equal(t, "action_required", jsonField(t, st, "status"))
	assert.Equal(t, "choose_slug", jsonField(t, st, "action"))
}

// ── SB-2: resubmit rules ────────────────────────────────────────────────────

// SB2-50: RP-1 failed (state allocated); the same form sent again succeeds.
func TestSB2_50_ResubmitSameDetailsWhileAllocatedSucceeds(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	p := s.trialPayload(nil)
	pin(t, rpURL, "rp1", "unavailable", 10)
	first := s.postTrial(t, p)
	require.Equal(t, http.StatusServiceUnavailable, first.status, first.text())
	clearPins(t, rpURL) // RP-1 is healthy again

	r := s.postTrial(t, p)

	assert.Equal(t, http.StatusCreated, r.status, r.text())
}

// SB2-51: RP-1 failed (state allocated); a changed form is accepted as a new attempt.
func TestSB2_51_ResubmitDifferentDetailsWhileAllocatedSucceeds(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, rpURL, "rp1", "unavailable", 10)
	first := s.postTrial(t, s.trialPayload(nil))
	require.Equal(t, http.StatusServiceUnavailable, first.status, first.text())
	clearPins(t, rpURL) // RP-1 is healthy again

	r := s.postTrial(t, s.trialPayload(nil)) // new company, slug and e-mail

	assert.Equal(t, http.StatusCreated, r.status, r.text())
}

// SB2-52b: state tenant_pending; the same form again returns 202 without calling I-1.
func TestSB2_52b_ResubmitSameDetailsWhileTenantPendingIs202(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	p := s.trialPayload(nil)
	pin(t, omURL, "i1", "unavailable", 10)
	first := s.postTrial(t, p)
	require.Equal(t, http.StatusAccepted, first.status, first.text())
	left := pinTTL(t, omURL, "i1")

	r := s.postTrial(t, p)

	assert.Equal(t, http.StatusAccepted, r.status, r.text())
	assert.Equal(t, left, pinTTL(t, omURL, "i1"), "no I-1 call on resume")
}

// SB2-54: completed; the same form again returns the original 201.
func TestSB2_54_ResubmitSameDetailsWhenCompletedReturnsOriginal(t *testing.T) {
	s, p := completedSignup(t)

	r := s.postTrial(t, p)

	assert.Equal(t, http.StatusCreated, r.status, r.text())
}

// SB2-55: completed; a changed form returns 409 invalid_state.
func TestSB2_55_ResubmitDifferentDetailsWhenCompletedIs409(t *testing.T) {
	s, _ := completedSignup(t)

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusConflict, r.status, r.text())
	assert.Contains(t, r.text(), "INVALID_STATE")
}

// ── SB-3: status ────────────────────────────────────────────────────────────

func TestSB3_01_StatusCompleted(t *testing.T) {
	s, _ := completedSignup(t)

	r := s.getStatus(t)

	assert.Equal(t, http.StatusOK, r.status, r.text())
	assert.Equal(t, "completed", jsonField(t, r, "status"))
}

// SB3-02, SB3-06, SB3-15: tenant_pending and I-1 still failing → provisioning.
func TestSB3_02_06_15_StatusProvisioningWhileI1StillDown(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, omURL, "i1", "unavailable", 10)
	first := s.postTrial(t, s.trialPayload(nil))
	require.Equal(t, http.StatusAccepted, first.status, first.text())

	r := s.getStatus(t)

	assert.Equal(t, http.StatusOK, r.status, r.text())
	assert.Equal(t, "provisioning", jsonField(t, r, "status"))
	assert.Equal(t, "workspace_setting_up", jsonField(t, r, "message"))
	assert.Contains(t, r.text(), "poll_after_ms")
}

// SB3-03: tenant_pending; the poll retries I-1, which now works → completed.
func TestSB3_03_StatusPollResumesI1AndCompletes(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, omURL, "i1", "unavailable", 3)
	first := s.postTrial(t, s.trialPayload(nil))
	require.Equal(t, http.StatusAccepted, first.status, first.text())

	r := s.getStatus(t)

	assert.Equal(t, http.StatusOK, r.status, r.text())
	assert.Equal(t, "completed", jsonField(t, r, "status"))
}

// SB3-07: another session cannot read this signup (same neutral 404).
func TestSB3_07_OtherSessionsSignupIs404(t *testing.T) {
	owner, _ := completedSignup(t)
	other := newSession(t, randomIP(), nil)

	r := other.do(t, http.MethodGet, "/api/v1/signup/"+owner.signupID+"/status", "", nil)

	assert.Equal(t, http.StatusNotFound, r.status, r.text())
}

func TestSB3_08_UnknownSignupIs404(t *testing.T) {
	s := newSession(t, randomIP(), nil)

	r := s.do(t, http.MethodGet, "/api/v1/signup/00000000-0000-4000-8000-000000000000/status", "", nil)

	assert.Equal(t, http.StatusNotFound, r.status, r.text())
}

// SB3-09: the 61st poll within a minute on one session is refused.
func TestSB3_09_SixtyFirstPollIs429(t *testing.T) {
	s, _ := completedSignup(t)
	for i := 1; i <= 60; i++ {
		r := s.getStatus(t)
		require.Equal(t, http.StatusOK, r.status, "poll %d: %s", i, r.text())
	}

	r := s.getStatus(t)

	assert.Equal(t, http.StatusTooManyRequests, r.status, r.text())
}

func TestSB3_10_StatusWithoutCookieIs401(t *testing.T) {
	s := newSession(t, randomIP(), nil)

	r, err := rawDo(t.Context(), http.MethodGet, baseURL+"/api/v1/signup/"+s.signupID+"/status", "", nil)

	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, r.status, r.text())
	assert.Contains(t, r.text(), "SESSION_EXPIRED")
}

// SB3-11: after a refused signup (eligibility 409) the handoff is gone → 404.
func TestSB3_11_StatusAfterRejectedSignupIs404(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, omURL, "eligibility", "trial_already_used", 1)
	first := s.postTrial(t, s.trialPayload(nil))
	require.Equal(t, http.StatusConflict, first.status, first.text())

	r := s.getStatus(t)

	assert.Equal(t, http.StatusNotFound, r.status, r.text())
}

// SB3-12: two polls at the same time on tenant_pending make only ONE I-1 call.
func TestSB3_12_ConcurrentPollsMakeOneI1Call(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, omURL, "i1", "unavailable", 10)
	first := s.postTrial(t, s.trialPayload(nil))
	require.Equal(t, http.StatusAccepted, first.status, first.text())
	left := pinTTL(t, omURL, "i1")

	var wg sync.WaitGroup
	results := make([]reply, 2)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = s.getStatus(t)
		}()
	}
	wg.Wait()

	for _, r := range results {
		assert.Equal(t, http.StatusOK, r.status, r.text())
		assert.True(t, strings.Contains(r.text(), "provisioning"), r.text())
	}
	assert.Equal(t, left-1, pinTTL(t, omURL, "i1"), "exactly one I-1 call")
}
