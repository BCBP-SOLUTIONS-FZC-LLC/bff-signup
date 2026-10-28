package domain_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
)

func TestDeriveSlug(t *testing.T) {
	cases := []struct {
		company string
		want    string
	}{
		{"Acme Bidding Co", "acme-bidding-co"},
		{"123 Solutions", "123-solutions"},
		{"  spaces  ", "spaces"},
		{"Hello & World!", "hello-world"},
		{"A", "a-"}, // too short — random suffix added
	}
	for _, c := range cases {
		got := domain.DeriveSlug(c.company)
		if strings.HasPrefix(c.want, "a-") {
			// short result gets a random suffix — just check the prefix
			assert.True(t, len(got) >= 3, "derived slug must be >= 3 chars: %q", got)
		} else {
			assert.Equal(t, c.want, got)
		}
		// derived slug must always pass validation
		require.NoError(t, domain.ValidateSlug(got, nil))
	}
}

// TestDeriveSlug_AllNonASCII verifies that a fully non-ASCII company name (e.g. Arabic or CJK)
// produces a valid slug rather than an invalid "-xxxx" that would fail ValidateSlug.
// TestSuggestSlugs_MaxLengthBase_TerminatesAndReturnsValid verifies the 50-attempt
// iteration cap prevents an infinite loop when the base slug is at its maximum
// length (30 chars). Every suffix attempt truncates back to the same base, so
// without the cap the loop never reaches len(suggestions)==3.
func TestSuggestSlugs_MaxLengthBase_TerminatesAndReturnsValid(t *testing.T) {
	// 30-char base — exactly at SlugMaxLen; every appended suffix gets truncated back
	base := "abcdefghijklmnopqrstuvwxyz1234" // 30 chars
	require.Len(t, base, 30)

	// SuggestSlugs must return within a bounded time (not loop forever) and
	// every returned suggestion must pass ValidateSlug.
	suggestions := domain.SuggestSlugs(base, "Abcdefghijklmnopqrstuvwxyz 1234", nil)
	assert.LessOrEqual(t, len(suggestions), 3, "must not return more than 3 suggestions")
	for _, s := range suggestions {
		assert.NoError(t, domain.ValidateSlug(s, nil), "every suggestion must be valid: %q", s)
	}
	// The function MUST terminate (if it loops forever this test times out).
}

func TestDeriveSlug_AllNonASCII(t *testing.T) {
	names := []string{
		"شركتي",        // Arabic
		"我的公司",         // Chinese
		"моя компания", // Russian (Cyrillic)
	}
	for _, name := range names {
		got := domain.DeriveSlug(name)
		assert.GreaterOrEqual(t, len(got), 3, "slug must be at least 3 chars for %q: got %q", name, got)
		assert.False(t, strings.HasPrefix(got, "-"), "slug must not start with hyphen for %q: got %q", name, got)
		require.NoError(t, domain.ValidateSlug(got, nil), "derived slug must be valid for %q: got %q", name, got)
	}
}

func TestValidateSlug_Valid(t *testing.T) {
	valid := []string{
		"acme", "acme-bidding", "abc123", "my-company-hq",
		"a1b", "xyz",
	}
	for _, s := range valid {
		assert.NoError(t, domain.ValidateSlug(s, nil), "slug %q should be valid", s)
	}
}

func TestValidateSlug_Invalid(t *testing.T) {
	invalid := []string{
		"",                      // too short
		"ab",                    // too short
		"-abc",                  // leading hyphen
		"abc-",                  // trailing hyphen
		"abc--def",              // double hyphen
		"ABC",                   // uppercase
		"abc def",               // space
		"admin",                 // reserved
		"signup",                // reserved
		"keycloak",              // reserved
		strings.Repeat("a", 31), // too long
	}
	for _, s := range invalid {
		assert.Error(t, domain.ValidateSlug(s, nil), "slug %q should be invalid", s)
	}
}

func TestValidateSlug_SubsetOfOMRule(t *testing.T) {
	// BFF rule is a strict subset of O&M's DNS-label rule (3-30 chars vs 3-63).
	// Anything the BFF accepts, O&M also accepts (LLD §5.8).
	slug := strings.Repeat("a", 30) // max BFF length — O&M allows up to 63
	assert.NoError(t, domain.ValidateSlug(slug, nil))
}

