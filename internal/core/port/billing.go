package port

import (
	"context"
	"errors"
)

// BillingClient is the port for the Billing Service (Phase 1.5, LLD §19).
// The real HTTP adapter is wired when PAID_SIGNUP_ENABLED=true; until then
// BillingStub is used and the /paid route returns 404.
type BillingClient interface {
	// AuthorizeAndCharge creates a Stripe subscription and charges the given
	// payment token. Blocks until Stripe confirms payment and returns the
	// billing_subscription_id needed by RP-2. Idempotent on IdempotencyKey
	// (format: "charge:<tenant_id>").
	AuthorizeAndCharge(ctx context.Context, req ChargeRequest) (*ChargeResponse, error)
}

// ChargeRequest is the payload sent to Billing for a direct paid signup.
type ChargeRequest struct {
	TenantID       string // pre-allocated by BFF (SB-INV-3)
	PaymentToken   string // Stripe payment-method token, tokenised in the browser — no PAN reaches the BFF
	Plan           string // starter | pro | enterprise
	Seats          int    // initial seat count
	IdempotencyKey string // "charge:<tenant_id>"
}

// ChargeResponse is the Billing response after a confirmed Stripe charge.
type ChargeResponse struct {
	BillingSubscriptionID string // stable ID forwarded to RP-2 and matched on TenantConverted
}

// ErrNotImplemented is returned by BillingStub in Phase 1.
var ErrNotImplemented = errors.New("not_implemented")

// BillingStub is the Phase 1 placeholder. All methods return ErrNotImplemented.
// Swap for the real HTTP adapter when the Billing service contract is finalised (LLD §19).
type BillingStub struct{}

func (BillingStub) AuthorizeAndCharge(_ context.Context, _ ChargeRequest) (*ChargeResponse, error) {
	return nil, ErrNotImplemented
}
