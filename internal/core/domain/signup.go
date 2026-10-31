package domain

import (
	"net/mail"
	"strings"
	"unicode"
)

// PasswordPolicy mirrors the Keycloak trial realm policy (LLD §10.6, C-4).
// The BFF's check is the only policy enforcement at signup — Keycloak cannot
// re-check a pre-hashed credential.
type PasswordPolicy struct {
	MinLength      int  `json:"min_length"`
	MaxLength      int  `json:"max_length"`
	RequireClasses int  `json:"require_classes"`
	NotEmail       bool `json:"not_email"`
}

// SlugRules is the slug constraint sent to the browser in SB-1.
type SlugRules struct {
	Pattern string `json:"pattern"`
	Min     int    `json:"min"`
	Max     int    `json:"max"`
}

const (
	emailMaxLen     = 254 // RFC 5321 §4.5.3
	localPartMaxLen = 64  // RFC 5321 §4.5.3
)

// ValidateCompanyName validates the company name field.
func ValidateCompanyName(name string) error {
	name = strings.TrimSpace(name)
	runes := []rune(name)
	if len(runes) < 2 || len(runes) > 100 {
		return NewError(ErrValidationFailed, "company_name must be 2–100 characters")
	}
	for _, r := range runes {
		if unicode.IsControl(r) {
			return NewError(ErrValidationFailed, "company_name contains invalid characters")
		}
	}
	return nil
}

// ValidateAdminName validates the admin name field.
func ValidateAdminName(name string) error {
	name = strings.TrimSpace(name)
	runes := []rune(name)
	if len(runes) < 1 || len(runes) > 100 {
		return NewError(ErrValidationFailed, "admin_name must be 1–100 characters")
	}
	for _, r := range runes {
		if unicode.IsControl(r) {
			return NewError(ErrValidationFailed, "admin_name contains invalid characters")
		}
	}
	return nil
}

// ValidateAdminEmail validates the admin email field against RFC 5321/5322 rules.
// The caller must pass a pre-normalised (fully lower-cased) address; see NormaliseEmail.
func ValidateAdminEmail(email string) error {
	if email == "" {
		return NewError(ErrValidationFailed, "admin_email is required")
	}
	if len(email) > emailMaxLen {
		return NewError(ErrValidationFailed, "admin_email must not exceed 254 characters")
	}
	// ParseAddress rejects malformed addresses; addr.Address != email rejects
	// display-name syntax ("Alice <alice@example.com>").
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return NewError(ErrValidationFailed, "admin_email must be a valid email address")
	}
	if email != strings.ToLower(email) {
		return NewError(ErrValidationFailed, "admin_email must be lowercase")
	}
	atIdx := strings.LastIndex(email, "@")
	local := email[:atIdx]
	domainPart := email[atIdx+1:]
	if len(local) > localPartMaxLen {
		return NewError(ErrValidationFailed, "admin_email local part must not exceed 64 characters")
	}
	if strings.Contains(local, "..") {
		return NewError(ErrValidationFailed, "admin_email local part must not contain consecutive dots")
	}
	if strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") {
		return NewError(ErrValidationFailed, "admin_email local part must not start or end with a dot")
	}
	for _, r := range domainPart {
		if r > 127 {
			return NewError(ErrValidationFailed, "admin_email domain must be ASCII (use punycode for international domains)")
		}
	}
	lastDot := strings.LastIndex(domainPart, ".")
	if lastDot < 0 || len(domainPart[lastDot+1:]) < 2 {
		return NewError(ErrValidationFailed, "admin_email domain must have a TLD of at least 2 characters")
	}
	return nil
}

// NormaliseEmail returns the email fully lowercased. Always call this before ValidateAdminEmail.
func NormaliseEmail(email string) string {
	return strings.ToLower(email)
}

// NormaliseCompanyName returns the company name in the form it must be stored
// and forwarded to RP-1 and I-1: trimmed (LLD §5.1). Validation, the handoff
// record and the downstream calls must all see this same value.
func NormaliseCompanyName(name string) string {
	return strings.TrimSpace(name)
}

// ValidatePassword checks the password against the policy (LLD §10.6).
// The plain password is compared here and then discarded; it never leaves this
// process except as a PBKDF2 hash sent to RP-1.
func ValidatePassword(password, email string, policy PasswordPolicy) error {
	if len(password) < policy.MinLength {
		return NewError(ErrValidationFailed, "password does not meet minimum length requirement")
	}
	if len(password) > policy.MaxLength {
		return NewError(ErrValidationFailed, "password exceeds maximum length")
	}
	if policy.NotEmail && strings.EqualFold(password, email) {
		return NewError(ErrValidationFailed, "password must not equal the email address")
	}
	if policy.RequireClasses > 0 {
		if countCharClasses(password) < policy.RequireClasses {
			return NewError(ErrValidationFailed, "password must contain at least 3 character classes (upper, lower, digit, special)")
		}
	}
	return nil
}

func countCharClasses(s string) int {
	var hasUpper, hasLower, hasDigit, hasSpecial bool
	for _, r := range s {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		default:
			hasSpecial = true
		}
	}
	n := 0
	for _, b := range []bool{hasUpper, hasLower, hasDigit, hasSpecial} {
		if b {
			n++
		}
	}
	return n
}
