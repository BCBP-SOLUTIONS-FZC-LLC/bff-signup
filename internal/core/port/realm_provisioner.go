package port

import "context"

// TermsAcceptance is sent on every RP-1 call so Realm Provisioner can write a durable
// legal.terms.accepted audit record (A-4, LLD §5.6, §10.6).
type TermsAcceptance struct {
	Version    string `json:"version"`     // e.g. "2026-09"
	AcceptedAt string `json:"accepted_at"` // RFC 3339 UTC — server time BFF validated accept_terms
	IPAddress  string `json:"ip_address"`  // end-user IP from trusted proxy headers
	UserAgent  string `json:"user_agent"`  // browser User-Agent, max 512 bytes
}

// ProvisionTrialRequest is the RP-1 request body (LLD §5.6).
type ProvisionTrialRequest struct {
	TenantID           string          `json:"tenant_id"`
	TenantName         string          `json:"tenant_name"`
	TenantSlug         string          `json:"tenant_slug"`
	AdminEmail         string          `json:"admin_email"`
	AdminName          string          `json:"admin_name"`
	HashedPasswordSeed string          `json:"hashed_password_seed"` // JSON-serialised HashedPasswordSeed
	Plan               string          `json:"plan"`                 // starter | pro | enterprise
	TermsAcceptance    TermsAcceptance `json:"terms_acceptance"`
}

// ProvisionTrialResponse is the RP-1 201 response body.
type ProvisionTrialResponse struct {
	TenantID              string `json:"tenant_id"`
	Realm                 string `json:"realm"`
	Strategy              string `json:"strategy"`
	KeycloakUserID        string `json:"keycloak_user_id"`
	EmailVerificationSent bool   `json:"email_verification_sent"`
}

// ProvisionDirectRequest is the RP-2 request body for direct paid signup (Phase 1.5, LLD §19).
// Fields mirror RP-1 but add BillingSubscriptionID and target a dedicated realm from day one.
// Confirm exact field set against RP LLD §5.3 when building Phase 1.5.
type ProvisionDirectRequest struct {
	TenantID              string          `json:"tenant_id"`
	TenantName            string          `json:"tenant_name"`
	TenantSlug            string          `json:"tenant_slug"`
	AdminEmail            string          `json:"admin_email"`
	AdminName             string          `json:"admin_name"`
	HashedPasswordSeed    string          `json:"hashed_password_seed"`
	Plan                  string          `json:"plan"`
	BillingSubscriptionID string          `json:"billing_subscription_id"`
	TermsAcceptance       TermsAcceptance `json:"terms_acceptance"`
}

// ProvisionDirectResponse is the RP-2 201 response body.
type ProvisionDirectResponse struct {
	TenantID              string `json:"tenant_id"`
	Realm                 string `json:"realm"`
	KeycloakUserID        string `json:"keycloak_user_id"`
	EmailVerificationSent bool   `json:"email_verification_sent"`
}

// RealmProvisioner is the port for RP-1 and RP-2 (LLD §5.6, §18.1, §19).
type RealmProvisioner interface {
	// ProvisionTrial calls RP-1. idempotencyKey is stable across retries
	// (format: "signup:<tenantID>:rp1"). Returns a DomainError on failure.
	ProvisionTrial(ctx context.Context, req ProvisionTrialRequest, idempotencyKey string) (*ProvisionTrialResponse, error)

	// ProvisionDirect calls RP-2 for direct paid signup (Phase 1.5, LLD §19).
	// idempotencyKey format: "signup:<tenantID>:rp2".
	// The real adapter is wired when PAID_SIGNUP_ENABLED=true.
	ProvisionDirect(ctx context.Context, req ProvisionDirectRequest, idempotencyKey string) (*ProvisionDirectResponse, error)
}
