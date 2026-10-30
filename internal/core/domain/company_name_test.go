package domain

import "testing"

func TestNormaliseCompanyName(t *testing.T) {
	cases := map[string]string{
		"Acme":                "Acme",
		"  Acme  ":            "Acme",
		"\tAcme Bidding Co\n": "Acme Bidding Co",
		"      ":              "",
	}
	for in, want := range cases {
		if got := NormaliseCompanyName(in); got != want {
			t.Errorf("NormaliseCompanyName(%q) = %q, want %q", in, got, want)
		}
	}
}
