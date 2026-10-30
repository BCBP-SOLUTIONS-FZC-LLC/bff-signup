//go:build e2e

package e2e_test

// Stage 2, batch 6: input boundaries (SB2-V01..V18, H01..H09), RP-1 409 answers
// (RP02/RP03), resubmit with other details (I02/I03), locale (UX01..03), what the
// service sends to the mocks (SB2-57..63, C01..C07) and SB-2 metrics (67..69).
//
// The "what was sent" checks use the mocks' recorded-request list
// (GET/DELETE /__mock/requests). The mock files must be the newer version; if
// the endpoint is missing these tests stop with a clear message.
//
// Covered by an earlier test (no new test): SB2-H05 = TestS04, SB2-H07 = TestSB3_07,
// SB2-RL02/RL03 = TestSB2_34. Stay manual: SB2-RL01 (Turnstile), SB2-RP01 (OBS-41).

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── recorded requests from the mocks ────────────────────────────────────────

type recordedReq struct {
	Label   string            `json:"label"`
	Headers map[string]string `json:"headers"`
	Body    map[string]any    `json:"body"`
}

func clearRecorded(t *testing.T) {
	t.Helper()
	for _, u := range []string{omURL, rpURL} {
		r, err := rawDo(t.Context(), http.MethodDelete, u+"/__mock/requests", "", nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, r.status,
			"mock has no /__mock/requests: copy the new mock files and restart the mocks")
	}
}

func recorded(t *testing.T, mockURL, label string) []recordedReq {
	t.Helper()
	r, err := rawDo(t.Context(), http.MethodGet, mockURL+"/__mock/requests", "", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, r.status, r.text())
	var all []recordedReq
	require.NoError(t, json.Unmarshal(r.body, &all))
	var out []recordedReq
	for _, x := range all {
		if x.Label == label {
			out = append(out, x)
		}
	}
	return out
}

// one returns the single recorded request with that label.
func one(t *testing.T, mockURL, label string) recordedReq {
	t.Helper()
	got := recorded(t, mockURL, label)
	require.Len(t, got, 1, "expected exactly one %s call", label)
	return got[0]
}

// pick returns the first of keys that is present in the body, as a string.
func pick(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			if s, ok := v.(string); ok {
				return s
			}
			b, _ := json.Marshal(v)
			return string(b)
		}
	}
	return ""
}

// signupWithRecording clears the recorded requests, runs one signup and returns its payload.
func signupWithRecording(t *testing.T, ip string, extra map[string]string, overrides map[string]any) (*session, map[string]any, reply) {
	t.Helper()
	clearRecorded(t)
	s := newSession(t, ip, extra)
	p := s.trialPayload(overrides)
	r := s.postTrial(t, p)
	require.Equal(t, http.StatusCreated, r.status, r.text())
	return s, p, r
}

// ── input boundaries ────────────────────────────────────────────────────────

func expectCreated(t *testing.T, overrides map[string]any, drop ...string) reply {
	t.Helper()
	s := newSession(t, randomIP(), nil)
	p := s.trialPayload(overrides)
	for _, k := range drop {
		delete(p, k)
	}
	r := s.postTrial(t, p)
	assert.Equal(t, http.StatusCreated, r.status, r.text())
	return r
}

func expectBad(t *testing.T, overrides map[string]any, want string, drop ...string) {
	t.Helper()
	s := newSession(t, randomIP(), nil)
	p := s.trialPayload(overrides)
	for _, k := range drop {
		delete(p, k)
	}
	r := s.postTrial(t, p)
	assert.Equal(t, http.StatusBadRequest, r.status, r.text())
	assert.Contains(t, strings.ToLower(r.text()), strings.ToLower(want))
}

func TestSB2_V01_CompanyNameOnlySpacesIs400(t *testing.T) {
	expectBad(t, map[string]any{"company_name": "   "}, "company_name")
}
func TestSB2_V02_CompanyName2CharsIs201(t *testing.T) {
	expectCreated(t, map[string]any{"company_name": "Ab"})
}
func TestSB2_V03_CompanyName100CharsIs201(t *testing.T) {
	expectCreated(t, map[string]any{"company_name": strings.Repeat("A", 100)})
}
func TestSB2_V04_AdminNameOnlySpacesIs400(t *testing.T) {
	expectBad(t, map[string]any{"admin_name": "   "}, "admin_name")
}

// V05: the e-mail domain is lower-cased before it is sent on.
func TestSB2_V05_EmailDomainLowercased(t *testing.T) {
	tag := randomTag()
	_, _, _ = signupWithRecording(t, randomIP(), nil, map[string]any{"admin_email": "owner-" + tag + "@EXAMPLE.TEST"})

	rp := one(t, rpURL, "RP-1")

	b, _ := json.Marshal(rp.Body)
	assert.Contains(t, string(b), "owner-"+tag+"@example.test")
	assert.NotContains(t, string(b), "EXAMPLE.TEST")
}

