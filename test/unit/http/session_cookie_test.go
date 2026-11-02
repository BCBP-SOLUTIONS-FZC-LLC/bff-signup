package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/test/fixtures"
)

// sessionCookie returns the sbff_sid Set-Cookie from the response, or nil.
func sessionCookie(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == "sbff_sid" {
			return c
		}
	}
	return nil
}

// I-02 / OBS-50: an SB-3 poll re-issues the session cookie with the full 30-min Max-Age.
func TestHandlerGetStatus_SlidesSessionCookie(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	seedCompletedHandoff(t, handoffs)
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), handoffs,
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/01JAB0000000000000000001/status", nil)
	newRouter(h).ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	c := sessionCookie(w)
	require.NotNil(t, c, "SB-3 must re-issue the sbff_sid cookie")
	assert.Equal(t, 1800, c.MaxAge)
	assert.Equal(t, "test-sid-001", c.Value)
}

// I-02 / OBS-50: an SB-2 submit re-issues the session cookie with the full 30-min Max-Age.
func TestHandlerSubmitTrial_SlidesSessionCookie(t *testing.T) {
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), fixtures.NewFakeHandoffStore(),
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc-001"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/trial", trialBody(t, nil))
	req.Header.Set("Content-Type", "application/json")
	newRouter(h).ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	c := sessionCookie(w)
	require.NotNil(t, c, "SB-2 must re-issue the sbff_sid cookie")
	assert.Equal(t, 1800, c.MaxAge)
}
