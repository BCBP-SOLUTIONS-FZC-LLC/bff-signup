package adapter_test

import (
	"context"
	"encoding/json"
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

func newBillingClient(url string) *billingadapter.Client {
	return billingadapter.New(url, time.Second)
}

func chargeReq() port.ChargeRequest {
	return port.ChargeRequest{
		TenantID:       "tid-billing-001",
		PaymentToken:   "tok_visa",
		Plan:           "pro",
		Seats:          5,
		IdempotencyKey: "charge:tid-billing-001",
	}
}

// ── Header contract ───────────────────────────────────────────────────────────

func TestBilling_AuthorizeAndCharge_HeaderContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "tid-billing-001", r.Header.Get("x-tenant-id"))
		assert.Equal(t, "00000000-0000-0000-0000-0000000000a1", r.Header.Get("x-user-id"))
		assert.Equal(t, "iam-system", r.Header.Get("x-tenant-roles"))
		assert.Equal(t, "charge:tid-billing-001", r.Header.Get("Idempotency-Key"))
		assert.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"billing_subscription_id": "sub_001"})
	}))
	defer srv.Close()

	resp, err := newBillingClient(srv.URL).AuthorizeAndCharge(context.Background(), chargeReq())
	require.NoError(t, err)
	assert.Equal(t, "sub_001", resp.BillingSubscriptionID)
}

// ── Happy paths ───────────────────────────────────────────────────────────────

func TestBilling_AuthorizeAndCharge_Created(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"billing_subscription_id": "sub_new_001"})
	}))
	defer srv.Close()

	resp, err := newBillingClient(srv.URL).AuthorizeAndCharge(context.Background(), chargeReq())
	require.NoError(t, err)
	assert.Equal(t, "sub_new_001", resp.BillingSubscriptionID)
}

func TestBilling_AuthorizeAndCharge_OKIdempotentReplay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"billing_subscription_id": "sub_existing_001"})
	}))
	defer srv.Close()

	resp, err := newBillingClient(srv.URL).AuthorizeAndCharge(context.Background(), chargeReq())
	require.NoError(t, err)
	assert.Equal(t, "sub_existing_001", resp.BillingSubscriptionID)
}

func TestBilling_AuthorizeAndCharge_409SubscriptionAlreadyExists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errorCode":               "SUBSCRIPTION_ALREADY_EXISTS",
			"billing_subscription_id": "sub_idempotent_001",
		})
	}))
	defer srv.Close()

	resp, err := newBillingClient(srv.URL).AuthorizeAndCharge(context.Background(), chargeReq())
	require.NoError(t, err)
	assert.Equal(t, "sub_idempotent_001", resp.BillingSubscriptionID)
}

// ── Error paths ───────────────────────────────────────────────────────────────

func TestBilling_AuthorizeAndCharge_402_PaymentDeclined(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_ = json.NewEncoder(w).Encode(map[string]any{"errorCode": "PAYMENT_DECLINED"})
	}))
	defer srv.Close()

	_, err := newBillingClient(srv.URL).AuthorizeAndCharge(context.Background(), chargeReq())
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrPaymentDeclined, de.Cause)
}

func TestBilling_AuthorizeAndCharge_400_BadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"errorCode": "INVALID_REQUEST"})
	}))
	defer srv.Close()

	_, err := newBillingClient(srv.URL).AuthorizeAndCharge(context.Background(), chargeReq())
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInternal, de.Cause)
}

func TestBilling_AuthorizeAndCharge_409UnknownCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"errorCode": "UNKNOWN_CONFLICT"})
	}))
	defer srv.Close()

	_, err := newBillingClient(srv.URL).AuthorizeAndCharge(context.Background(), chargeReq())
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInternal, de.Cause)
}

func TestBilling_AuthorizeAndCharge_502_BadGateway(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	_, err := newBillingClient(srv.URL).AuthorizeAndCharge(context.Background(), chargeReq())
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

func TestBilling_AuthorizeAndCharge_503_ServiceUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	_, err := newBillingClient(srv.URL).AuthorizeAndCharge(context.Background(), chargeReq())
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

func TestBilling_AuthorizeAndCharge_504_GatewayTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	defer srv.Close()

	_, err := newBillingClient(srv.URL).AuthorizeAndCharge(context.Background(), chargeReq())
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

func TestBilling_AuthorizeAndCharge_UnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()

	_, err := newBillingClient(srv.URL).AuthorizeAndCharge(context.Background(), chargeReq())
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInternal, de.Cause)
}

func TestBilling_AuthorizeAndCharge_NetworkError(t *testing.T) {
	_, err := billingadapter.New("http://127.0.0.1:1", 100*time.Millisecond).
		AuthorizeAndCharge(context.Background(), chargeReq())
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

func TestBilling_AuthorizeAndCharge_MissingSubscriptionID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{}) // missing billing_subscription_id
	}))
	defer srv.Close()

	_, err := newBillingClient(srv.URL).AuthorizeAndCharge(context.Background(), chargeReq())
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInternal, de.Cause)
	assert.Contains(t, de.Message, "billing_subscription_id")
}
