package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
)

// Config holds all service configuration loaded from environment variables.
// Fail-fast validation runs in Validate() before any connection is opened (LLD §12).
type Config struct {
	// App
	AppEnv      string
	AppPort     string
	MetricsPort string

	// Public host (signup.bcbpsolutions.com)
	PublicHost string

	// Valkey
	ValkeyURL string

	// Secrets
	HMACKey         string // SBFF_HMAC_KEY — used to HMAC email addresses
	TurnstileSecret string // SBFF_TURNSTILE_SECRET

	// Upstream services
	RealmProvisionerURL string
	OrgMembershipURL    string
	CatalogURL          string

	// Timeouts
	RPTimeoutMs          int
	I1TimeoutMs          int
	EligibilityTimeoutMs int
	TurnstileTimeoutMs   int
	RequestDeadlineMs    int

	// Rate limits (configurable, LLD §12)
	SignupsPerIPPerHour    int
	AttemptsPerEmailPerDay int
	SessionsPerIPPerHour   int
	PollsPerSessionPerMin  int

	// CAPTCHA
	CaptchaSiteKey      string
	CaptchaExpectedHost string

	// Password
	PBKDF2Iterations int

	// Slug
	ReservedSlugs map[string]bool

	// Session
	SessionTTLSeconds int

	// Plans fallback (LLD §12)
	PlansFallback []domain.PlanInfo

	// Terms
	CurrentTermsVersion string
}

// Load reads all configuration from environment variables.
func Load() (*Config, error) {
	c := &Config{
		AppEnv:      envOr("APP_ENV", "dev"),
		AppPort:     envOr("APP_PORT", "8080"),
		MetricsPort: envOr("METRICS_PORT", "9090"),

		PublicHost: os.Getenv("PUBLIC_HOST"), // required — validated in Validate()

		ValkeyURL:       mustEnv("VALKEY_URL"),
		HMACKey:         mustEnv("SBFF_HMAC_KEY"),
		TurnstileSecret: mustEnv("SBFF_TURNSTILE_SECRET"),

		RealmProvisionerURL: mustEnv("REALM_PROVISIONER_BASE_URL"),
		OrgMembershipURL:    mustEnv("ORG_MEMBERSHIP_BASE_URL"),
		CatalogURL:          envOr("CATALOG_BASE_URL", ""),

		RPTimeoutMs:          envInt("RP_TIMEOUT_MS", 900),
		I1TimeoutMs:          envInt("I1_TIMEOUT_MS", 300),
		EligibilityTimeoutMs: envInt("ELIGIBILITY_TIMEOUT_MS", 150),
		TurnstileTimeoutMs:   envInt("TURNSTILE_TIMEOUT_MS", 300),
		RequestDeadlineMs:    envInt("REQUEST_DEADLINE_MS", 2500),

		SignupsPerIPPerHour:    envInt("RATE_SIGNUPS_PER_IP_PER_HOUR", 3),
		AttemptsPerEmailPerDay: envInt("RATE_ATTEMPTS_PER_EMAIL_PER_DAY", 5),
		SessionsPerIPPerHour:   envInt("RATE_SESSIONS_PER_IP_PER_HOUR", 20),
		PollsPerSessionPerMin:  envInt("RATE_POLLS_PER_SESSION_PER_MIN", 60),

		CaptchaSiteKey:      mustEnv("CAPTCHA_SITE_KEY"),
		CaptchaExpectedHost: envOr("CAPTCHA_EXPECTED_HOSTNAME", "signup.bcbpsolutions.com"),

		PBKDF2Iterations: envInt("PBKDF2_ITERATIONS", 210000),

		SessionTTLSeconds:   envInt("SESSION_TTL_SECONDS", 1800),
		CurrentTermsVersion: envOr("TERMS_CURRENT_VERSION", "2026-09"),
	}

	// Reserved slug list
	reserved := envOr("SLUG_RESERVED", "admin,api,app,auth,billing,help,login,signup,status,support,trial,www,mail,static,cdn,internal,system,root,tenant,keycloak")
	c.ReservedSlugs = parseReserved(reserved)

	// Plans fallback (LLD §12 — codes must be exactly starter|pro|enterprise)
	c.PlansFallback = []domain.PlanInfo{
		{Code: domain.PlanStarter, Name: "Starter", TrialDays: 14},
		{Code: domain.PlanPro, Name: "Pro", TrialDays: 14},
		{Code: domain.PlanEnterprise, Name: "Enterprise", TrialDays: 14},
	}

	return c, c.Validate()
}

// Validate performs fail-fast checks before any connections are opened.
func (c *Config) Validate() error {
	if c.PublicHost == "" {
		return fmt.Errorf("PUBLIC_HOST must not be empty")
	}
	if c.PBKDF2Iterations < 210000 {
		return fmt.Errorf("PBKDF2_ITERATIONS must be >= 210000 (got %d)", c.PBKDF2Iterations)
	}
	if len(c.HMACKey) < 32 {
		return fmt.Errorf("SBFF_HMAC_KEY must be at least 32 bytes")
	}
	if c.ValkeyURL == "" {
		return fmt.Errorf("VALKEY_URL must not be empty")
	}
	if c.RealmProvisionerURL == "" {
		return fmt.Errorf("REALM_PROVISIONER_BASE_URL must not be empty")
	}
	if c.OrgMembershipURL == "" {
		return fmt.Errorf("ORG_MEMBERSHIP_BASE_URL must not be empty")
	}
	return nil
}

// PasswordPolicy returns the domain.PasswordPolicy derived from config.
func (c *Config) PasswordPolicy() domain.PasswordPolicy {
	return domain.PasswordPolicy{
		MinLength:      12,
		MaxLength:      128,
		RequireClasses: 3,
		NotEmail:       true,
	}
}

// SlugRules returns the domain.SlugRules for the SB-1 response.
func (c *Config) SlugRules() domain.SlugRules {
	return domain.SlugRules{
		Pattern: `^[a-z0-9](?:[a-z0-9-]{1,28}[a-z0-9])$`,
		Min:     3,
		Max:     30,
	}
}

// Timeout helpers
func (c *Config) RPTimeout() time.Duration          { return ms(c.RPTimeoutMs) }
func (c *Config) I1Timeout() time.Duration          { return ms(c.I1TimeoutMs) }
func (c *Config) EligibilityTimeout() time.Duration { return ms(c.EligibilityTimeoutMs) }
func (c *Config) TurnstileTimeout() time.Duration   { return ms(c.TurnstileTimeoutMs) }
func (c *Config) RequestDeadline() time.Duration    { return ms(c.RequestDeadlineMs) }

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

// ── helpers ───────────────────────────────────────────────────────────────────

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required environment variable %s is not set", key))
	}
	return v
}

func envInt(key string, def int) int {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func parseReserved(s string) map[string]bool {
	m := make(map[string]bool)
	for _, word := range strings.Split(s, ",") {
		word = strings.TrimSpace(word)
		if word != "" {
			m[word] = true
		}
	}
	return m
}
