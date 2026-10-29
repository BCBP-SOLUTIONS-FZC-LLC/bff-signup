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
		// Timeouts and session TTL must satisfy the bounds enforced by Validate().
		RPTimeoutMs:          900,
		I1TimeoutMs:          300,
		EligibilityTimeoutMs: 150,
		RequestDeadlineMs:    2500,
		SessionTTLSeconds:    1800,
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

// TestConfig_Validate_PaidEnabledWithoutBillingURL exercises the M-1 guard:
// PAID_SIGNUP_ENABLED=true requires BILLING_BASE_URL.
func TestConfig_Validate_PaidEnabledWithoutBillingURL(t *testing.T) {
	cfg := validConfig()
	cfg.PaidSignupEnabled = true
	cfg.BillingURL = ""
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "BILLING_BASE_URL")
}

// TestConfig_Validate_PaidEnabledWithBillingURL passes when both flags are set.
func TestConfig_Validate_PaidEnabledWithBillingURL(t *testing.T) {
	cfg := validConfig()
	cfg.PaidSignupEnabled = true
	cfg.BillingURL = "http://billing.internal"
	err := cfg.Validate()
	require.NoError(t, err)
}

// TestConfig_Validate_CaptchaSkipBlockedInProd verifies CAPTCHA_SKIP is rejected
// when APP_ENV is not dev/test/local (security guard against accidental prod bypass).
func TestConfig_Validate_CaptchaSkipBlockedInProd(t *testing.T) {
	for _, env := range []string{"prod", "staging", "production"} {
		cfg := validConfig()
		cfg.CaptchaSkip = true
		cfg.AppEnv = env
		err := cfg.Validate()
		require.Error(t, err, "CAPTCHA_SKIP must be rejected in APP_ENV=%s", env)
		assert.Contains(t, err.Error(), "CAPTCHA_SKIP")
	}
}

// TestConfig_Validate_CaptchaSkipAllowedInDev verifies CAPTCHA_SKIP is allowed
// in dev/test/local environments (local curl support).
func TestConfig_Validate_CaptchaSkipAllowedInDev(t *testing.T) {
	for _, env := range []string{"dev", "test", "local"} {
		cfg := validConfig()
		cfg.CaptchaSkip = true
		cfg.AppEnv = env
		err := cfg.Validate()
		require.NoError(t, err, "CAPTCHA_SKIP must be allowed in APP_ENV=%s", env)
	}
}

// TestConfig_Validate_TimeoutBounds verifies that near-zero timeouts are rejected
// at startup before any connections are opened.
func TestConfig_Validate_TimeoutBounds(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"RPTimeoutMs=0", func(c *Config) { c.RPTimeoutMs = 0 }, "RP_TIMEOUT_MS"},
		{"RPTimeoutMs=99", func(c *Config) { c.RPTimeoutMs = 99 }, "RP_TIMEOUT_MS"},
		{"I1TimeoutMs=0", func(c *Config) { c.I1TimeoutMs = 0 }, "I1_TIMEOUT_MS"},
		{"I1TimeoutMs=49", func(c *Config) { c.I1TimeoutMs = 49 }, "I1_TIMEOUT_MS"},
		{"EligibilityTimeoutMs=0", func(c *Config) { c.EligibilityTimeoutMs = 0 }, "ELIGIBILITY_TIMEOUT_MS"},
		{"RequestDeadlineMs=0", func(c *Config) { c.RequestDeadlineMs = 0 }, "REQUEST_DEADLINE_MS"},
		{"RequestDeadlineMs=499", func(c *Config) { c.RequestDeadlineMs = 499 }, "REQUEST_DEADLINE_MS"},
		{"SessionTTLSeconds=0", func(c *Config) { c.SessionTTLSeconds = 0 }, "SESSION_TTL_SECONDS"},
		{"SessionTTLSeconds=299", func(c *Config) { c.SessionTTLSeconds = 299 }, "SESSION_TTL_SECONDS"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			tc.mutate(cfg)
			err := cfg.Validate()
			require.Error(t, err, "%s must be rejected", tc.name)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
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
