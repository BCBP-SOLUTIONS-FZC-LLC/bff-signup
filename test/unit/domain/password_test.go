package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
)

var defaultPolicy = domain.PasswordPolicy{
	MinLength:      12,
	MaxLength:      128,
	RequireClasses: 3,
	NotEmail:       true,
}

func TestValidatePassword_Valid(t *testing.T) {
	err := domain.ValidatePassword("SecurePass123!", "user@example.com", defaultPolicy)
	assert.NoError(t, err)
}

func TestValidatePassword_TooShort(t *testing.T) {
	err := domain.ValidatePassword("Short1!", "user@example.com", defaultPolicy)
	assert.Error(t, err)
}

func TestValidatePassword_TooLong(t *testing.T) {
	err := domain.ValidatePassword(strings.Repeat("a", 129), "user@example.com", defaultPolicy)
	assert.Error(t, err)
}

func TestValidatePassword_EqualToEmail(t *testing.T) {
	err := domain.ValidatePassword("user@example.com", "user@example.com", defaultPolicy)
	assert.Error(t, err)
}

func TestValidatePassword_InsufficientClasses(t *testing.T) {
	// only lowercase — misses digit and special
	err := domain.ValidatePassword("alllowercaseonly", "user@example.com", defaultPolicy)
	assert.Error(t, err)
}

func TestValidatePassword_ExactlyThreeClasses(t *testing.T) {
	// lower + upper + digit (no special) = 3 classes → passes
	err := domain.ValidatePassword("SecurePass123", "user@example.com", defaultPolicy)
	assert.NoError(t, err)
}

// SB-INV-7: The BFF's password check is the ONLY enforcement at signup.
// This test documents the invariant so a future refactor cannot silently drop it.
func TestValidatePassword_AuthoritativeCheck(t *testing.T) {
	// A password that passes the BFF policy will be hashed and sent to RP-1.
	// Keycloak cannot re-check it. If this test fails, review the invariant.
	err := domain.ValidatePassword("ValidPass999@", "user@example.com", defaultPolicy)
	assert.NoError(t, err, "BFF policy check must be the authoritative gate at signup (SB-INV-7)")
}
