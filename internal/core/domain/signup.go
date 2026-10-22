package domain

import (
	"net/mail"
	"regexp"
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

var controlChar = regexp.MustCompile(`[\x00-\x1f\x7f]`)

// ValidateCompanyName validates the company name field.
func ValidateCompanyName(name string) error {
	name = strings.TrimSpace(name)
	if len(name) < 2 || len(name) > 100 {
		return NewError(ErrValidationFailed, "company_name must be 2–100 characters")
	}
	if controlChar.MatchString(name) {
		return NewError(ErrValidationFailed, "company_name contains invalid characters")
	}
	return nil
}

// ValidateAdminName validates the admin name field.
func ValidateAdminName(name string) error {
	name = strings.TrimSpace(name)
	if len(name) < 1 || len(name) > 100 {
		return NewError(ErrValidationFailed, "admin_name must be 1–100 characters")
	}
	if controlChar.MatchString(name) {
		return NewError(ErrValidationFailed, "admin_name contains invalid characters")
	}
	return nil
}

// ValidateAdminEmail validates the admin email field.
func ValidateAdminEmail(email string) error {
	if len(email) > 254 {
		return NewError(ErrValidationFailed, "admin_email exceeds maximum length")
	}
	addr, err := mail.ParseAddress(email)
	if err != nil {
		return NewError(ErrValidationFailed, "admin_email is not a valid email address")
	}
	// Normalise domain to lowercase
	parts := strings.SplitN(addr.Address, "@", 2)
	if len(parts) != 2 {
		return NewError(ErrValidationFailed, "admin_email is not a valid email address")
	}
	return nil
}

// NormaliseEmail returns the email with its domain part lowercased.
func NormaliseEmail(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return strings.ToLower(email)
	}
	return parts[0] + "@" + strings.ToLower(parts[1])
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
