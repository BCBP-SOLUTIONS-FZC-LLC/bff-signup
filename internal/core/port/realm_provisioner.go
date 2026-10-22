package port

import "context"

// ProvisionTrialRequest is the RP-1 request body (LLD §5.6).
type ProvisionTrialRequest struct {
	TenantID           string `json:"tenant_id"`
	TenantName         string `json:"tenant_name"`
	TenantSlug         string `json:"tenant_slug"`
	AdminEmail         string `json:"admin_email"`
	AdminName          string `json:"admin_name"`
	HashedPasswordSeed string `json:"hashed_password_seed"` // JSON-serialised HashedPasswordSeed
	Plan               string `json:"plan"`                 // starter | pro | enterprise
}

// ProvisionTrialResponse is the RP-1 201 response body.
type ProvisionTrialResponse struct {
	TenantID              string `json:"tenant_id"`
	Realm                 string `json:"realm"`
	Strategy              string `json:"strategy"`
	KeycloakUserID        string `json:"keycloak_user_id"`
	EmailVerificationSent bool   `json:"email_verification_sent"`
}

// RealmProvisioner is the port for RP-1 (LLD §5.6, §18.1).
type RealmProvisioner interface {
	// ProvisionTrial calls RP-1. idempotencyKey is stable across retries
	// (format: "signup:<tenantID>:rp1"). Returns a DomainError on failure.
	ProvisionTrial(ctx context.Context, req ProvisionTrialRequest, idempotencyKey string) (*ProvisionTrialResponse, error)
}
