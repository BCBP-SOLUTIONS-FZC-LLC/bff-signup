package main

// wiring.go isolates upstream client construction from main.go so the
// composition root stays readable. Each function returns the concrete adapter
// type; main.go assigns it to the matching port interface variable.

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/catalog"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/hasher"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/orgmembership"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/realmprovisioner"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/turnstile"
	valkeystore "github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/valkey"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

// buildBilling returns the BillingClient to use.
// When PAID_SIGNUP_ENABLED=true and BILLING_BASE_URL is set, a real HTTP adapter
// will be returned here once it is built (Phase 1.5, LLD §19). Until then, the
// stub is used regardless — the /paid route returns 404 when the flag is false.
func buildBilling(_ *config.Config) port.BillingClient {
	return port.BillingStub{}
}

// buildValkeyClient parses the VALKEY_URL and returns a connected go-redis client.
// Caller is responsible for calling rdb.Ping and rdb.Close.
func buildValkeyClient(valkeyURL string) (*redis.Client, error) {
	opts, err := redis.ParseURL(valkeyURL)
	if err != nil {
		return nil, err
	}
	return redis.NewClient(opts), nil
}

// buildValkeyStores constructs the three Valkey-backed port implementations
// (session store, handoff store, rate limiter) from a shared client.
func buildValkeyStores(rdb *redis.Client) (
	sessions port.SessionStore,
	handoffs port.HandoffStore,
	limiter port.RateLimiter,
) {
	return valkeystore.NewSessionStore(rdb),
		valkeystore.NewHandoffStore(rdb),
		valkeystore.NewRateLimiter(rdb)
}

// buildRealmProvisioner constructs the RP-1 HTTP client.
func buildRealmProvisioner(cfg *config.Config) *realmprovisioner.Client {
	return realmprovisioner.New(cfg.RealmProvisionerURL, cfg.RPTimeout())
}

// buildOrgMembership constructs the I-1 + trial-eligibility HTTP client.
func buildOrgMembership(cfg *config.Config) *orgmembership.Client {
	return orgmembership.New(cfg.OrgMembershipURL, cfg.I1Timeout())
}

// buildTurnstile constructs the Cloudflare Turnstile CAPTCHA verifier.
func buildTurnstile(cfg *config.Config) *turnstile.Client {
	return turnstile.New(cfg.TurnstileSecret, cfg.CaptchaExpectedHost, cfg.TurnstileTimeout())
}

// buildPasswordHasher constructs the PBKDF2-SHA512 hasher.
// PBKDF2_ITERATIONS is validated >= 210000 in config.Validate().
func buildPasswordHasher(cfg *config.Config) *hasher.PBKDF2Hasher {
	return hasher.New(cfg.PBKDF2Iterations)
}

// buildCatalog constructs the plan catalogue client.
// If CATALOG_BASE_URL is empty, a staticCatalog backed by plansFallback is returned
// — codes are always starter|pro|enterprise (LLD §6, §12).
func buildCatalog(cfg *config.Config) port.Catalog {
	if cfg.CatalogURL != "" {
		return catalog.New(cfg.CatalogURL, 3*time.Second, 5*time.Minute, cfg.PlansFallback)
	}
	return &staticCatalog{plans: cfg.PlansFallback}
}

// staticCatalog is the local fallback when no Catalog Service URL is configured.
type staticCatalog struct {
	plans []domain.PlanInfo
}

func (s *staticCatalog) Plans(_ context.Context) ([]domain.PlanInfo, error) {
	return s.plans, nil
}
