//go:build e2e

package e2e_test

// Stage 2, batch 1: SB-1 (session) and the first SB-2 checks (session, CSRF,
// Origin, body size, JSON, company_name). One test per Excel case, named with
// the case ID.
//
// Not automated here (stay manual): SB1-03 needs Valkey stopped.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sessionPath = "/api/v1/signup/session"

// getSession calls SB-1 and returns the raw reply (no assertions).
func getSession(t *testing.T, ip string) reply {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+sessionPath, nil)
	require.NoError(t, err)
	if ip != "" {
		req.Header.Set("x-envoy-external-address", ip)
	}
	r, err := send(req)
	require.NoError(t, err)
	return r
}

// sb1Body is the SB-1 response body.
type sb1Body struct {
	SignupID            string `json:"signup_id"`
	CSRFToken           string `json:"csrf_token"`
	CurrentTermsVersion string `json:"current_terms_version"`
	Captcha             struct {
		SiteKey string `json:"site_key"`
	} `json:"captcha"`
	Plans []struct {
		Code      string `json:"code"`
		TrialDays int    `json:"trial_days"`
	} `json:"plans"`
	SlugRules struct {
		Pattern string `json:"pattern"`
	} `json:"slug_rules"`
}

func okSession(t *testing.T) (reply, sb1Body) {
	t.Helper()
	r := getSession(t, randomIP())
	require.Equal(t, http.StatusOK, r.status, r.text())
	var b sb1Body
	require.NoError(t, json.Unmarshal(r.body, &b))
	return r, b
}

// postOpts lets a test break one part of a POST on purpose.
type postOpts struct {
	noCookie   bool
	noCSRF     bool
	csrf       string // overrides the CSRF header when set
	origin     string // overrides the Origin header when set
	noOrigin   bool
	rawBody    []byte
	contentTyp string
}

func (s *session) postWith(t *testing.T, o postOpts) reply {
	t.Helper()
	ct := o.contentTyp
	if ct == "" {
		ct = "application/json"
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+trialPath, bytes.NewReader(o.rawBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", ct)
	if s.ip != "" {
		req.Header.Set("x-envoy-external-address", s.ip)
	}
	if !o.noCookie {
		req.Header.Set("Cookie", cookieSID+"="+s.sid)
	}
	if !o.noCSRF {
		v := s.csrf
		if o.csrf != "" {
			v = o.csrf
		}
		req.Header.Set("X-CSRF-Token", v)
	}
	if !o.noOrigin {
		v := origin
		if o.origin != "" {
			v = o.origin
		}
		req.Header.Set("Origin", v)
	}
	r, err := send(req)
	require.NoError(t, err)
	return r
}

// ── SB-1 ────────────────────────────────────────────────────────────────────

func TestSB1_01_FirstCallReturnsSessionContract(t *testing.T) {
	r, b := okSession(t)

	assert.NotEmpty(t, b.SignupID)
	assert.NotEmpty(t, b.CSRFToken)
	assert.NotEmpty(t, b.Captcha.SiteKey)
	assert.NotEmpty(t, b.Plans)
	assert.NotEmpty(t, b.SlugRules.Pattern)
	require.NotNil(t, r.cookie(cookieSID))
}

func TestSB1_02_TwentyFirstSessionFromSameIPIs429(t *testing.T) {
	ip := randomIP()
	for i := 1; i <= 20; i++ {
		r := getSession(t, ip)
		require.Equal(t, http.StatusOK, r.status, "call %d: %s", i, r.text())
	}

	r := getSession(t, ip)

	assert.Equal(t, http.StatusTooManyRequests, r.status, r.text())
	assert.Contains(t, r.text(), "RATE_LIMITED")
	assert.Positive(t, retryAfterSeconds(t, r), "429 must carry a Retry-After header")
}

func TestSB1_04_ResponseHeaders(t *testing.T) {
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+sessionPath, nil)
	require.NoError(t, err)
	req.Header.Set("x-envoy-external-address", randomIP())
	resp, err := httpc.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))
	assert.NotEmpty(t, resp.Header.Get("Referrer-Policy"))
	assert.NotEmpty(t, resp.Header.Get("X-Request-Id"))
}

func TestSB1_05_CookieFlags(t *testing.T) {
	r, _ := okSession(t)

	c := r.cookie(cookieSID)
	require.NotNil(t, c)
	assert.True(t, c.HttpOnly, "HttpOnly")
	assert.True(t, c.Secure, "Secure")
	assert.Equal(t, http.SameSiteStrictMode, c.SameSite)
	assert.Equal(t, "/api/v1/signup", c.Path)
	assert.Equal(t, 1800, c.MaxAge)
}

