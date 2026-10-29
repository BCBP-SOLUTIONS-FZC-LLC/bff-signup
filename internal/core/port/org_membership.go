package port

import "context"

// ProvisionTenantRequest is the I-1 request body (LLD §5.6, C-1).
type ProvisionTenantRequest struct {
	TenantID      string `json:"tenant_id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Plan          string `json:"plan"`          // starter | pro | enterprise (never "trial")
	OwnerUserID   string `json:"owner_user_id"` // keycloak_user_id from RP-1 (SB-INV-4)
	DefaultLocale string `json:"default_locale"`
	LicensedSeats int    `json:"licensed_seats"` // 0 for trial (plan-controlled); seat count for paid
	// EmailHash is the HMAC-SHA256 hex of lower(admin_email). O&M writes it atomically
	// into trial_signup_ledger inside the I-1 transaction (CR-4). Omitempty — backstop
	// callers (RP's TenantIdentityProvisioned consumer) legitimately omit it.
	EmailHash string `json:"email_hash,omitempty"`
}

// ProvisionTenantResponse is the I-1 201/200 response body.
// O&M's TenantResponse uses json tag "id" (not "tenant_id") for the tenant UUID.
type ProvisionTenantResponse struct {
	TenantID string `json:"id"`
}

// OrgMembership is the port for I-1, trial-eligibility, and the PUB-1 slug check (LLD §5.6, §18.2).
type OrgMembership interface {
	// ProvisionTenant calls I-1. Idempotent on tenant_id (ON CONFLICT DO NOTHING).
	// Returns a DomainError on failure; DomainError{Cause: ErrSlugTaken} on 409 slug conflict.
	ProvisionTenant(ctx context.Context, req ProvisionTenantRequest) (*ProvisionTenantResponse, error)

	// CheckEligibility calls POST /internal/trial-eligibility (CR-4).
	// Returns nil if eligible; DomainError{Cause: ErrTrialNotEligible} if not.
	CheckEligibility(ctx context.Context, emailHash string) error

	// CheckSlugFree calls PUB-1 (GET /api/v1/public/tenants/resolve?identifier=<slug>).
	// Returns nil if the slug is free or if PUB-1 is unreachable (fail-open — proceed to RP-1).
	// Returns DomainError{Cause: ErrSlugTaken} only when found: true is confirmed.
	CheckSlugFree(ctx context.Context, slug string) error
}
