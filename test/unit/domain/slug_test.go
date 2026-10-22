package domain_test

import (
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
