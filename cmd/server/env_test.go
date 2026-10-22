package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/config"
)

// setValidEnv sets the minimum valid environment for config.Load() to succeed.
func setValidEnv(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{
		"VALKEY_URL":                 "redis://localhost:6379/0",
		"SBFF_HMAC_KEY":              "this-is-a-32-byte-hmac-key-dev!!",
		"SBFF_TURNSTILE_SECRET":      "1x0000000000000000000000000000000AA",
		"CAPTCHA_SITE_KEY":           "1x00000000000000000000AA",
		"REALM_PROVISIONER_BASE_URL": "http://localhost:9001",
		"ORG_MEMBERSHIP_BASE_URL":    "http://localhost:9002",
		"PBKDF2_ITERATIONS":          "210000",
		"PUBLIC_HOST":                "signup.bcbpsolutions.com",
	} {
		t.Setenv(k, v)
	}
}

func TestConfigLoad_ValidEnv(t *testing.T) {
	setValidEnv(t)
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "signup.bcbpsolutions.com", cfg.PublicHost)
	assert.Equal(t, 210000, cfg.PBKDF2Iterations)
}

func TestConfigLoad_MissingValkeyURL(t *testing.T) {
	setValidEnv(t)
	t.Setenv("VALKEY_URL", "")
	// mustEnv panics on empty required env var
	assert.Panics(t, func() { config.Load() }) //nolint:errcheck
}

func TestConfigLoad_MissingHMACKey(t *testing.T) {
	setValidEnv(t)
	t.Setenv("SBFF_HMAC_KEY", "")
	assert.Panics(t, func() { config.Load() }) //nolint:errcheck
}

func TestConfigLoad_MissingTurnstileSecret(t *testing.T) {
	setValidEnv(t)
	t.Setenv("SBFF_TURNSTILE_SECRET", "")
	assert.Panics(t, func() { config.Load() }) //nolint:errcheck
}

func TestConfigLoad_MissingRealmProvisionerURL(t *testing.T) {
	setValidEnv(t)
	t.Setenv("REALM_PROVISIONER_BASE_URL", "")
	assert.Panics(t, func() { config.Load() }) //nolint:errcheck
}

func TestConfigLoad_MissingOrgMembershipURL(t *testing.T) {
	setValidEnv(t)
	t.Setenv("ORG_MEMBERSHIP_BASE_URL", "")
	assert.Panics(t, func() { config.Load() }) //nolint:errcheck
}

func TestConfigLoad_EmptyPublicHost(t *testing.T) {
	setValidEnv(t)
	t.Setenv("PUBLIC_HOST", "")
	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PUBLIC_HOST")
}

func TestConfigLoad_PBKDF2TooLow(t *testing.T) {
	setValidEnv(t)
	t.Setenv("PBKDF2_ITERATIONS", "100000") // below 210000 minimum
	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PBKDF2_ITERATIONS")
}

func TestConfigLoad_HMACKeyTooShort(t *testing.T) {
	setValidEnv(t)
	t.Setenv("SBFF_HMAC_KEY", "tooshort") // < 32 bytes
	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SBFF_HMAC_KEY")
}

func TestConfigLoad_DefaultRateLimits(t *testing.T) {
	setValidEnv(t)
	cfg, err := config.Load()
	require.NoError(t, err)
	// Confirm defaults from LLD §12
	assert.Equal(t, 3, cfg.SignupsPerIPPerHour)
	assert.Equal(t, 5, cfg.AttemptsPerEmailPerDay)
	assert.Equal(t, 20, cfg.SessionsPerIPPerHour)
	assert.Equal(t, 60, cfg.PollsPerSessionPerMin)
}

func TestConfigLoad_PlansFallbackNeverTrialCode(t *testing.T) {
	setValidEnv(t)
	cfg, err := config.Load()
	require.NoError(t, err)
	// SB-INV-8: plan codes must be starter|pro|enterprise, never "trial"
	for _, p := range cfg.PlansFallback {
		assert.NotEqual(t, "trial", string(p.Code), "plan fallback must not contain 'trial'")
	}
}