func TestSuggestSlugs(t *testing.T) {
	suggestions := domain.SuggestSlugs("acme", "Acme Bidding Corp", nil)
	assert.LessOrEqual(t, len(suggestions), 3)
	assert.GreaterOrEqual(t, len(suggestions), 1)
	for _, s := range suggestions {
		assert.NoError(t, domain.ValidateSlug(s, nil), "suggestion %q must be valid", s)
		assert.NotEqual(t, "acme", s, "suggestion must differ from the conflicting slug")
	}
}

// TestSuggestSlugs_DeduplicatesViaAppendUniq exercises the appendUniq deduplication path.
// When a word-derived candidate equals a numbered candidate, appendUniq's `existing == s` branch fires.
// We need the word loop to generate exactly 1 candidate (word "2" → "base-2") and the numbered loop
// to try the same candidate ("base-2"). Use a company name with only "2" as the useful word.
func TestSuggestSlugs_DeduplicatesViaAppendUniq(t *testing.T) {
	// Only one word "2" generates "base-2". Numbered variant n=2 also gives "base-2".
	// Reserve "base-3" through "base-9" so numbered loop gets stuck on "base-2" → appendUniq dedup.
	reserved := map[string]bool{
		"base-3": true, "base-4": true, "base-5": true, "base-6": true,
		"base-7": true, "base-8": true, "base-9": true,
	}
	// Company is just "2" so word loop generates only "base-2"
	suggestions := domain.SuggestSlugs("base", "2", reserved)
	// appendUniq ensures no duplicates in the result
	seen := make(map[string]bool)
	for _, s := range suggestions {
		assert.False(t, seen[s], "duplicate suggestion: %q", s)
		seen[s] = true
	}
	assert.LessOrEqual(t, len(suggestions), 3)
}

// TestSuggestSlugs_NeedsRandomSuffix exercises the random-suffix loop in SuggestSlugs.
// When there are no useful word-derived candidates and numbered variants are exhausted/reserved,
// the function falls back to random suffixes.
func TestSuggestSlugs_NeedsRandomSuffix(t *testing.T) {
	// Reserve all numbered variants (-2 through -9) and use a base with no useful word-derived candidates.
	reserved := map[string]bool{
		"xyz-2": true, "xyz-3": true, "xyz-4": true, "xyz-5": true,
		"xyz-6": true, "xyz-7": true, "xyz-8": true, "xyz-9": true,
	}
	// Company name "xyz" means word-derived candidates would be "xyz-xyz" (same word = still valid),
	// but let's use a name with only the base word so no useful word candidates either.
	// Empty company name avoids word candidates entirely.
	suggestions := domain.SuggestSlugs("xyz", "", reserved)
	assert.LessOrEqual(t, len(suggestions), 3)
	// All should be valid slugs
	for _, s := range suggestions {
		assert.NoError(t, domain.ValidateSlug(s, nil), "random suggestion %q must be valid", s)
	}
}

// TestSuggestSlugs_RandomSuffixLongBase exercises the truncation branch in the random-suffix loop.
// When the base is close to SlugMaxLen, random suffixes may make the candidate too long.
func TestSuggestSlugs_RandomSuffixLongBase(t *testing.T) {
	// 26-char base: random suffix "-XXXX" = 5 chars → total 31 > SlugMaxLen(30) → truncates
	longBase := strings.Repeat("a", 26)
	reserved := map[string]bool{}
	// Reserve numbered variants to force random suffix path
	for n := 2; n <= 9; n++ {
		candidate := fmt.Sprintf("%s-%d", longBase, n)
		reserved[candidate] = true
	}
	// Company name with only long words (also exceeds limit) to skip word-derived path
	suggestions := domain.SuggestSlugs(longBase, "VeryLongCompanyWordHere", reserved)
	for _, s := range suggestions {
		assert.LessOrEqual(t, len(s), 30)
	}
}

// TestSuggestSlugs_EmptyCompanyName exercises the path where the company name is empty (no words).
func TestSuggestSlugs_EmptyCompanyName(t *testing.T) {
	suggestions := domain.SuggestSlugs("base", "", nil)
	// With empty company name, no word-derived candidates — should get numbered+random
	assert.LessOrEqual(t, len(suggestions), 3)
	assert.GreaterOrEqual(t, len(suggestions), 1)
}

