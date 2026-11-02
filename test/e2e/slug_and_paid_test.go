//go:build e2e

package e2e_test

// Stage 2, batch 4: SB-4 (change slug) and SB-5 (paid, switched off).
//
// Not automated here (stay manual): SB4-08 and SB4-13 (need the mock request
// log to compare tenant_id and owner_user_id), SB5-02 (needs the service
// restarted with PAID_SIGNUP_ENABLED=true; now in captcha_and_paid_test.go). SB4-12 is in Stage 1.

import (
	"bytes"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (s *session) slugPath() string { return "/api/v1/signup/" + s.signupID + "/slug" }

func (s *session) postSlug(t *testing.T, slug string) reply {
	t.Helper()
	return s.do(t, http.MethodPost, s.slugPath(), "application/json", mustJSON(t, map[string]any{"slug": slug}))
}

func freeSlug() string { return "e2e-" + randomTag() }

// slugConflictSession returns a session whose signup is in state slug_conflict
// (I-1 said "slug already taken" once). Extra I-1 pin calls left = pinCalls-1.
func slugConflictSession(t *testing.T, pinCalls int) *session {
	t.Helper()
	s := newSession(t, randomIP(), nil)
	pin(t, omURL, "i1", "slug_already_taken", pinCalls)
	first := s.postTrial(t, s.trialPayload(nil))
	require.Equal(t, http.StatusConflict, first.status, "setup: %s", first.text())
	return s
}

func TestSB4_01_FreeSlugCompletesSignup(t *testing.T) {
	s := slugConflictSession(t, 1)

	r := s.postSlug(t, freeSlug())

	assert.Equal(t, http.StatusCreated, r.status, r.text())
	assert.Equal(t, "completed", jsonField(t, r, "status"))
}

func TestSB4_02_SlugTakenAgainIs409WithSuggestions(t *testing.T) {
	s := slugConflictSession(t, 2)

	r := s.postSlug(t, freeSlug())

	assert.Equal(t, http.StatusConflict, r.status, r.text())
	assert.Equal(t, "SLUG_TAKEN", r.errorCode())
	assert.Contains(t, r.text(), "suggestions")
}

func TestSB4_03_NotInSlugConflictIs409InvalidState(t *testing.T) {
	s, _ := completedSignup(t)

	r := s.postSlug(t, freeSlug())

	assert.Equal(t, http.StatusConflict, r.status, r.text())
	assert.Contains(t, r.text(), "INVALID_STATE")
}

func TestSB4_04_BadSlugFormatIs400(t *testing.T) {
	s := slugConflictSession(t, 1)

	r := s.postSlug(t, "Bad_Slug!")

	assert.Equal(t, http.StatusBadRequest, r.status, r.text())
	assert.Equal(t, "VALIDATION_FAILED", r.errorCode())
}

// SB4-05: the 6th slug attempt is refused without calling I-1.
func TestSB4_05_SixthSlugAttemptIsRefused(t *testing.T) {
	s := slugConflictSession(t, 10) // SB-2 used 1 call; 9 left
	for i := 1; i <= 5; i++ {
		r := s.postSlug(t, freeSlug())
		require.Equal(t, http.StatusConflict, r.status, "attempt %d: %s", i, r.text())
	}
	left := pinTTL(t, omURL, "i1")

	r := s.postSlug(t, freeSlug())

	assert.Equal(t, http.StatusConflict, r.status, r.text())
	assert.Contains(t, r.text(), "INVALID_STATE")
	assert.Equal(t, left, pinTTL(t, omURL, "i1"), "no I-1 call on the 6th attempt")
}

func TestSB4_06_MissingCSRFIs403(t *testing.T) {
	s := slugConflictSession(t, 1)

	r := s.postSlugRaw(t, true, false, mustJSON(t, map[string]any{"slug": freeSlug()}))

	assert.Equal(t, http.StatusForbidden, r.status, r.text())
	assert.Contains(t, r.text(), "CSRF_INVALID")
}

func TestSB4_07_MissingCookieIs401(t *testing.T) {
	s := slugConflictSession(t, 1)

	r := s.postSlugRaw(t, false, true, mustJSON(t, map[string]any{"slug": freeSlug()}))

	assert.Equal(t, http.StatusUnauthorized, r.status, r.text())
	assert.Contains(t, r.text(), "SESSION_EXPIRED")
}

func TestSB4_09_ReservedSlugIs400(t *testing.T) {
	s := slugConflictSession(t, 1)

	r := s.postSlug(t, "api")

	assert.Equal(t, http.StatusBadRequest, r.status, r.text())
	assert.Equal(t, "VALIDATION_FAILED", r.errorCode())
}

// SB4-10: state allocated (RP-1 failed, nothing created yet) → not awaiting a slug.
func TestSB4_10_StateAllocatedIs409InvalidState(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, rpURL, "rp1", "unavailable", 10)
	first := s.postTrial(t, s.trialPayload(nil))
	require.Equal(t, http.StatusServiceUnavailable, first.status, first.text())
	clearPins(t, rpURL)

	r := s.postSlug(t, freeSlug())

	assert.Equal(t, http.StatusConflict, r.status, r.text())
	assert.Contains(t, r.text(), "INVALID_STATE")
}

// SB4-11: the 5th (last allowed) attempt with a free slug succeeds.
func TestSB4_11_FifthAttemptWithFreeSlugSucceeds(t *testing.T) {
	s := slugConflictSession(t, 5) // SB-2 + attempts 1-4 taken; attempt 5 free
	for i := 1; i <= 4; i++ {
		r := s.postSlug(t, freeSlug())
		require.Equal(t, http.StatusConflict, r.status, "attempt %d: %s", i, r.text())
	}

	r := s.postSlug(t, freeSlug())

	assert.Equal(t, http.StatusCreated, r.status, r.text())
}

// postSlugRaw posts to SB-4 and lets the test leave out the cookie or the CSRF header.
func (s *session) postSlugRaw(t *testing.T, withCookie, withCSRF bool, body []byte) reply {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+s.slugPath(), bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	if s.ip != "" {
		req.Header.Set("x-envoy-external-address", s.ip)
	}
	if withCookie {
		req.Header.Set("Cookie", cookieSID+"="+s.sid)
	}
	if withCSRF {
		req.Header.Set("X-CSRF-Token", s.csrf)
	}
	r, err := send(req)
	require.NoError(t, err)
	return r
}

// ── SB-5 ────────────────────────────────────────────────────────────────────

// SB5-01: PAID_SIGNUP_ENABLED=false (default) → the route answers 404.
func TestSB5_01_PaidRouteIs404WhenDisabled(t *testing.T) {
	if os.Getenv("E2E_PAID_ENABLED") == "true" {
		t.Skip("the service runs with PAID_SIGNUP_ENABLED=true; see SB5-02")
	}
	s := newSession(t, randomIP(), nil)
	p := s.trialPayload(map[string]any{"seats": 5, "payment_token": "tok_e2e"})

	r := s.do(t, http.MethodPost, "/api/v1/signup/paid", "application/json", mustJSON(t, p))

	assert.Equal(t, http.StatusNotFound, r.status, r.text())
	assert.True(t, strings.Contains(strings.ToLower(r.text()), "not enabled") ||
		strings.Contains(strings.ToLower(r.text()), "not found"), r.text())
}
