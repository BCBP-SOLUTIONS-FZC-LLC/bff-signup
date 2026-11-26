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

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/realmprovisioner"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

// ── ProvisionTrial (RP-1) ─────────────────────────────────────────────────────

// TestRP_ProvisionTrial_HeaderContract verifies the mandatory headers from LLD §5.6 and §14.3:
//
//	x-tenant-id = the handoff's tenant_id
//	x-user-id   = "00000000-0000-0000-0000-0000000000a1" (system principal, C-2)
//	x-tenant-roles = "iam-system"
//	Idempotency-Key = stable, non-empty (signup:<tenant_id>:rp1)
func TestRP_ProvisionTrial_HeaderContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// LLD §14.3 — header contract
		assert.Equal(t, "tid-contract-001", r.Header.Get("x-tenant-id"), "x-tenant-id must be the handoff tenant_id (C-2)")
		assert.Equal(t, "00000000-0000-0000-0000-0000000000a1", r.Header.Get("x-user-id"), "x-user-id must be the system principal (C-2)")
		assert.Equal(t, "iam-system", r.Header.Get("x-tenant-roles"), "x-tenant-roles must be iam-system (LLD §5.6)")
		assert.NotEmpty(t, r.Header.Get("Idempotency-Key"), "Idempotency-Key must be present on RP-1 (C-2)")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(port.ProvisionTrialResponse{KeycloakUserID: "kc-001"})
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	_, err := c.ProvisionTrial(context.Background(), port.ProvisionTrialRequest{
		TenantID: "tid-contract-001",
		Plan:     "pro",
	}, "signup:tid-contract-001:rp1")
	require.NoError(t, err)
}

func TestRP_ProvisionTrial_Created(t *testing.T) {
	want := port.ProvisionTrialResponse{
		TenantID:              "tid-001",
		Realm:                 "trial",
		Strategy:              "shared",
		KeycloakUserID:        "kc-001",
		EmailVerificationSent: true,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/internal/tenants/provision-trial", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.NotEmpty(t, r.Header.Get("Idempotency-Key"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	resp, err := c.ProvisionTrial(context.Background(), port.ProvisionTrialRequest{
		TenantID:   "tid-001",
		TenantName: "Acme",
		TenantSlug: "acme",
		Plan:       "pro",
	}, "signup:tid-001:rp1")
	require.NoError(t, err)
	assert.Equal(t, "kc-001", resp.KeycloakUserID)
	assert.True(t, resp.EmailVerificationSent)
}

func TestRP_ProvisionTrial_EmailAlreadyRegistered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    409,
			"errorCode": "EMAIL_ALREADY_REGISTERED",
		})
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	_, err := c.ProvisionTrial(context.Background(), port.ProvisionTrialRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrEmailUnavailable, de.Cause)
}

func TestRP_ProvisionTrial_OperationInProgress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"errorCode": "OPERATION_IN_PROGRESS"})
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	_, err := c.ProvisionTrial(context.Background(), port.ProvisionTrialRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrOperationInProgress, de.Cause)
}

func TestRP_ProvisionTrial_BadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"errorCode": "INVALID_REQUEST"})
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	_, err := c.ProvisionTrial(context.Background(), port.ProvisionTrialRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInternal, de.Cause)
}

func TestRP_ProvisionTrial_ServiceUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	_, err := c.ProvisionTrial(context.Background(), port.ProvisionTrialRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

func TestRP_ProvisionTrial_NetworkError(t *testing.T) {
	c := realmprovisioner.New("http://127.0.0.1:1", 100*time.Millisecond)
	_, err := c.ProvisionTrial(context.Background(), port.ProvisionTrialRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

// ── ProvisionDirect (RP-2) ────────────────────────────────────────────────────

func TestRP_ProvisionDirect_Created(t *testing.T) {
	want := port.ProvisionDirectResponse{
		TenantID:       "tid-002",
		Realm:          "acme-realm",
		Strategy:       "dedicated",
		KeycloakUserID: "kc-002",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/internal/tenants/provision-direct", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	resp, err := c.ProvisionDirect(context.Background(), port.ProvisionDirectRequest{
		TenantID: "tid-002", Plan: "pro",
	}, "signup:tid-002:rp2")
	require.NoError(t, err)
	assert.Equal(t, "kc-002", resp.KeycloakUserID)
}

func TestRP_ProvisionDirect_UnexpectedStatus_ReturnsInternal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot) // default: case
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	_, err := c.ProvisionDirect(context.Background(), port.ProvisionDirectRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInternal, de.Cause)
}

func TestRP_ProvisionDirect_EmailAlreadyRegistered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"errorCode": "EMAIL_ALREADY_REGISTERED"})
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	_, err := c.ProvisionDirect(context.Background(), port.ProvisionDirectRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrEmailUnavailable, de.Cause)
}