// TestSuggestSlugs_LongBase exercises the slug-too-long branches in SuggestSlugs.
// When base is close to SlugMaxLen, word-derived candidates and numbered variants
// may exceed the limit, triggering the truncation/break branches.
func TestSuggestSlugs_LongBase(t *testing.T) {
	// A 28-char base: any word suffix would exceed SlugMaxLen (30)
	longBase := strings.Repeat("a", 28)
	// Company name with a long word so word+base exceeds limit
	suggestions := domain.SuggestSlugs(longBase, "VeryLongCompanyNameHere", nil)
	// All suggestions must be valid slugs ≤ 30 chars
	for _, s := range suggestions {
		assert.LessOrEqual(t, len(s), 30)
		assert.NoError(t, domain.ValidateSlug(s, nil), "suggestion %q must be valid", s)
	}
	assert.LessOrEqual(t, len(suggestions), 3)
}

// TestDeriveSlug_NonASCII exercises the `r > unicode.MaxASCII` branch in DeriveSlug.
// Non-ASCII characters are dropped from the slug.
func TestDeriveSlug_NonASCII(t *testing.T) {
	// "Café" has 'é' (non-ASCII) which should be dropped → "caf" → extended with suffix
	got := domain.DeriveSlug("Café Corp")
	assert.NoError(t, domain.ValidateSlug(got, nil), "slug from non-ASCII name must be valid: %q", got)
}

// TestDeriveSlug_LongName exercises the `len(s) > SlugMaxLen` truncation branch in DeriveSlug.
func TestDeriveSlug_LongName(t *testing.T) {
	// 50+ characters → triggers truncation at SlugMaxLen (30)
	longName := "abcdefghijklmnopqrstuvwxyz-abcdefghijklmnopqrstuvwxyz"
	got := domain.DeriveSlug(longName)
	assert.LessOrEqual(t, len(got), 30)
	assert.NoError(t, domain.ValidateSlug(got, nil), "truncated slug must be valid")
}

// TestValidateAdminEmail_TooLong exercises the len > 254 branch in ValidateAdminEmail.
func TestValidateAdminEmail_TooLong(t *testing.T) {
	// Build an email longer than 254 chars (255+ total)
	localPart := strings.Repeat("a", 244)
	email := localPart + "@example.com" // 244 + 12 = 256 chars
	err := domain.ValidateAdminEmail(email)
	assert.Error(t, err)
}

// TestValidateCompanyName_ControlChar exercises C0 control-char rejection in ValidateCompanyName.
func TestValidateCompanyName_ControlChar(t *testing.T) {
	err := domain.ValidateCompanyName("Acme\x01Corp")
	assert.Error(t, err)
}

// TestValidateCompanyName_C1ControlChar exercises C1 control-char rejection (unicode.IsControl, not regex).
// U+0085 is NEL (Next Line), a C1 control missed by the old [\x00-\x1f\x7f] regex.
func TestValidateCompanyName_C1ControlChar(t *testing.T) {
	s := "Acme" + string(rune(0x85)) + "Corp" // U+0085 NEL
	err := domain.ValidateCompanyName(s)
	assert.Error(t, err)
}

// TestValidateCompanyName_UnicodeName exercises rune-based length counting for multi-byte chars.
func TestValidateCompanyName_UnicodeName(t *testing.T) {
	// 10 Japanese chars = 30 bytes — must pass (len counts runes, not bytes)
	err := domain.ValidateCompanyName("日本株式会社テスト社")
	assert.NoError(t, err)
}

// TestValidateAdminName_ControlChar exercises C0 control-char rejection in ValidateAdminName.
func TestValidateAdminName_ControlChar(t *testing.T) {
	err := domain.ValidateAdminName("Dana\x00Owner")
	assert.Error(t, err)
}

// TestValidateAdminName_C1ControlChar exercises C1 control-char rejection (unicode.IsControl, not regex).
// U+009F is APC (Application Program Command), a C1 control missed by the old regex.
func TestValidateAdminName_C1ControlChar(t *testing.T) {
	s := "Dana" + string(rune(0x9f)) + "Owner" // U+009F APC
	err := domain.ValidateAdminName(s)
	assert.Error(t, err)
}

// TestValidateAdminName_UnicodeName exercises rune-based length counting for multi-byte chars.
// Arabic text is ~20 bytes for ~10 chars — must pass because the limit is 100 runes, not bytes.
func TestValidateAdminName_UnicodeName(t *testing.T) {
	err := domain.ValidateAdminName("محمد علي محمد")
	assert.NoError(t, err)
}

