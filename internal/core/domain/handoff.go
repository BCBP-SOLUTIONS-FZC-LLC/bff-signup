package domain

import "time"

// HandoffState is the state of the provisioning handoff (LLD §9.1).
type HandoffState string

const (
	StateAllocated        HandoffState = "allocated"
	StatePaymentPending   HandoffState = "payment_pending"   // Phase 1.5 (LLD §19): Billing call in flight
	StatePaymentConfirmed HandoffState = "payment_confirmed" // Phase 1.5 (LLD §19): billing_subscription_id obtained
	StateIdentityCreated  HandoffState = "identity_created"
	StateTenantPending    HandoffState = "tenant_pending"
	StateSlugConflict     HandoffState = "slug_conflict"
	StateCompleted        HandoffState = "completed"
	StateRejected         HandoffState = "rejected"
	StateFailed           HandoffState = "failed"
)

// SignupType distinguishes trial from direct-paid signup paths (Phase 1.5, LLD §19).
type SignupType string

const (
	SignupTypeTrial SignupType = "trial"
	SignupTypePaid  SignupType = "paid"
)

// Handoff is the ephemeral provisioning record stored in Valkey (LLD §4.2).
// It never holds the plain password, hashed seed, admin email, or CAPTCHA token.
type Handoff struct {
	SignupID              string       `json:"signup_id"` // ULID
	TenantID              string       `json:"tenant_id"` // UUIDv7
	Type                  SignupType   `json:"type"`      // "trial" | "paid"
	State                 HandoffState `json:"state"`
	Slug                  string       `json:"slug"`
	CompanyName           string       `json:"company_name"`
	Plan                  Plan         `json:"plan"`
	DefaultLocale         string       `json:"default_locale"`
	AdminEmailHMAC        string       `json:"admin_email_hmac"` // HMAC-SHA256 of lower(email)
	KeycloakUserID        string       `json:"keycloak_user_id,omitempty"`
	BillingSubscriptionID string       `json:"billing_subscription_id,omitempty"` // Phase 1.5 (LLD §19)
	LicensedSeats         int          `json:"licensed_seats,omitempty"`          // 0 for trial; seat count for paid (I-1)
	SlugAttempts          int          `json:"slug_attempts"`
	I1Attempts            int          `json:"i1_attempts"`
	LastError             string       `json:"last_error,omitempty"`
	CreatedAt             time.Time    `json:"created_at"`
	UpdatedAt             time.Time    `json:"updated_at"`
}

// IsTerminal returns true if the state has no further transitions.
func (h *Handoff) IsTerminal() bool {
	switch h.State { //nolint:exhaustive
	case StateCompleted, StateRejected, StateFailed:
		return true
	}
	return false
}

// CanResume returns true if SB-3 may attempt I-1 for this handoff.
func (h *Handoff) CanResume() bool {
	return h.State == StateTenantPending
}

// CanChangeSlug returns true if SB-4 is a valid action for this handoff.
func (h *Handoff) CanChangeSlug() bool {
	return h.State == StateSlugConflict
}