func TestSB1_06_PlansMatchCatalog(t *testing.T) {
	_, b := okSession(t)

	got := map[string]int{}
	for _, p := range b.Plans {
		got[p.Code] = p.TrialDays
	}
	assert.Equal(t, map[string]int{"starter": 14, "pro": 14, "enterprise": 14}, got)
}

func TestSB1_07_08_TermsVersionPresent(t *testing.T) {
	_, b := okSession(t)

	assert.NotEmpty(t, strings.TrimSpace(b.CurrentTermsVersion))
}

func TestSB1_09_SlugRulePatternIsExact(t *testing.T) {
	_, b := okSession(t)

	assert.Equal(t, `^[a-z0-9](?:[a-z0-9-]{1,28}[a-z0-9])$`, b.SlugRules.Pattern)
}

func TestSB1_10_OnlyPublicSiteKeyReturned(t *testing.T) {
	r, b := okSession(t)

	assert.NotEmpty(t, b.Captcha.SiteKey)
	assert.NotContains(t, strings.ToLower(r.text()), "secret")
}

func TestSB1_11_PlanCodesNeverTrial(t *testing.T) {
	_, b := okSession(t)

	codes := []string{}
	for _, p := range b.Plans {
		codes = append(codes, p.Code)
	}
	assert.ElementsMatch(t, []string{"starter", "pro", "enterprise"}, codes)
	assert.NotContains(t, codes, "trial")
}

// ── SB-2: session, CSRF, Origin, body, JSON, company_name ───────────────────

func TestSB2_01_NoCookieIs401(t *testing.T) {
	s := newSession(t, randomIP(), nil)

	r := s.postWith(t, postOpts{noCookie: true, rawBody: mustJSON(t, s.trialPayload(nil))})

	assert.Equal(t, http.StatusUnauthorized, r.status, r.text())
	assert.Contains(t, r.text(), "SESSION_EXPIRED")
}

func TestSB2_02_MissingCSRFHeaderIs403(t *testing.T) {
	s := newSession(t, randomIP(), nil)

	r := s.postWith(t, postOpts{noCSRF: true, rawBody: mustJSON(t, s.trialPayload(nil))})

	assert.Equal(t, http.StatusForbidden, r.status, r.text())
	assert.Contains(t, r.text(), "CSRF_INVALID")
}

func TestSB2_03_WrongCSRFTokenIs403(t *testing.T) {
	s := newSession(t, randomIP(), nil)

	r := s.postWith(t, postOpts{csrf: "not-the-token", rawBody: mustJSON(t, s.trialPayload(nil))})

	assert.Equal(t, http.StatusForbidden, r.status, r.text())
	assert.Contains(t, r.text(), "CSRF_INVALID")
}

func TestSB2_04_WrongOriginIs403(t *testing.T) {
	s := newSession(t, randomIP(), nil)

	r := s.postWith(t, postOpts{origin: "https://evil.example", rawBody: mustJSON(t, s.trialPayload(nil))})

	assert.Equal(t, http.StatusForbidden, r.status, r.text())
	assert.Contains(t, r.text(), "CSRF_INVALID")
}

func TestSB2_05_BodyOver16KBIs413(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	payload := s.trialPayload(map[string]any{"company_name": strings.Repeat("A", 20000)})

	r := s.postTrial(t, payload)

	assert.Equal(t, http.StatusRequestEntityTooLarge, r.status, r.text())
	assert.Contains(t, r.text(), "REQUEST_TOO_LARGE")
}

func TestSB2_06_MalformedJSONIs400(t *testing.T) {
	s := newSession(t, randomIP(), nil)

	r := s.postWith(t, postOpts{rawBody: []byte(`{"company_name": "Acme",`)})

	assert.Equal(t, http.StatusBadRequest, r.status, r.text())
	assert.Contains(t, r.text(), "VALIDATION_FAILED")
}

func TestSB2_08_EmptyCompanyNameIs400(t *testing.T) {
	s := newSession(t, randomIP(), nil)

	r := s.postTrial(t, s.trialPayload(map[string]any{"company_name": ""}))

	assert.Equal(t, http.StatusBadRequest, r.status, r.text())
	assert.Contains(t, r.text(), "company_name")
}

func TestSB2_09_OneCharCompanyNameIs400(t *testing.T) {
	s := newSession(t, randomIP(), nil)

	r := s.postTrial(t, s.trialPayload(map[string]any{"company_name": "A"}))

	assert.Equal(t, http.StatusBadRequest, r.status, r.text())
	assert.Contains(t, r.text(), "company_name")
}

// retryAfterSeconds reads the Retry-After header of a reply (0 when missing).
func retryAfterSeconds(t *testing.T, r reply) int {
	t.Helper()
	n, _ := strconv.Atoi(r.header.Get("Retry-After"))
	return n
}
