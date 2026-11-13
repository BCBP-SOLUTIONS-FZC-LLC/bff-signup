package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/metrics"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

const maxUpstreamBodyBytes = 64 * 1024

// authorizeAndChargePath is the Billing service endpoint (Commercial HLD §8.3, §10.6).
// Confirm exact path against the Billing LLD once published (SGN-2).
const authorizeAndChargePath = "/api/v1/internal/subscriptions/authorize-and-charge"

const systemPrincipal = "00000000-0000-0000-0000-0000000000a1"

// Client implements port.BillingClient via the Billing HTTP service (SGN-2, LLD §19).
type Client struct {
	httpClient *http.Client
	baseURL    string
}

var _ port.BillingClient = (*Client)(nil)

func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// billingResponseBody covers both the success shape and the RFC 9457 error shape,
// allowing a single decode regardless of status code.
type billingResponseBody struct {
	// Success field (201 / 200 / idempotent 409)
	BillingSubscriptionID string `json:"billing_subscription_id"`
	// RFC 9457 error fields
	ErrorCode string `json:"errorCode"`
	Detail    string `json:"detail"`
}

// chargeRequestBody is the wire shape sent to Billing.
// Field names must match the Billing LLD once published (SGN-2).
type chargeRequestBody struct {
	TenantID     string `json:"tenant_id"`
	PaymentToken string `json:"payment_token"`
	Plan         string `json:"plan"`
	Seats        int    `json:"seats"`
}

// AuthorizeAndCharge calls the Billing service to create a Stripe subscription and
// collect payment. Idempotent on req.IdempotencyKey ("charge:<tenant_id>").
// On ErrPaymentDeclined the caller must delete the handoff (terminal, card declined).
// On ErrDependencyUnavailable the caller may retry (transient service error).
func (c *Client) AuthorizeAndCharge(ctx context.Context, req port.ChargeRequest) (*port.ChargeResponse, error) {
	body, err := json.Marshal(chargeRequestBody{
		TenantID:     req.TenantID,
		PaymentToken: req.PaymentToken,
		Plan:         req.Plan,
		Seats:        req.Seats,
	})
	if err != nil {
		return nil, domain.NewError(domain.ErrInternal, "failed to marshal billing request")
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+authorizeAndChargePath, bytes.NewReader(body))
	if err != nil {
		return nil, domain.NewError(domain.ErrInternal, "failed to build billing request")
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-tenant-id", req.TenantID)
	httpReq.Header.Set("x-user-id", systemPrincipal)
	httpReq.Header.Set("x-tenant-roles", "iam-system")
	httpReq.Header.Set("Idempotency-Key", req.IdempotencyKey)

	start := time.Now()
	resp, err := c.httpClient.Do(httpReq)
	metrics.ObserveDependencyCall("billing", "authorize_and_charge", time.Since(start), resp, err)
	if err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "billing service temporarily unavailable")
	}
	defer resp.Body.Close() //nolint:errcheck

	var out billingResponseBody
	_ = json.NewDecoder(io.LimitReader(resp.Body, maxUpstreamBodyBytes)).Decode(&out)

	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
		if out.BillingSubscriptionID == "" {
			return nil, domain.NewError(domain.ErrInternal, "billing response missing billing_subscription_id")
		}
		return &port.ChargeResponse{BillingSubscriptionID: out.BillingSubscriptionID}, nil

	case http.StatusPaymentRequired:
		// 402 — card declined; terminal, the user must fix their payment method
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "payment was declined by the payment processor")

	case http.StatusConflict:
		// SUBSCRIPTION_ALREADY_EXISTS: idempotent replay — return the existing subscription ID
		if out.ErrorCode == "SUBSCRIPTION_ALREADY_EXISTS" && out.BillingSubscriptionID != "" {
			return &port.ChargeResponse{BillingSubscriptionID: out.BillingSubscriptionID}, nil
		}
		return nil, domain.NewError(domain.ErrInternal, fmt.Sprintf("unexpected billing 409: %s", out.ErrorCode))

	case http.StatusBadRequest:
		// INVALID_REQUEST means a BFF bug — do not retry
		return nil, domain.NewError(domain.ErrInternal, fmt.Sprintf("billing rejected request: %s", out.ErrorCode))

	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "billing service temporarily unavailable")

	default:
		return nil, domain.NewError(domain.ErrInternal, fmt.Sprintf("unexpected billing status %d", resp.StatusCode))
	}
}
