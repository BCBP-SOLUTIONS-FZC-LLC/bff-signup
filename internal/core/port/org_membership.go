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
}

// ProvisionTenantResponse is the I-1 201/200 response body.
type ProvisionTenantResponse struct {
	TenantID string `json:"tenant_id"`
}

// OrgMembership is the port for I-1 and the trial-eligibility check (LLD §5.6, §18.2).
type OrgMembership interface {
	// ProvisionTenant calls I-1. Idempotent on tenant_id (ON CONFLICT DO NOTHING).
	// Returns a DomainError on failure; DomainError{Cause: ErrSlugTaken} on 409 slug conflict.
	ProvisionTenant(ctx context.Context, req ProvisionTenantRequest) (*ProvisionTenantResponse, error)

	// CheckEligibility calls POST /internal/trial-eligibility (CR-4).
	// Returns nil if eligible; DomainError{Cause: ErrTrialNotEligible} if not.
	CheckEligibility(ctx context.Context, emailHash string) error
}