func TestSB2_V06_SlugStartsWithHyphenIs400(t *testing.T) {
	expectBad(t, map[string]any{"slug": "-acme"}, "slug")
}
func TestSB2_V07_SlugEndsWithHyphenIs400(t *testing.T) {
	expectBad(t, map[string]any{"slug": "acme-"}, "slug")
}
func TestSB2_V08_Slug3CharsIs201(t *testing.T) { expectCreated(t, map[string]any{"slug": "abc"}) }
func TestSB2_V09_Slug30CharsIs201(t *testing.T) {
	expectCreated(t, map[string]any{"slug": "a" + strings.Repeat("b", 28) + "c"})
}
func TestSB2_V10_SlugWithUnicodeIs400(t *testing.T) {
	expectBad(t, map[string]any{"slug": "açme"}, "slug")
}
func TestSB2_V11_ReservedSlugUppercaseIs400(t *testing.T) {
	expectBad(t, map[string]any{"slug": "ADMIN"}, "slug")
}
func TestSB2_V12_Password12CharsIs201(t *testing.T) {
	expectCreated(t, map[string]any{"password": "Password1234"})
}
func TestSB2_V13_Password128CharsIs201(t *testing.T) {
	expectCreated(t, map[string]any{"password": strings.Repeat("Aa1", 43)[:128]})
}
func TestSB2_V14_PasswordExactlyThreeClassesIs201(t *testing.T) {
	expectCreated(t, map[string]any{"password": "Password123abc"})
}
func TestSB2_V15_AcceptTermsAbsentIs400(t *testing.T) {
	expectBad(t, nil, "terms", "accept_terms")
}
func TestSB2_V16_CompanyNameAbsentIs400(t *testing.T) {
	expectBad(t, nil, "company_name", "company_name")
}
func TestSB2_V17_PlanNullIs400(t *testing.T) {
	expectBad(t, map[string]any{"plan": nil}, "plan")
}
func TestSB2_V18_SlugAbsentAndCompanyAllSpecialCharsIs201(t *testing.T) {
	expectCreated(t, map[string]any{"company_name": "!!!"}, "slug")
}

// ── hostile input ───────────────────────────────────────────────────────────

func TestSB2_H01_ScriptTagCompanyNameIs201AsPlainText(t *testing.T) {
	expectCreated(t, map[string]any{"company_name": "<script>alert(1)</script>"})
}
func TestSB2_H02_SQLTextCompanyNameIs201AsPlainText(t *testing.T) {
	expectCreated(t, map[string]any{"company_name": "'; DROP TABLE users; --"})
}
func TestSB2_H03_EmailWithSQLCharsIs400(t *testing.T) {
	expectBad(t, map[string]any{"admin_email": "a'; DROP TABLE x;--@example.test"}, "admin_email")
}
func TestSB2_H04_PathTraversalSlugIs400(t *testing.T) {
	expectBad(t, map[string]any{"slug": "../../etc/passwd"}, "slug")
}

func TestSB2_H06_RawRandomCSRFTokenIs403(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	fake := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

	r := s.postWith(t, postOpts{csrf: fake, rawBody: mustJSON(t, s.trialPayload(nil))})

	assert.Equal(t, http.StatusForbidden, r.status, r.text())
	assert.Contains(t, r.text(), "CSRF_INVALID")
}

func TestSB2_H08_TamperedCookieIs401(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	flip := "A"
	if strings.HasSuffix(s.sid, "A") {
		flip = "B"
	}
	bad := *s
	bad.sid = s.sid[:len(s.sid)-1] + flip

	r := bad.postTrial(t, bad.trialPayload(nil))

	assert.Equal(t, http.StatusUnauthorized, r.status, r.text())
}

func TestSB2_H09_HugeCaptchaTokenIs413(t *testing.T) {
	s := newSession(t, randomIP(), nil)

	r := s.postTrial(t, s.trialPayload(map[string]any{"captcha_token": strings.Repeat("x", 20000)}))

	assert.Equal(t, http.StatusRequestEntityTooLarge, r.status, r.text())
}

// ── RP-1 409 answers, resubmit with other details ───────────────────────────

func TestSB2_RP02_RealmAlreadyDedicatedIs409InvalidState(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, rpURL, "rp1", "realm_already_dedicated", 1)

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusConflict, r.status, r.text())
	assert.Contains(t, r.text(), "INVALID_STATE")
}

