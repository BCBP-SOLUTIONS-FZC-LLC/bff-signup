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
	BillingURL          string // Billing B-I1 base URL (LLD §19.5); required when PAID_SIGNUP_ENABLED=true

	// Direct paid signup flag (LLD §19.5): stays false until Billing go-live step 4
	PaidSignupEnabled bool // PAID_SIGNUP_ENABLED=true enables POST /paid and the Billing client

	// Timeouts
	RPTimeoutMs           int // RP-1 call timeout (RP publishes p95 ≤ 1.5 s)
	RP2TimeoutMs          int // RP-2 call timeout (RP publishes p95 ≤ 4 s: realm clone, OIDC clients, admin user)
	I1TimeoutMs           int
	EligibilityTimeoutMs  int
	TurnstileTimeoutMs    int
	RequestDeadlineMs     int
	BillingTimeoutMs      int // Billing B-I1 call timeout; >= 15000 when paid signup is on (BL-OQ-8: p99 12 s)
	PaidRequestDeadlineMs int // SB-5 request deadline: covers B-I1 + RP-2 + I-1 (BL-OQ-8)

	// Rate limits (configurable, LLD §12)
	SignupsPerIPPerHour    int
	AttemptsPerEmailPerDay int
	SessionsPerIPPerHour   int
	PollsPerSessionPerMin  int

	// CAPTCHA
	CaptchaSiteKey      string
	CaptchaExpectedHost string
	CaptchaSkip         bool // CAPTCHA_SKIP=true bypasses Turnstile (dev/test only)

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
		BillingURL:          envOr("BILLING_BASE_URL", ""),
		PaidSignupEnabled:   envBool("PAID_SIGNUP_ENABLED"),

		RPTimeoutMs:           envInt("RP_TIMEOUT_MS", 2000),
		RP2TimeoutMs:          envInt("RP2_TIMEOUT_MS", 8000),
		I1TimeoutMs:           envInt("I1_TIMEOUT_MS", 300),
		EligibilityTimeoutMs:  envInt("ELIGIBILITY_TIMEOUT_MS", 150),
		TurnstileTimeoutMs:    envInt("TURNSTILE_TIMEOUT_MS", 300),
		RequestDeadlineMs:     envInt("REQUEST_DEADLINE_MS", 5000),
		BillingTimeoutMs:      envInt("BILLING_TIMEOUT_MS", MinBillingTimeoutMs),
		PaidRequestDeadlineMs: envInt("PAID_REQUEST_DEADLINE_MS", 30000),

		SignupsPerIPPerHour:    envInt("RATE_SIGNUPS_PER_IP_PER_HOUR", 3),
		AttemptsPerEmailPerDay: envInt("RATE_ATTEMPTS_PER_EMAIL_PER_DAY", 5),
		SessionsPerIPPerHour:   envInt("RATE_SESSIONS_PER_IP_PER_HOUR", 20),
		PollsPerSessionPerMin:  envInt("RATE_POLLS_PER_SESSION_PER_MIN", 60),

		CaptchaSiteKey:      mustEnv("CAPTCHA_SITE_KEY"),
		CaptchaExpectedHost: envOr("CAPTCHA_EXPECTED_HOSTNAME", "signup.bcbpsolutions.com"),
		CaptchaSkip:         envBool("CAPTCHA_SKIP"),

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
	if c.PaidSignupEnabled && c.BillingURL == "" {
		return fmt.Errorf("BILLING_BASE_URL must not be empty when PAID_SIGNUP_ENABLED=true")
	}
	if c.PaidSignupEnabled {
		if c.BillingTimeoutMs < MinBillingTimeoutMs {
			return fmt.Errorf("BILLING_TIMEOUT_MS must be >= %d when PAID_SIGNUP_ENABLED=true (got %d)", MinBillingTimeoutMs, c.BillingTimeoutMs)
		}
		if c.RP2TimeoutMs < 100 {
			return fmt.Errorf("RP2_TIMEOUT_MS must be >= 100ms (got %d)", c.RP2TimeoutMs)
		}
		// SB-5 chain: B-I1 → PUB-1 (I1 timeout) → RP-2 → I-1.
		if need := c.BillingTimeoutMs + c.I1TimeoutMs + c.RP2TimeoutMs + c.I1TimeoutMs; c.PaidRequestDeadlineMs < need {
			return fmt.Errorf("PAID_REQUEST_DEADLINE_MS must be >= BILLING_TIMEOUT_MS + RP2_TIMEOUT_MS + 2*I1_TIMEOUT_MS (%d) (got %d)", need, c.PaidRequestDeadlineMs)
		}
	}
	if c.CaptchaSkip && c.AppEnv != "dev" && c.AppEnv != "test" && c.AppEnv != "local" {
		return fmt.Errorf("CAPTCHA_SKIP must not be enabled outside dev/test/local environments (APP_ENV=%s)", c.AppEnv)
	}
	if c.RPTimeoutMs < 100 {
		return fmt.Errorf("RP_TIMEOUT_MS must be >= 100ms (got %d)", c.RPTimeoutMs)
	}
	if c.I1TimeoutMs < 50 {
		return fmt.Errorf("I1_TIMEOUT_MS must be >= 50ms (got %d)", c.I1TimeoutMs)
	}
	if c.EligibilityTimeoutMs < 50 {
		return fmt.Errorf("ELIGIBILITY_TIMEOUT_MS must be >= 50ms (got %d)", c.EligibilityTimeoutMs)
	}
	if c.RequestDeadlineMs < 500 {
		return fmt.Errorf("REQUEST_DEADLINE_MS must be >= 500ms (got %d)", c.RequestDeadlineMs)
	}
	// SB-2 chain without retries: eligibility → PUB-1 (I1 timeout) → RP-1 → I-1.
	if need := c.EligibilityTimeoutMs + c.I1TimeoutMs + c.RPTimeoutMs + c.I1TimeoutMs; c.RequestDeadlineMs < need {
		return fmt.Errorf("REQUEST_DEADLINE_MS must be >= ELIGIBILITY_TIMEOUT_MS + RP_TIMEOUT_MS + 2*I1_TIMEOUT_MS (%d) (got %d)", need, c.RequestDeadlineMs)
	}
	if c.SessionTTLSeconds < 300 {
		return fmt.Errorf("SESSION_TTL_SECONDS must be >= 300 seconds (got %d)", c.SessionTTLSeconds)
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
func (c *Config) RPTimeout() time.Duration  { return ms(c.RPTimeoutMs) }
func (c *Config) RP2Timeout() time.Duration { return ms(c.RP2TimeoutMs) }

// HandoffLockTTL is how long the per-signup handoff lock lives. It must outlast
// the longest request that holds it (SB-5 waits on Billing and RP-2), or a
// second request could proceed mid-payment: longest deadline + 5 s, at least 15 s.
func (c *Config) HandoffLockTTL() time.Duration {
	ttl := c.MaxRequestDeadline() + 5*time.Second
	if ttl < 15*time.Second {
		ttl = 15 * time.Second
	}
	return ttl
}
func (c *Config) I1Timeout() time.Duration           { return ms(c.I1TimeoutMs) }
func (c *Config) EligibilityTimeout() time.Duration  { return ms(c.EligibilityTimeoutMs) }
func (c *Config) TurnstileTimeout() time.Duration    { return ms(c.TurnstileTimeoutMs) }
func (c *Config) RequestDeadline() time.Duration     { return ms(c.RequestDeadlineMs) }
func (c *Config) BillingTimeout() time.Duration      { return ms(c.BillingTimeoutMs) }
func (c *Config) PaidRequestDeadline() time.Duration { return ms(c.PaidRequestDeadlineMs) }

// MinBillingTimeoutMs is the lowest BILLING_TIMEOUT_MS accepted with paid signup
// on. Billing B-I1 makes two to four provider calls: p95 6 s, p99 12 s (BL-OQ-8).
const MinBillingTimeoutMs = 15000

// MaxRequestDeadline is the longest handler deadline in use: REQUEST_DEADLINE_MS,
// or PAID_REQUEST_DEADLINE_MS when paid signup is on and it is longer. It is
// gincommon's outer RequestTimeout: an outer deadline shorter than SB-5's would
// cut SB-5 off, because each handler's deadline derives from the request context.
func (c *Config) MaxRequestDeadline() time.Duration {
	longest := c.RequestDeadlineMs
	if c.PaidSignupEnabled && c.PaidRequestDeadlineMs > longest {
		longest = c.PaidRequestDeadlineMs
	}
	return ms(longest)
}

// ServerWriteTimeout is the API server's WriteTimeout: the longest handler
// deadline plus 500 ms to write the response.
func (c *Config) ServerWriteTimeout() time.Duration {
	return c.MaxRequestDeadline() + 500*time.Millisecond
}

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

func envBool(key string) bool {
	return strings.EqualFold(os.Getenv(key), "true")
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
