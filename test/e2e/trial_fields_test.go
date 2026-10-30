//go:build e2e

package e2e_test

// Stage 2, batch 2: SB-2 field validation (SB2-10..31), rate limits (SB2-34,
// SB2-35) and the three 409 answers from the mocks (SB2-38..40).
//
// Not automated here (stay manual): SB2-32/33/36 need a real Turnstile
// (CAPTCHA_SKIP=true locally); SB2-37a/37b are timing cases.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// expectInvalid sends a trial with one field changed and expects 400 whose body
// mentions want (a field name or a word from the error detail).
func expectInvalid(t *testing.T, overrides map[string]any, want string) {
	t.Helper()
	s := newSession(t, randomIP(), nil)

	r := s.postTrial(t, s.trialPayload(overrides))

	assert.Equal(t, http.StatusBadRequest, r.status, r.text())
	assert.Contains(t, strings.ToLower(r.text()), strings.ToLower(want))
}

func TestSB2_10_CompanyName101CharsIs400(t *testing.T) {
	expectInvalid(t, map[string]any{"company_name": strings.Repeat("A", 101)}, "company_name")
}

func TestSB2_11_CompanyNameControlCharIs400(t *testing.T) {
	expectInvalid(t, map[string]any{"company_name": "Acme\x01Co"}, "company_name")
}

func TestSB2_12_SlugUppercaseIs400(t *testing.T) {
	expectInvalid(t, map[string]any{"slug": "Acme-Corp"}, "slug")
}

func TestSB2_13_SlugTwoCharsIs400(t *testing.T) {
	expectInvalid(t, map[string]any{"slug": "ab"}, "slug")
}

func TestSB2_14_Slug31CharsIs400(t *testing.T) {
	expectInvalid(t, map[string]any{"slug": strings.Repeat("a", 31)}, "slug")
}

func TestSB2_15_SlugDoubleHyphenIs400(t *testing.T) {
	expectInvalid(t, map[string]any{"slug": "ac--me"}, "slug")
}

func TestSB2_16_ReservedSlugIs400(t *testing.T) {
	expectInvalid(t, map[string]any{"slug": "admin"}, "reserved")
}

func TestSB2_17_SlugAbsentIsDerivedFromCompany(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	p := s.trialPayload(map[string]any{"company_name": "E2E Derive " + randomTag()})
	delete(p, "slug")

	r := s.postTrial(t, p)

	assert.Equal(t, http.StatusCreated, r.status, r.text())
}

func TestSB2_18_AdminNameEmptyIs400(t *testing.T) {
	expectInvalid(t, map[string]any{"admin_name": ""}, "admin_name")
}

func TestSB2_19_AdminName101CharsIs400(t *testing.T) {
	expectInvalid(t, map[string]any{"admin_name": strings.Repeat("A", 101)}, "admin_name")
}

func TestSB2_20_AdminEmailInvalidIs400(t *testing.T) {
	expectInvalid(t, map[string]any{"admin_email": "not-an-email"}, "admin_email")
}

func TestSB2_21_AdminEmailOver254CharsIs400(t *testing.T) {
	email := strings.Repeat("a", 242) + "@example.test" // 255 characters
	require.Len(t, email, 255)
	expectInvalid(t, map[string]any{"admin_email": email}, "admin_email")
}

func TestSB2_22_PasswordUnder12CharsIs400(t *testing.T) {
	expectInvalid(t, map[string]any{"password": "Ab1!xyz"}, "password")
}

func TestSB2_23_PasswordOver128CharsIs400(t *testing.T) {
	expectInvalid(t, map[string]any{"password": "Aa1!" + strings.Repeat("x", 125)}, "password")
}

func TestSB2_24_PasswordTwoCharClassesIs400(t *testing.T) {
	expectInvalid(t, map[string]any{"password": "alllowercase123"}, "password")
}

func TestSB2_25_PasswordEqualsEmailIs400(t *testing.T) {
	same := "Abcdef1!ghij@example.test"
	expectInvalid(t, map[string]any{"admin_email": same, "password": same}, "password")
}

func TestSB2_26_UnknownPlanIs400(t *testing.T) {
	expectInvalid(t, map[string]any{"plan": "gold"}, "plan")
}

func TestSB2_27_PlanTrialIs400(t *testing.T) {
	expectInvalid(t, map[string]any{"plan": "trial"}, "plan")
}

func TestSB2_28_AcceptTermsFalseIs400(t *testing.T) {
	expectInvalid(t, map[string]any{"accept_terms": false}, "terms")
}

func TestSB2_29_TermsVersionEmptyIs400(t *testing.T) {
	expectInvalid(t, map[string]any{"terms_version": ""}, "terms_version")
}

func TestSB2_30_TermsVersionNotCurrentIs400(t *testing.T) {
	expectInvalid(t, map[string]any{"terms_version": "1999-01"}, "terms_version")
}

func TestSB2_31_CaptchaTokenEmptyIs400(t *testing.T) {
	expectInvalid(t, map[string]any{"captcha_token": ""}, "captcha_token")
}

// ── rate limits ─────────────────────────────────────────────────────────────

// SB2-34: the 4th signup from one IP within an hour is refused (limit 3/hour).
func TestSB2_34_FourthSignupFromSameIPIs429(t *testing.T) {
	ip := randomIP()
	for i := 1; i <= 3; i++ {
		s := newSession(t, ip, nil)
		r := s.postTrial(t, s.trialPayload(nil))
		require.Equal(t, http.StatusCreated, r.status, "signup %d: %s", i, r.text())
	}

	s := newSession(t, ip, nil)
	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusTooManyRequests, r.status, r.text())
	assert.Contains(t, r.text(), "RATE_LIMITED")
}

// SB2-35: the 6th attempt with one e-mail within 24 h is refused (limit 5/day).
// Each attempt comes from a different IP so the per-IP limit is not hit first.
func TestSB2_35_SixthAttemptForSameEmailIs429(t *testing.T) {
	email := "e2e+" + randomTag() + "@example.test"
	for i := 1; i <= 5; i++ {
		s := newSession(t, randomIP(), nil)
		r := s.postTrial(t, s.trialPayload(map[string]any{"admin_email": email}))
		require.Equal(t, http.StatusCreated, r.status, "attempt %d: %s", i, r.text())
	}

	s := newSession(t, randomIP(), nil)
	r := s.postTrial(t, s.trialPayload(map[string]any{"admin_email": email}))

	assert.Equal(t, http.StatusTooManyRequests, r.status, r.text())
	assert.Contains(t, r.text(), "RATE_LIMITED")
}

// ── 409 answers from downstream (mocks pinned) ──────────────────────────────

func TestSB2_38_EligibilityTrialAlreadyUsedIs409(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, omURL, "eligibility", "trial_already_used", 1)

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusConflict, r.status, r.text())
}

func TestSB2_39_Pub1SlugTakenIs409WithSuggestions(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, omURL, "pub1", "slug_taken", 1)

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusConflict, r.status, r.text())
	assert.Contains(t, r.text(), "suggestions")
}

func TestSB2_40_RP1EmailAlreadyRegisteredIs409(t *testing.T) {
	s := newSession(t, randomIP(), nil)
	pin(t, rpURL, "rp1", "email_already_registered", 1)

	r := s.postTrial(t, s.trialPayload(nil))

	assert.Equal(t, http.StatusConflict, r.status, r.text())
	assert.Contains(t, strings.ToUpper(r.text()), "EMAIL")
}