func TestSB2_RP03_TenantOffboardedIs409InvalidState(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, rpURL, "rp1", "tenant_offboarded", 1)

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusConflict, r.status, r.text())
	assert.Contains(t, r.text(), "INVALID_STATE")
}

func TestSB2_I02_DifferentDetailsWhileTenantPendingIs409(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, omURL, "i1", "unavailable", 10)
	first := s.postTrial(t, s.trialPayload(nil))
	require.Equal(t, http.StatusAccepted, first.status, first.text())

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusConflict, r.status, r.text())
	assert.Contains(t, r.text(), "details_changed")
}

func TestSB2_I03_DifferentDetailsWhileSlugConflictIs409(t *testing.T) {
	s := slugConflictSession(t, 1)

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusConflict, r.status, r.text())
	assert.Contains(t, r.text(), "details_changed")
}

// ── locale ──────────────────────────────────────────────────────────────────

func TestSB2_UX01_AcceptLanguageArabicIsSentToI1(t *testing.T) {
	signupWithRecording(t, randomIP(), map[string]string{"Accept-Language": "ar-AE"}, nil)

	assert.Equal(t, "ar-AE", pick(one(t, omURL, "I-1").Body, "default_locale"))
}

func TestSB2_UX02_UnsupportedLanguageFallsBackToEnUS(t *testing.T) {
	signupWithRecording(t, randomIP(), map[string]string{"Accept-Language": "zh-CN"}, nil)

	assert.Equal(t, "en-US", pick(one(t, omURL, "I-1").Body, "default_locale"))
}

