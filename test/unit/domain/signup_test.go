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
