package domain

import "time"

// HandoffState is the state of the provisioning handoff (LLD §9.1).
type HandoffState string

const (
	StateAllocated             HandoffState = "allocated"
	StatePaymentPending        HandoffState = "payment_pending"         // SB-5 (LLD §19): Billing call in flight, or outcome unknown
	StatePaymentActionRequired HandoffState = "payment_action_required" // BL-OQ-8: Billing 402 PAYMENT_ACTION_REQUIRED; browser runs 3-D Secure, then re-POSTs SB-5
	StatePaymentConfirmed      HandoffState = "payment_confirmed"       // SB-5 (LLD §19): paid; RP-2 not done yet
	StateIdentityCreated       HandoffState = "identity_created"
	StateTenantPending         HandoffState = "tenant_pending"
	StateSlugConflict          HandoffState = "slug_conflict"
	StateCompleted             HandoffState = "completed"
	StateRejected              HandoffState = "rejected"
	StateFailed                HandoffState = "failed"
)

// SignupType distinguishes trial (SB-2) from direct-paid (SB-5, LLD §19) signups.
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
	BillingSubscriptionID string       `json:"billing_subscription_id,omitempty"` // SB-5 (LLD §19)
	// PaymentRefHMAC is HMAC-SHA256 of the browser payment_method_ref. It lets a
	// resumed SB-5 prove it sends Billing the same body without storing the token.
	PaymentRefHMAC string `json:"payment_ref_hmac,omitempty"`
	// RP2Attempted (SB-5): RP-2 was called with signup:<tenant_id>:rp2 and is not known to
	// have failed. RP replays by key, so slug, company name and email are frozen.
	RP2Attempted  bool      `json:"rp2_attempted,omitempty"`
	LicensedSeats int       `json:"licensed_seats,omitempty"` // 0 for trial; seat count for paid (I-1)
	SlugAttempts  int       `json:"slug_attempts"`
	I1Attempts    int       `json:"i1_attempts"`
	LastError     string    `json:"last_error,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// IsTerminal returns true if the state has no further transitions.
func (h *Handoff) IsTerminal() bool {
	switch h.State { //nolint:exhaustive
	case StateCompleted, StateRejected, StateFailed:
		return true
	}
	return false
}

// BlocksOtherSignupType reports whether this handoff is an attempt of the other
// signup type (trial vs paid) that a new submit on the same session must not
// overwrite. Only a rejected or empty handoff may be replaced.
func (h *Handoff) BlocksOtherSignupType(want SignupType) bool {
	if h == nil || h.State == "" || h.State == StateRejected {
		return false
	}
	have := h.Type
	if have == "" {
		have = SignupTypeTrial // handoffs written before Type existed are trial
	}
	return have != want
}

// IsPaymentResumable returns true when a repeat SB-5 must call Billing again with
// the same tenant_id, Idempotency-Key and body (BL-OQ-8: retries reuse the key).
func (h *Handoff) IsPaymentResumable() bool {
	return h.Type == SignupTypePaid &&
		(h.State == StatePaymentPending || h.State == StatePaymentActionRequired)
}

// CanResume returns true if SB-3 may attempt I-1 for this handoff.
func (h *Handoff) CanResume() bool {
	return h.State == StateTenantPending
}

// CanChangeSlug returns true if SB-4 is a valid action for this handoff.
func (h *Handoff) CanChangeSlug() bool {
	return h.State == StateSlugConflict
}
