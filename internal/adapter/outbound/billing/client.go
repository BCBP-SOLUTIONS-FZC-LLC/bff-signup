package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/metrics"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/tracing"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

const maxUpstreamBodyBytes = 64 * 1024

// authorizeAndChargePath is Billing's internal route B-I1 (Billing LLD §5.4.2, BL-OQ-8).
// Caller identity is the mesh SPIFFE ID (BIL-AUTH-3); no identity headers are sent.
const authorizeAndChargePath = "/api/v1/internal/subscriptions/authorize-and-charge"

// Billing errorCode values the adapter branches on (Billing LLD §5.4.2).
const (
	codePaymentActionRequired     = "PAYMENT_ACTION_REQUIRED"
	codePaymentDeclined           = "PAYMENT_DECLINED"
	codeChargeExpired             = "CHARGE_EXPIRED"
	codeSubscriptionAlreadyExists = "SUBSCRIPTION_ALREADY_EXISTS"
	codeRequestInProgress         = "REQUEST_IN_PROGRESS"
	codePriceNotAvailable         = "PRICE_NOT_AVAILABLE"
)

// declineReasons is the closed set Billing documents for PAYMENT_DECLINED. Any
// other value is reported to the browser as "other".
var declineReasons = map[string]bool{
	"card_declined": true, "insufficient_funds": true, "expired_card": true,
	"incorrect_cvc": true, "processing_error": true, "authentication_failed": true,
	"other": true,
}

// Client implements port.BillingClient via Billing's B-I1 route (BL-OQ-8, LLD §19).
type Client struct {
	httpClient *http.Client
	baseURL    string
}

var _ port.BillingClient = (*Client)(nil)

// New returns a B-I1 client. timeout must cover B-I1's p99 (12 s); config
// enforces BILLING_TIMEOUT_MS >= 15000 when paid signup is enabled.
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: timeout, Transport: tracing.Transport("billing", true)},
	}
}

// chargeRequestBody is the B-I1 request body. Only the required fields are
// sent: B-I1 retries must repeat the body exactly, so optional fields that the
// user could change between attempts (company name, contact) are left out.
type chargeRequestBody struct {
	TenantID         string `json:"tenant_id"`
	PlanCode         string `json:"plan_code"`
	LicensedSeats    int    `json:"licensed_seats"`
	PaymentMethodRef string `json:"payment_method_ref"`
}

// billingResponseBody covers the success shape and the RFC 9457 problem shape.
type billingResponseBody struct {
	BillingSubscriptionID string `json:"billing_subscription_id"`
	ExpiresAt             string `json:"expires_at"`
	Action                *struct {
		Type         string `json:"type"`
		ClientSecret string `json:"client_secret"`
	} `json:"action"`
	DeclineReason string `json:"decline_reason"`
	ErrorCode     string `json:"errorCode"`
}

