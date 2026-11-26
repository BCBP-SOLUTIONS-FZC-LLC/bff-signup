package port

import (
	"context"
	"errors"
)

// BillingClient is the port for Billing's internal route B-I1
// (POST /api/v1/internal/subscriptions/authorize-and-charge; Billing LLD §5.4.2,
// BL-OQ-8). The real HTTP adapter is wired when PAID_SIGNUP_ENABLED=true; until
// then BillingStub is used and the /paid route returns 404.
type BillingClient interface {
	// AuthorizeAndCharge creates a subscription and charges the given payment
	// token. Idempotent on IdempotencyKey ("charge:<tenant_id>"): every retry,
	// including after a timeout or 3-D Secure, must reuse the same key and body.
	//
	// A nil error means Billing gave a definite answer, described by
	// ChargeResponse.Outcome. Errors carry a domain sentinel:
	//   - ErrPaymentDeclined: nothing payable was created; start a fresh attempt.
	//   - ErrChargeExpired: 3-D Secure was abandoned; start a fresh attempt.
	//   - ErrDependencyUnavailable: outcome unknown or call in flight; retry with
	//     the same key and body (details may carry retry_after_seconds).
	//   - ErrValidationFailed / ErrInternal: request or caller rejected; do not retry.
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

// ChargeOutcome is the definite B-I1 result carried by a ChargeResponse.
type ChargeOutcome string

const (
	// ChargeConfirmed: 201, or 200 with Idempotent-Replayed. Payment is confirmed;
	// continue to RP-2. The zero value, so a bare ChargeResponse means confirmed.
	ChargeConfirmed ChargeOutcome = ""
	// ChargeActionRequired: 402 PAYMENT_ACTION_REQUIRED. The browser must complete
	// 3-D Secure with Action.ClientSecret, then the same call is retried.
	ChargeActionRequired ChargeOutcome = "action_required"
	// ChargeAlreadyExists: 409 SUBSCRIPTION_ALREADY_EXISTS. The tenant already has
	// a subscription under a different key or body. The BFF never provisions from
	// it: it is not this attempt's record and may be unpaid (LLD §19.4).
	ChargeAlreadyExists ChargeOutcome = "already_exists"
)

// PaymentAction is the customer action Billing needs before it can confirm.
type PaymentAction struct {
	Type         string // "3ds"
	ClientSecret string // for the provider's browser JS only; never logged or stored
}

// ChargeResponse is a definite B-I1 answer.
type ChargeResponse struct {
	Outcome               ChargeOutcome
	BillingSubscriptionID string         // stable ID forwarded to RP-2 and matched on TenantConverted
	Action                *PaymentAction // set only for ChargeActionRequired
	ExpiresAt             string         // RFC 3339; set only for ChargeActionRequired
}

// ErrNotImplemented is returned by BillingStub.
var ErrNotImplemented = errors.New("not_implemented")

// BillingStub is wired when PAID_SIGNUP_ENABLED=false (the /paid route then returns 404).
// All methods return ErrNotImplemented. The B-I1 adapter is wired when enabled.
type BillingStub struct{}

func (BillingStub) AuthorizeAndCharge(_ context.Context, _ ChargeRequest) (*ChargeResponse, error) {
	return nil, ErrNotImplemented
}
