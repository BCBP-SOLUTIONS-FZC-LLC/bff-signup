package adapter_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	billingadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/billing"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

// Billing B-I1 contract tests (Billing LLD §5.4.2, BL-OQ-8 hand-off §2).

const billingTenant = "3fa85f64-5717-4562-b3fc-2c963f66afa6"

func newBillingClient(url string) *billingadapter.Client {
	return billingadapter.New(url, time.Second)
}

func chargeReq() port.ChargeRequest {
	return port.ChargeRequest{
		TenantID:       billingTenant,
		PaymentToken:   "pm_1QAbCdEfGhIjKlMn",
		Plan:           "pro",
		Seats:          10,
		IdempotencyKey: "charge:" + billingTenant,
	}
}

// billingServer replies with status, optional headers and a JSON body.
func billingServer(t *testing.T, status int, body map[string]any, headers map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		if body != nil {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		if body != nil {
			_ = json.NewEncoder(w).Encode(body)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ── Request contract ──────────────────────────────────────────────────────────

func TestBilling_AuthorizeAndCharge_RequestContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/internal/subscriptions/authorize-and-charge", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, billingTenant, r.Header.Get("x-tenant-id"))
		assert.Equal(t, "charge:"+billingTenant, r.Header.Get("Idempotency-Key"))
		// Caller identity is the mesh SPIFFE ID (BIL-AUTH-3); no identity headers.
		assert.Empty(t, r.Header.Get("x-user-id"))
		assert.Empty(t, r.Header.Get("x-tenant-roles"))

		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		require.NoError(t, json.Unmarshal(raw, &body))
		assert.Equal(t, map[string]any{
			"tenant_id":          billingTenant, // equals x-tenant-id
			"plan_code":          "pro",
			"licensed_seats":     float64(10),
			"payment_method_ref": "pm_1QAbCdEfGhIjKlMn",
		}, body, "only the required B-I1 fields, so retries repeat the body exactly")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"billing_subscription_id": "sub_001", "status": "active"})
	}))
	defer srv.Close()

	resp, err := newBillingClient(srv.URL).AuthorizeAndCharge(context.Background(), chargeReq())
	require.NoError(t, err)
	assert.Equal(t, port.ChargeConfirmed, resp.Outcome)
	assert.Equal(t, "sub_001", resp.BillingSubscriptionID)
}

// ── Definite outcomes ─────────────────────────────────────────────────────────

func TestBilling_AuthorizeAndCharge_Outcomes(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    map[string]any
		headers map[string]string
		outcome port.ChargeOutcome
		subID   string
	}{
		{"201 created", http.StatusCreated, map[string]any{"billing_subscription_id": "sub_new"}, nil, port.ChargeConfirmed, "sub_new"},
		{"200 idempotent replay", http.StatusOK, map[string]any{"billing_subscription_id": "sub_replay"},
			map[string]string{"Idempotent-Replayed": "true"}, port.ChargeConfirmed, "sub_replay"},
		{"409 subscription already exists", http.StatusConflict,
			map[string]any{"errorCode": "SUBSCRIPTION_ALREADY_EXISTS", "billing_subscription_id": "sub_other"},
			nil, port.ChargeAlreadyExists, "sub_other"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := billingServer(t, tc.status, tc.body, tc.headers)
			resp, err := newBillingClient(srv.URL).AuthorizeAndCharge(context.Background(), chargeReq())
			require.NoError(t, err)
			assert.Equal(t, tc.outcome, resp.Outcome)
			assert.Equal(t, tc.subID, resp.BillingSubscriptionID)
			assert.Nil(t, resp.Action)
		})
	}
}

func TestBilling_AuthorizeAndCharge_402_PaymentActionRequired(t *testing.T) {
	srv := billingServer(t, http.StatusPaymentRequired, map[string]any{
		"errorCode":               "PAYMENT_ACTION_REQUIRED",
		"action":                  map[string]any{"type": "3ds", "client_secret": "pi_123_secret_456"},
		"billing_subscription_id": "sub_3ds",
		"expires_at":              "2026-11-26T12:30:00Z",
	}, nil)

	resp, err := newBillingClient(srv.URL).AuthorizeAndCharge(context.Background(), chargeReq())
	require.NoError(t, err)
	assert.Equal(t, port.ChargeActionRequired, resp.Outcome)
	assert.Equal(t, "sub_3ds", resp.BillingSubscriptionID)
	require.NotNil(t, resp.Action)
	assert.Equal(t, "3ds", resp.Action.Type)
	assert.Equal(t, "pi_123_secret_456", resp.Action.ClientSecret)
	assert.Equal(t, "2026-11-26T12:30:00Z", resp.ExpiresAt)
}

// ── Errors ────────────────────────────────────────────────────────────────────