// AuthorizeAndCharge calls B-I1. See port.BillingClient for the outcome contract.
func (c *Client) AuthorizeAndCharge(ctx context.Context, req port.ChargeRequest) (*port.ChargeResponse, error) {
	body, err := json.Marshal(chargeRequestBody{
		TenantID:         req.TenantID,
		PlanCode:         req.Plan,
		LicensedSeats:    req.Seats,
		PaymentMethodRef: req.PaymentToken,
	})
	if err != nil {
		return nil, domain.NewError(domain.ErrInternal, "failed to marshal billing request")
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+authorizeAndChargePath, bytes.NewReader(body))
	if err != nil {
		return nil, domain.NewError(domain.ErrInternal, "failed to build billing request")
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-tenant-id", req.TenantID) // must equal body tenant_id (else 400 TENANT_MISMATCH)
	httpReq.Header.Set("Idempotency-Key", req.IdempotencyKey)

	start := time.Now()
	resp, err := c.httpClient.Do(httpReq)
	metrics.ObserveDependencyCall("billing", "authorize_and_charge", time.Since(start), resp, err)
	if err != nil {
		// Timeout or transport error: the charge may be in flight. Retry with the same key.
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "billing service temporarily unavailable")
	}
	defer resp.Body.Close() //nolint:errcheck

	var out billingResponseBody
	_ = json.NewDecoder(io.LimitReader(resp.Body, maxUpstreamBodyBytes)).Decode(&out)

	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK: // 200 = Idempotent-Replayed of the first 201
		if out.BillingSubscriptionID == "" {
			return nil, domain.NewError(domain.ErrInternal, "billing response missing billing_subscription_id")
		}
		return &port.ChargeResponse{Outcome: port.ChargeConfirmed, BillingSubscriptionID: out.BillingSubscriptionID}, nil

	case http.StatusPaymentRequired:
		return paymentRequired(out)

	case http.StatusGone:
		if out.ErrorCode == codeChargeExpired {
			return nil, domain.NewError(domain.ErrChargeExpired, "payment authentication was not completed in time; please start again")
		}
		return nil, unexpected(resp.StatusCode, out.ErrorCode)

	case http.StatusConflict:
		switch out.ErrorCode {
		case codeSubscriptionAlreadyExists:
			if out.BillingSubscriptionID == "" {
				return nil, domain.NewError(domain.ErrInternal, "billing 409 SUBSCRIPTION_ALREADY_EXISTS missing billing_subscription_id")
			}
			return &port.ChargeResponse{Outcome: port.ChargeAlreadyExists, BillingSubscriptionID: out.BillingSubscriptionID}, nil
		case codeRequestInProgress:
			return nil, retryable("payment is still being processed", resp)
		}
		return nil, unexpected(resp.StatusCode, out.ErrorCode)

	case http.StatusUnprocessableEntity:
		if out.ErrorCode == codePriceNotAvailable {
			return nil, domain.NewError(domain.ErrValidationFailed, "the selected plan is not available for purchase")
		}
		return nil, unexpected(resp.StatusCode, out.ErrorCode)

	case http.StatusBadRequest:
		// VALIDATION_ERROR / TENANT_MISMATCH: a BFF bug. Do not retry.
		return nil, domain.NewError(domain.ErrInternal, fmt.Sprintf("billing rejected request: %s", out.ErrorCode))

	case http.StatusForbidden:
		// INSUFFICIENT_ROLE: wrong service account, or the mesh did not forward the client certificate.
		return nil, domain.NewError(domain.ErrInternal, fmt.Sprintf("billing rejected caller identity: %s", out.ErrorCode))

	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		// PROVIDER_UNAVAILABLE / DB_UNAVAILABLE and gateway errors: safe to retry with the same key.
		return nil, retryable("billing service temporarily unavailable", resp)

	default:
		return nil, unexpected(resp.StatusCode, out.ErrorCode)
	}
}

// paymentRequired maps the two documented 402 shapes.
func paymentRequired(out billingResponseBody) (*port.ChargeResponse, error) {
	switch out.ErrorCode {
	case codePaymentActionRequired:
		if out.Action == nil || out.Action.ClientSecret == "" || out.BillingSubscriptionID == "" {
			return nil, domain.NewError(domain.ErrInternal, "billing 402 PAYMENT_ACTION_REQUIRED missing action or billing_subscription_id")
		}
		return &port.ChargeResponse{
			Outcome:               port.ChargeActionRequired,
			BillingSubscriptionID: out.BillingSubscriptionID,
			Action:                &port.PaymentAction{Type: out.Action.Type, ClientSecret: out.Action.ClientSecret},
			ExpiresAt:             out.ExpiresAt,
		}, nil
	case codePaymentDeclined:
		reason := out.DeclineReason
		if !declineReasons[reason] {
			reason = "other"
		}
		return nil, domain.NewError(domain.ErrPaymentDeclined, "the payment was declined").
			WithDetails(map[string]any{"decline_reason": reason})
	}
	return nil, unexpected(http.StatusPaymentRequired, out.ErrorCode)
}

// retryable builds an ErrDependencyUnavailable that carries Billing's
// Retry-After (delta-seconds form) as retry_after_seconds when present.
func retryable(msg string, resp *http.Response) error {
	e := domain.NewError(domain.ErrDependencyUnavailable, msg)
	if secs, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && secs > 0 {
		e = e.WithDetails(map[string]any{"retry_after_seconds": secs})
	}
	return e
}

func unexpected(status int, code string) error {
	return domain.NewError(domain.ErrInternal, fmt.Sprintf("unexpected billing status %d %s", status, code))
}