// TestValidateAdminName_AtLimit_100Runes verifies exactly 100 runes is accepted (LLD §5.4 — B-2 fix).
func TestValidateAdminName_AtLimit_100Runes(t *testing.T) {
	err := domain.ValidateAdminName(strings.Repeat("a", 100))
	assert.NoError(t, err)
}

// TestValidateAdminName_OverLimit_101Runes verifies 101 runes is rejected (LLD §5.4 — B-2 fix).
func TestValidateAdminName_OverLimit_101Runes(t *testing.T) {
	err := domain.ValidateAdminName(strings.Repeat("a", 101))
	assert.Error(t, err)
}

// TestNormaliseEmail_NoAtSign exercises NormaliseEmail with a non-email string.
func TestNormaliseEmail_NoAtSign(t *testing.T) {
	result := domain.NormaliseEmail("NOTANEMAIL")
	assert.Equal(t, "notanemail", result)
}

// TestNormaliseEmail_LowercasesFullAddress exercises that NormaliseEmail lowercases both
// local and domain parts (not just the domain).
func TestNormaliseEmail_LowercasesFullAddress(t *testing.T) {
	result := domain.NormaliseEmail("Alice@EXAMPLE.COM")
	assert.Equal(t, "alice@example.com", result)
}

// TestValidateAdminEmail_DisplayNameRejected ensures "Alice <alice@example.com>" is rejected.
func TestValidateAdminEmail_DisplayNameRejected(t *testing.T) {
	err := domain.ValidateAdminEmail("alice <alice@example.com>")
	assert.Error(t, err)
}

// TestValidateAdminEmail_UppercaseRejected ensures non-lowercase emails are rejected.
func TestValidateAdminEmail_UppercaseRejected(t *testing.T) {
	err := domain.ValidateAdminEmail("Alice@example.com")
	assert.Error(t, err)
}

// TestValidateAdminEmail_LocalPartTooLong exercises the 64-char local-part limit (RFC 5321).
func TestValidateAdminEmail_LocalPartTooLong(t *testing.T) {
	local := strings.Repeat("a", 65)
	err := domain.ValidateAdminEmail(local + "@example.com")
	assert.Error(t, err)
}

// TestValidateAdminEmail_ConsecutiveDots exercises the no-consecutive-dots rule.
func TestValidateAdminEmail_ConsecutiveDots(t *testing.T) {
	err := domain.ValidateAdminEmail("john..doe@example.com")
	assert.Error(t, err)
}

// TestValidateAdminEmail_PunycodeAccepted verifies that a properly punycode-encoded
// international domain (IDN) is accepted. The code says "use punycode for
// international domains" but only the reject path (raw non-ASCII) was tested before.
func TestValidateAdminEmail_PunycodeAccepted(t *testing.T) {
	// xn--e1afmkfd.xn--p1ai is the ACE encoding of кирилл.рф (Cyrillic/Russian).
	// A valid, all-ASCII punycode domain must be accepted.
	err := domain.ValidateAdminEmail("user@xn--e1afmkfd.xn--p1ai")
	assert.NoError(t, err, "properly punycode-encoded IDN domain must be accepted")
}

// TestValidateAdminEmail_RawNonASCIIDomainRejected verifies that a raw Unicode
// domain (not punycode-encoded) is rejected with a helpful message.
func TestValidateAdminEmail_RawNonASCIIDomainRejected(t *testing.T) {
	err := domain.ValidateAdminEmail("user@кирилл.рф")
	assert.Error(t, err, "raw Unicode domain must be rejected; punycode required")
}

// TestValidateAdminEmail_LeadingDot exercises the no-leading-dot rule.
func TestValidateAdminEmail_LeadingDot(t *testing.T) {
	err := domain.ValidateAdminEmail(".john@example.com")
	assert.Error(t, err)
}

// TestValidateAdminEmail_TrailingDot exercises the no-trailing-dot rule.
func TestValidateAdminEmail_TrailingDot(t *testing.T) {
	err := domain.ValidateAdminEmail("john.@example.com")
	assert.Error(t, err)
}

// TestValidateAdminEmail_ShortTLD exercises the TLD minimum 2-char rule.
func TestValidateAdminEmail_ShortTLD(t *testing.T) {
	err := domain.ValidateAdminEmail("john@example.x")
	assert.Error(t, err)
}

// TestValidateAdminEmail_Valid exercises a clean, compliant address.
func TestValidateAdminEmail_Valid(t *testing.T) {
	err := domain.ValidateAdminEmail("alice.smith@acme-corp.example.com")
	assert.NoError(t, err)
}
