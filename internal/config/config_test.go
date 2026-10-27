package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setRequiredEnvVars(t *testing.T) {
	t.Helper()
	t.Setenv("VALKEY_URL", "redis://localhost:6379/0")
	t.Setenv("SBFF_HMAC_KEY", "test-hmac-key-32-bytes-minimum!!")
	t.Setenv("SBFF_TURNSTILE_SECRET", "test-secret")
	t.Setenv("REALM_PROVISIONER_BASE_URL", "http://rp.example.com")
	t.Setenv("ORG_MEMBERSHIP_BASE_URL", "http://om.example.com")
	t.Setenv("CAPTCHA_SITE_KEY", "test-site-key")
	t.Setenv("PUBLIC_HOST", "signup.example.com")
}

func TestLoad_WithRequiredEnvVars(t *testing.T) {
	setRequiredEnvVars(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "signup.example.com", cfg.PublicHost)
	assert.Equal(t, "redis://localhost:6379/0", cfg.ValkeyURL)
}

func TestConfig_PasswordPolicy(t *testing.T) {
	cfg := &Config{}
	pol := cfg.PasswordPolicy()
	assert.Equal(t, 12, pol.MinLength)
	assert.Equal(t, 128, pol.MaxLength)
	assert.Equal(t, 3, pol.RequireClasses)
	assert.True(t, pol.NotEmail)
}

func TestConfig_SlugRules(t *testing.T) {
	cfg := &Config{}
	rules := cfg.SlugRules()
	assert.Equal(t, 3, rules.Min)
	assert.Equal(t, 30, rules.Max)
	assert.NotEmpty(t, rules.Pattern)
}

func TestConfig_TimeoutHelpers(t *testing.T) {
	cfg := &Config{
		RPTimeoutMs:          2000,
		I1TimeoutMs:          1500,
		EligibilityTimeoutMs: 1000,
		TurnstileTimeoutMs:   3000,
		RequestDeadlineMs:    2500,
		BillingTimeoutMs:     5000,
	}
	assert.Equal(t, 2000*time.Millisecond, cfg.RPTimeout())
	assert.Equal(t, 1500*time.Millisecond, cfg.I1Timeout())
	assert.Equal(t, 1000*time.Millisecond, cfg.EligibilityTimeout())
	assert.Equal(t, 3000*time.Millisecond, cfg.TurnstileTimeout())
	assert.Equal(t, 2500*time.Millisecond, cfg.RequestDeadline())
	assert.Equal(t, 5000*time.Millisecond, cfg.BillingTimeout())
}

func validConfig() *Config {
	return &Config{
		PublicHost:          "signup.example.com",
		PBKDF2Iterations:    210000,
		HMACKey:             "test-hmac-key-32-bytes-minimum!!",
		ValkeyURL:           "redis://localhost:6379/0",
		RealmProvisionerURL: "http://rp.example.com",
		OrgMembershipURL:    "http://om.example.com",
	}
}

func TestConfig_Validate_HappyPath(t *testing.T) {
	err := validConfig().Validate()
	require.NoError(t, err)
}

func TestConfig_Validate_MissingOrgMembershipURL(t *testing.T) {
	cfg := validConfig()
	cfg.OrgMembershipURL = ""
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ORG_MEMBERSHIP_BASE_URL")
}

func TestConfig_Validate_WeakHMACKey(t *testing.T) {
	cfg := validConfig()
	cfg.HMACKey = "short"
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SBFF_HMAC_KEY")
}

func TestConfig_Validate_MissingPublicHost(t *testing.T) {
	cfg := validConfig()
	cfg.PublicHost = ""
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PUBLIC_HOST")
}

func TestConfig_Validate_LowPBKDF2Iterations(t *testing.T) {
	cfg := validConfig()
	cfg.PBKDF2Iterations = 100000
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PBKDF2_ITERATIONS")
}

func TestConfig_Validate_MissingValkeyURL(t *testing.T) {
	cfg := validConfig()
	cfg.ValkeyURL = ""
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "VALKEY_URL")
}

func TestConfig_Validate_MissingRealmProvisionerURL(t *testing.T) {
	cfg := validConfig()
	cfg.RealmProvisionerURL = ""
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "REALM_PROVISIONER_BASE_URL")
}

// TestEnvOr_ReturnsDefault exercises the envOr default branch.
func TestEnvOr_ReturnsDefault(t *testing.T) {
	t.Setenv("TEST_ENV_OR_KEY_XYZ", "")
	result := envOr("TEST_ENV_OR_KEY_XYZ", "default-val")
	assert.Equal(t, "default-val", result)
}

// TestEnvOr_ReturnsEnvValue exercises the envOr env branch.
func TestEnvOr_ReturnsEnvValue(t *testing.T) {
	t.Setenv("TEST_ENV_OR_KEY_ABC", "set-value")
	result := envOr("TEST_ENV_OR_KEY_ABC", "default-val")
	assert.Equal(t, "set-value", result)
}

// TestEnvInt_InvalidValue exercises the envInt parse-error branch.
func TestEnvInt_InvalidValue(t *testing.T) {
	t.Setenv("TEST_ENV_INT_KEY", "not-an-int")
	result := envInt("TEST_ENV_INT_KEY", 42)
	assert.Equal(t, 42, result)
}
