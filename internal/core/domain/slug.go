package domain

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"unicode"
)

const (
	SlugMinLen = 3
	SlugMaxLen = 30
)

var (
	slugPattern    = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,28}[a-z0-9])$`)
	nonAlphanumRun = regexp.MustCompile(`[^a-z0-9]+`)

	// reservedSlugs is the default set; configurable via Config.Slug.Reserved.
	defaultReservedSlugs = map[string]bool{
		"admin": true, "api": true, "app": true, "auth": true,
		"billing": true, "help": true, "login": true, "signup": true,
		"status": true, "support": true, "trial": true, "www": true,
		"mail": true, "static": true, "cdn": true, "internal": true,
		"system": true, "root": true, "tenant": true, "keycloak": true,
	}
)

// ValidateSlug returns an error if s does not match the slug rules (LLD §5.8).
func ValidateSlug(s string, reserved map[string]bool) error {
	if len(s) < SlugMinLen || len(s) > SlugMaxLen {
		return NewError(ErrValidationFailed, "slug length must be 3–30 characters")
	}
	if strings.Contains(s, "--") {
		return NewError(ErrValidationFailed, "slug must not contain consecutive hyphens")
	}
	if !slugPattern.MatchString(s) {
		return NewError(ErrValidationFailed, "slug must be lowercase alphanumeric with hyphens, no leading/trailing/double hyphens")
	}
	r := reserved
	if r == nil {
		r = defaultReservedSlugs
	}
	if r[s] {
		return NewError(ErrValidationFailed, "slug is reserved")
	}
	return nil
}

// DeriveSlug derives a valid slug from companyName (LLD §5.8).
// If the derived result is < 3 chars, a random suffix is appended.
func DeriveSlug(companyName string) string {
	// Lowercase and strip non-ASCII characters (simple ASCII-fold)
	s := strings.Map(func(r rune) rune {
		if r > unicode.MaxASCII {
			return -1 // drop non-ASCII
		}
		return unicode.ToLower(r)
	}, companyName)
	s = nonAlphanumRun.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > SlugMaxLen {
		s = s[:SlugMaxLen]
		s = strings.TrimRight(s, "-")
	}
	if len(s) < SlugMinLen {
		s = s + "-" + randomSlugSuffix(4)
	}
	return s
}

// SuggestSlugs generates up to 3 alternative slugs after a slug conflict (LLD §5.8).
func SuggestSlugs(base, companyName string, reserved map[string]bool) []string {
	var suggestions []string

	// From a word in the company name
	words := strings.Fields(companyName)
	for _, w := range words {
		candidate := base + "-" + strings.ToLower(w)
		candidate = nonAlphanumRun.ReplaceAllString(candidate, "-")
		candidate = strings.Trim(candidate, "-")
		if len(candidate) > SlugMaxLen {
			candidate = candidate[:SlugMaxLen]
			candidate = strings.TrimRight(candidate, "-")
		}
		if ValidateSlug(candidate, reserved) == nil {
			suggestions = appendUniq(suggestions, candidate)
		}
		if len(suggestions) >= 3 {
			return suggestions
		}
	}

	// Numbered variants
	for n := 2; n <= 9 && len(suggestions) < 3; n++ {
		candidate := fmt.Sprintf("%s-%d", base, n)
		if len(candidate) > SlugMaxLen {
			break
		}
		if ValidateSlug(candidate, reserved) == nil {
			suggestions = appendUniq(suggestions, candidate)
		}
	}

	// Random suffix
	for len(suggestions) < 3 {
		candidate := base + "-" + randomSlugSuffix(4)
		if len(candidate) > SlugMaxLen {
			candidate = candidate[:SlugMaxLen]
			candidate = strings.TrimRight(candidate, "-")
		}
		if ValidateSlug(candidate, reserved) == nil {
			suggestions = appendUniq(suggestions, candidate)
		}
	}

	return suggestions
}

func appendUniq(ss []string, s string) []string {
	for _, existing := range ss {
		if existing == s {
			return ss
		}
	}
	return append(ss, s)
}

const slugChars = "abcdefghijklmnopqrstuvwxyz0123456789"

func randomSlugSuffix(n int) string {
	b := make([]byte, n)
	for i := range b {
		idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(slugChars))))
		b[i] = slugChars[idx.Int64()]
	}
	return string(b)
}
