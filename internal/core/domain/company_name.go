package domain

import "strings"

// NormaliseCompanyName returns the company name in the form it must be stored
// and forwarded to RP-1 and I-1: trimmed (LLD §5.1). Validation, the handoff
// record and the downstream calls must all see this same value.
func NormaliseCompanyName(name string) string {
	return strings.TrimSpace(name)
}
