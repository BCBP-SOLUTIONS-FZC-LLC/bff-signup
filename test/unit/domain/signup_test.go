package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
)

func TestNormaliseCompanyName(t *testing.T) {
	cases := map[string]string{
		"Acme":                "Acme",
		"  Acme  ":            "Acme",
		"\tAcme Bidding Co\n": "Acme Bidding Co",
		"      ":              "",
	}
	for in, want := range cases {
		assert.Equal(t, want, domain.NormaliseCompanyName(in), "input %q", in)
	}
}

// Addresses with misplaced dots are rejected by the address parser before the
// dot-specific checks in ValidateAdminEmail are reached, so they get the generic message.
func TestValidateAdminEmail_RejectsEmptyAndMisplacedDots(t *testing.T) {
	cases := map[string]string{
		"":                 "admin_email is required",
		"a..b@example.com": "must be a valid email address",
		".ab@example.com":  "must be a valid email address",
		"ab.@example.com":  "must be a valid email address",
	}
	for email, want := range cases {
		err := domain.ValidateAdminEmail(email)

		var de *domain.DomainError
		if assert.ErrorAs(t, err, &de, "email %q", email) {
			assert.ErrorIs(t, de.Cause, domain.ErrValidationFailed, "email %q", email)
			assert.Contains(t, de.Message, want, "email %q", email)
		}
	}
}