func TestBilling_AuthorizeAndCharge_Errors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       map[string]any
		headers    map[string]string
		cause      error
		retryAfter any // expected details.retry_after_seconds, nil for none
	}{
		{"402 action required without client_secret", http.StatusPaymentRequired,
			map[string]any{"errorCode": "PAYMENT_ACTION_REQUIRED", "billing_subscription_id": "sub"}, nil, domain.ErrInternal, nil},
		{"402 unknown code", http.StatusPaymentRequired, map[string]any{"errorCode": "SOMETHING_ELSE"}, nil, domain.ErrInternal, nil},
		{"410 charge expired", http.StatusGone, map[string]any{"errorCode": "CHARGE_EXPIRED"}, nil, domain.ErrChargeExpired, nil},
		{"410 unknown code", http.StatusGone, map[string]any{"errorCode": "GONE"}, nil, domain.ErrInternal, nil},
		{"409 already exists without id", http.StatusConflict,
			map[string]any{"errorCode": "SUBSCRIPTION_ALREADY_EXISTS"}, nil, domain.ErrInternal, nil},
		{"409 request in progress", http.StatusConflict, map[string]any{"errorCode": "REQUEST_IN_PROGRESS"},
			map[string]string{"Retry-After": "3"}, domain.ErrDependencyUnavailable, 3},
		{"409 unknown code", http.StatusConflict, map[string]any{"errorCode": "UNKNOWN_CONFLICT"}, nil, domain.ErrInternal, nil},
		{"400 validation error", http.StatusBadRequest, map[string]any{"errorCode": "VALIDATION_ERROR"}, nil, domain.ErrInternal, nil},
		{"400 tenant mismatch", http.StatusBadRequest, map[string]any{"errorCode": "TENANT_MISMATCH"}, nil, domain.ErrInternal, nil},
		{"403 insufficient role", http.StatusForbidden, map[string]any{"errorCode": "INSUFFICIENT_ROLE"}, nil, domain.ErrInternal, nil},
		{"422 price not available", http.StatusUnprocessableEntity, map[string]any{"errorCode": "PRICE_NOT_AVAILABLE"}, nil, domain.ErrValidationFailed, nil},
		{"422 unknown code", http.StatusUnprocessableEntity, map[string]any{"errorCode": "OTHER"}, nil, domain.ErrInternal, nil},
		{"500", http.StatusInternalServerError, nil, nil, domain.ErrDependencyUnavailable, nil},
		{"502", http.StatusBadGateway, nil, nil, domain.ErrDependencyUnavailable, nil},
		{"503 provider unavailable", http.StatusServiceUnavailable, map[string]any{"errorCode": "PROVIDER_UNAVAILABLE"},
			map[string]string{"Retry-After": "5"}, domain.ErrDependencyUnavailable, 5},
		{"503 non-numeric Retry-After", http.StatusServiceUnavailable, map[string]any{"errorCode": "DB_UNAVAILABLE"},
			map[string]string{"Retry-After": "Wed, 21 Oct 2026 07:28:00 GMT"}, domain.ErrDependencyUnavailable, nil},
		{"504", http.StatusGatewayTimeout, nil, nil, domain.ErrDependencyUnavailable, nil},
		{"418 unexpected", http.StatusTeapot, nil, nil, domain.ErrInternal, nil},
		{"201 missing subscription id", http.StatusCreated, map[string]any{}, nil, domain.ErrInternal, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := billingServer(t, tc.status, tc.body, tc.headers)
			_, err := newBillingClient(srv.URL).AuthorizeAndCharge(context.Background(), chargeReq())
			require.Error(t, err)
			var de *domain.DomainError
			require.ErrorAs(t, err, &de)
			assert.Equal(t, tc.cause, de.Cause)
			if tc.retryAfter == nil {
				assert.Nil(t, de.Details["retry_after_seconds"])
			} else {
				assert.Equal(t, tc.retryAfter, de.Details["retry_after_seconds"])
			}
		})
	}
}

func TestBilling_AuthorizeAndCharge_402_PaymentDeclined(t *testing.T) {
	tests := []struct{ sent, want string }{
		{"insufficient_funds", "insufficient_funds"},
		{"authentication_failed", "authentication_failed"},
		{"", "other"},
		{"something_new", "other"}, // outside the documented set
	}
	for _, tc := range tests {
		t.Run(tc.want+"/"+tc.sent, func(t *testing.T) {
			srv := billingServer(t, http.StatusPaymentRequired,
				map[string]any{"errorCode": "PAYMENT_DECLINED", "decline_reason": tc.sent}, nil)
			_, err := newBillingClient(srv.URL).AuthorizeAndCharge(context.Background(), chargeReq())
			var de *domain.DomainError
			require.ErrorAs(t, err, &de)
			assert.Equal(t, domain.ErrPaymentDeclined, de.Cause)
			assert.Equal(t, tc.want, de.Details["decline_reason"])
		})
	}
}

func TestBilling_AuthorizeAndCharge_NetworkError(t *testing.T) {
	_, err := billingadapter.New("http://127.0.0.1:1", 100*time.Millisecond).
		AuthorizeAndCharge(context.Background(), chargeReq())
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause, "outcome unknown: caller must retry with the same key")
}

func TestBilling_AuthorizeAndCharge_UnparsableBaseURL(t *testing.T) {
	// "http://bad host" is accepted by url.Parse but rejected by http.NewRequestWithContext,
	// exercising the "failed to build billing request" ErrInternal path.
	_, err := billingadapter.New("http://bad host", time.Second).
		AuthorizeAndCharge(context.Background(), chargeReq())
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInternal, de.Cause)
}