func TestSB2_UX03_SlugSuggestionsAreValidSlugs(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, omURL, "pub1", "slug_taken", 1)

	r := s.postTrial(t, s.trialPayload(nil))
	require.Equal(t, http.StatusConflict, r.status, r.text())

	var body struct {
		Suggestions []string `json:"suggestions"`
	}
	require.NoError(t, json.Unmarshal(r.body, &body))
	require.NotEmpty(t, body.Suggestions)
	re := regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,28}[a-z0-9])$`)
	for _, sg := range body.Suggestions {
		assert.Regexp(t, re, sg)
		assert.NotContains(t, sg, "--")
	}
}

// ── what the service sends to the mocks ─────────────────────────────────────

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func TestSB2_57_RP1CarriesIdempotencyKey(t *testing.T) {
	signupWithRecording(t, randomIP(), nil, nil)
	rp := one(t, rpURL, "RP-1")

	key := rp.Headers["idempotency-key"]
	assert.Regexp(t, `^signup:[0-9a-f-]{36}:rp1$`, key)
	assert.Equal(t, "signup:"+rp.Headers["x-tenant-id"]+":rp1", key)
}

func TestSB2_58_I1HasNoIdempotencyKey(t *testing.T) {
	signupWithRecording(t, randomIP(), nil, nil)

	_, has := one(t, omURL, "I-1").Headers["idempotency-key"]
	assert.False(t, has)
}

func TestSB2_59_TenantIDHeaderMatchesOnRP1AndI1(t *testing.T) {
	signupWithRecording(t, randomIP(), nil, nil)
	rp, i1 := one(t, rpURL, "RP-1"), one(t, omURL, "I-1")

	assert.Regexp(t, uuidRe, rp.Headers["x-tenant-id"])
	assert.Equal(t, rp.Headers["x-tenant-id"], i1.Headers["x-tenant-id"])
}

func TestSB2_60_SystemPrincipalOnAllInternalCalls(t *testing.T) {
	signupWithRecording(t, randomIP(), nil, nil)
	const system = "00000000-0000-0000-0000-0000000000a1"

	assert.Equal(t, system, one(t, omURL, "ELIGIBILITY").Headers["x-user-id"])
	assert.Equal(t, system, one(t, rpURL, "RP-1").Headers["x-user-id"])
	assert.Equal(t, system, one(t, omURL, "I-1").Headers["x-user-id"])
}

// SB2-61, C01, C02: terms_acceptance on RP-1 (server time, Envoy IP, version).
func TestSB2_61_C01_C02_TermsAcceptanceOnRP1(t *testing.T) {
	ip := randomIP()
	s, _, _ := signupWithRecording(t, ip, map[string]string{"X-Forwarded-For": "203.0.113.99"}, nil)
	rp := one(t, rpURL, "RP-1")

	ta, ok := rp.Body["terms_acceptance"].(map[string]any)
	require.True(t, ok, "RP-1 body has no terms_acceptance")
	assert.Equal(t, s.termsVersion, ta["version"])
	assert.Equal(t, ip, ta["ip_address"], "IP must come from x-envoy-external-address, not X-Forwarded-For")
	at, err := time.Parse(time.RFC3339, pick(ta, "accepted_at"))
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), at, time.Minute, "accepted_at is the server's time")
}

// SB2-C03: a long User-Agent is cut to 512 bytes on a character boundary.
func TestSB2_C03_LongUserAgentTruncatedOnRuneBoundary(t *testing.T) {
	ua := strings.Repeat("é", 300) // 600 bytes
	signupWithRecording(t, randomIP(), map[string]string{"User-Agent": ua}, nil)
	rp := one(t, rpURL, "RP-1")

	ta := rp.Body["terms_acceptance"].(map[string]any)
	got := pick(ta, "user_agent")
	assert.LessOrEqual(t, len(got), 512)
	assert.True(t, utf8.ValidString(got))
	assert.NotContains(t, got, "�")
	assert.NotEmpty(t, got)
}

func TestSB2_C04_SystemRolesHeaderOnInternalCalls(t *testing.T) {
	signupWithRecording(t, randomIP(), nil, nil)

	assert.Equal(t, "iam-system", one(t, omURL, "ELIGIBILITY").Headers["x-tenant-roles"])
	assert.Equal(t, "iam-system", one(t, rpURL, "RP-1").Headers["x-tenant-roles"])
	assert.Equal(t, "iam-system", one(t, omURL, "I-1").Headers["x-tenant-roles"])
}

func TestSB2_C05_Pub1DownSignupContinues(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, omURL, "pub1", "unavailable", 1)

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusCreated, r.status, r.text())
}

func TestSB2_C06_Pub1SlowSignupContinues(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, omURL, "pub1", "slow", 1)

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusCreated, r.status, r.text())
}

// SB2-62, C07: RP-1 and I-1 get the same plan, slug and name.
func TestSB2_62_C07_RP1AndI1CarrySamePlanSlugName(t *testing.T) {
	_, p, _ := signupWithRecording(t, randomIP(), nil, map[string]any{"plan": "pro"})
	rp, i1 := one(t, rpURL, "RP-1"), one(t, omURL, "I-1")

	assert.Equal(t, "pro", pick(rp.Body, "plan"))
	assert.Equal(t, pick(rp.Body, "plan"), pick(i1.Body, "plan"))
	assert.Equal(t, p["slug"], pick(rp.Body, "tenant_slug", "slug"))
	assert.Equal(t, pick(rp.Body, "tenant_slug", "slug"), pick(i1.Body, "slug", "tenant_slug"))
	assert.Equal(t, p["company_name"], pick(rp.Body, "tenant_name", "name"))
	assert.Equal(t, pick(rp.Body, "tenant_name", "name"), pick(i1.Body, "name", "tenant_name"))
}

func TestSB2_63_RP1CarriesHashedPasswordSeedNotThePassword(t *testing.T) {
	_, p, _ := signupWithRecording(t, randomIP(), nil, nil)
	rp := one(t, rpURL, "RP-1")

	b, _ := json.Marshal(rp.Body)
	text := strings.ToLower(string(b))
	assert.Contains(t, text, "hashed_password_seed")
	assert.Contains(t, text, "pbkdf2")
	assert.Contains(t, text, "210000")
	assert.NotContains(t, string(b), p["password"].(string))
}

// ── SB-2 metrics ────────────────────────────────────────────────────────────

func TestSB2_67_68_69_SubmitMetrics(t *testing.T) {
	m := func(name, labels string) float64 { return metricSum(getMetrics(t), name, labels) }

	submitted0 := m("bff_signup_form_submitted_total", `mode="trial"`)
	ok0 := m("bff_signup_provisioning_succeeded_total", `mode="trial"`)
	failed0 := m("bff_signup_provisioning_failed_total", "")

	s := newSession(t, randomIP(), nil)
	require.Equal(t, http.StatusCreated, s.postTrial(t, s.trialPayload(nil)).status)

	assert.Equal(t, submitted0+1, m("bff_signup_form_submitted_total", `mode="trial"`), "SB2-67")
	assert.Equal(t, ok0+1, m("bff_signup_provisioning_succeeded_total", `mode="trial"`), "SB2-68")

	// SB2-69: a slug conflict counts as a submit but NOT as a provisioning failure (OBS-43).
	submitted1 := m("bff_signup_form_submitted_total", `mode="trial"`)
	c := newSession(t, randomIP(), nil)
	pin(t, omURL, "pub1", "slug_taken", 1)
	require.Equal(t, http.StatusConflict, c.postTrial(t, c.trialPayload(nil)).status)

	assert.Equal(t, submitted1+1, m("bff_signup_form_submitted_total", `mode="trial"`))
	assert.Equal(t, failed0, m("bff_signup_provisioning_failed_total", ""), "SB2-69")
}