func TestRP_ProvisionDirect_OperationInProgress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"errorCode": "OPERATION_IN_PROGRESS"})
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	_, err := c.ProvisionDirect(context.Background(), port.ProvisionDirectRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrOperationInProgress, de.Cause, "retry with the same key")
}

func TestRP_ProvisionDirect_RealmAlreadyDedicated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"errorCode": "REALM_ALREADY_DEDICATED"})
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	_, err := c.ProvisionDirect(context.Background(), port.ProvisionDirectRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrSlugTaken, de.Cause, "realm tenant-<slug> exists: a slug conflict")
	assert.Contains(t, de.Message, "realm")
}

func TestRP_ProvisionDirect_TenantOffboarded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"errorCode": "TENANT_OFFBOARDED"})
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	_, err := c.ProvisionDirect(context.Background(), port.ProvisionDirectRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInvalidState, de.Cause)
	assert.Contains(t, de.Message, "offboarded")
}

func TestRP_ProvisionDirect_Unexpected409(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"errorCode": "UNKNOWN_CODE"})
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	_, err := c.ProvisionDirect(context.Background(), port.ProvisionDirectRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInternal, de.Cause)
}

func TestRP_ProvisionDirect_BadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"errorCode": "INVALID_REQUEST"})
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	_, err := c.ProvisionDirect(context.Background(), port.ProvisionDirectRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInternal, de.Cause)
}

func TestRP_ProvisionDirect_BadGateway(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	_, err := c.ProvisionDirect(context.Background(), port.ProvisionDirectRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

func TestRP_ProvisionDirect_ServiceUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	_, err := c.ProvisionDirect(context.Background(), port.ProvisionDirectRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

func TestRP_ProvisionDirect_NetworkError(t *testing.T) {
	c := realmprovisioner.New("http://127.0.0.1:1", 100*time.Millisecond)
	_, err := c.ProvisionDirect(context.Background(), port.ProvisionDirectRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

func TestRP_ProvisionDirect_UnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	_, err := c.ProvisionDirect(context.Background(), port.ProvisionDirectRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInternal, de.Cause)
}

// ── ProvisionTrial additional error paths ─────────────────────────────────────

func TestRP_ProvisionTrial_Created_BadJSON_ReturnsInternal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	_, err := c.ProvisionTrial(context.Background(), port.ProvisionTrialRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInternal, de.Cause)
	assert.Contains(t, de.Message, "decode")
}

func TestRP_ProvisionDirect_Created_BadJSON_ReturnsInternal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	_, err := c.ProvisionDirect(context.Background(), port.ProvisionDirectRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInternal, de.Cause)
	assert.Contains(t, de.Message, "decode")
}

func TestRP_ProvisionTrial_RealmAlreadyDedicated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"errorCode": "REALM_ALREADY_DEDICATED"})
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	_, err := c.ProvisionTrial(context.Background(), port.ProvisionTrialRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInvalidState, de.Cause)
}

func TestRP_ProvisionTrial_TenantOffboarded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"errorCode": "TENANT_OFFBOARDED"})
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	_, err := c.ProvisionTrial(context.Background(), port.ProvisionTrialRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInvalidState, de.Cause)
}

func TestRP_ProvisionTrial_Unexpected409(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"errorCode": "UNKNOWN_CODE"})
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	_, err := c.ProvisionTrial(context.Background(), port.ProvisionTrialRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInternal, de.Cause)
}

func TestRP_ProvisionTrial_BadGateway(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	_, err := c.ProvisionTrial(context.Background(), port.ProvisionTrialRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

func TestRP_ProvisionTrial_UnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, time.Second)
	_, err := c.ProvisionTrial(context.Background(), port.ProvisionTrialRequest{}, "key")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInternal, de.Cause)
}

// RP-2 has its own, longer timeout (RP2_TIMEOUT_MS): RP-2 p95 is 4 s against RP-1's 1.5 s.
func TestRP_ProvisionDirect_UsesDirectTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"tenant_id": "t", "keycloak_user_id": "kc", "email_verification_sent": false})
	}))
	defer srv.Close()

	c := realmprovisioner.New(srv.URL, 50*time.Millisecond) // RP-1 budget would time out
	_, err := c.ProvisionDirect(context.Background(), port.ProvisionDirectRequest{TenantID: "t"}, "k")
	require.Error(t, err, "without WithDirectTimeout RP-2 shares the short RP-1 timeout")

	resp, err := c.WithDirectTimeout(time.Second).ProvisionDirect(context.Background(), port.ProvisionDirectRequest{TenantID: "t"}, "k")
	require.NoError(t, err)
	require.NotNil(t, resp.EmailVerificationSent)
	assert.False(t, *resp.EmailVerificationSent)
}
