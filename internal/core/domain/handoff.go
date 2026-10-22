package domain

import "time"

// HandoffState is the state of the provisioning handoff (LLD §9.1).
type HandoffState string

const (
	StateAllocated       HandoffState = "allocated"
	StateIdentityCreated HandoffState = "identity_created"
	StateTenantPending   HandoffState = "tenant_pending"
	StateSlugConflict    HandoffState = "slug_conflict"
	StateCompleted       HandoffState = "completed"
	StateRejected        HandoffState = "rejected"
	StateFailed          HandoffState = "failed"
)

// Handoff is the ephemeral provisioning record stored in Valkey (LLD §4.2).
// It never holds the plain password, hashed seed, admin email, or CAPTCHA token.
type Handoff struct {
	SignupID       string       `json:"signup_id"` // ULID
	TenantID       string       `json:"tenant_id"` // UUIDv7
	State          HandoffState `json:"state"`
	Slug           string       `json:"slug"`
	CompanyName    string       `json:"company_name"`
	Plan           Plan         `json:"plan"`
	DefaultLocale  string       `json:"default_locale"`
	AdminEmailHMAC string       `json:"admin_email_hmac"` // HMAC-SHA256 of lower(email)
	KeycloakUserID string       `json:"keycloak_user_id,omitempty"`
	SlugAttempts   int          `json:"slug_attempts"`
	I1Attempts     int          `json:"i1_attempts"`
	LastError      string       `json:"last_error,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
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
