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

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/orgmembership"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

// ── CheckSlugFree ─────────────────────────────────────────────────────────────

func TestOrgMembership_CheckSlugFree_Free(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/public/tenants/resolve", r.URL.Path)
		assert.Equal(t, "myslug", r.URL.Query().Get("identifier"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"found": false})
	}))
	defer srv.Close()

	c := orgmembership.New(srv.URL, time.Second)
	err := c.CheckSlugFree(context.Background(), "myslug")
	assert.NoError(t, err)
}

func TestOrgMembership_CheckSlugFree_Taken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"found": true})
	}))
	defer srv.Close()

	c := orgmembership.New(srv.URL, time.Second)
	err := c.CheckSlugFree(context.Background(), "taken-slug")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrSlugTaken, de.Cause)
}

func TestOrgMembership_CheckSlugFree_ServerError_FailOpen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := orgmembership.New(srv.URL, time.Second)
	err := c.CheckSlugFree(context.Background(), "any-slug")
	assert.NoError(t, err, "PUB-1 must be fail-open on server error")
}

func TestOrgMembership_CheckSlugFree_NetworkError_FailOpen(t *testing.T) {
	c := orgmembership.New("http://127.0.0.1:1", 100*time.Millisecond)
	err := c.CheckSlugFree(context.Background(), "any-slug")
	assert.NoError(t, err, "PUB-1 must be fail-open on network error")
}

// ── CheckEligibility ──────────────────────────────────────────────────────────

func TestOrgMembership_CheckEligibility_Eligible(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/internal/trial-eligibility", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := orgmembership.New(srv.URL, time.Second)
	err := c.CheckEligibility(context.Background(), "hash-001")
	assert.NoError(t, err)
}

func TestOrgMembership_CheckEligibility_AlreadyUsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    409,
			"errorCode": "TRIAL_ALREADY_USED",
			"detail":    "already used",
		})
	}))
	defer srv.Close()

	c := orgmembership.New(srv.URL, time.Second)
	err := c.CheckEligibility(context.Background(), "hash-001")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrTrialNotEligible, de.Cause)
}

func TestOrgMembership_CheckEligibility_ServiceDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := orgmembership.New(srv.URL, time.Second)
	err := c.CheckEligibility(context.Background(), "hash-001")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

// ── ProvisionTenant (I-1) ─────────────────────────────────────────────────────

// TestOrgMembership_I1_HeaderContract verifies the LLD §5.6 + §14.3 header contract for I-1:
//
//	x-tenant-id = the handoff's tenant_id (NOT nilUUID — that's only for eligibility)
//	x-user-id   = system principal …00a1
//	x-tenant-roles = iam-system
//	No Idempotency-Key — O&M is idempotent by state on tenant_id (§9.2, not via ledger)
func TestOrgMembership_I1_HeaderContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// LLD §14.3 header contract
		assert.Equal(t, "tid-i1-contract", r.Header.Get("x-tenant-id"), "x-tenant-id must be the tenant_id (C-2)")
		assert.Equal(t, "00000000-0000-0000-0000-0000000000a1", r.Header.Get("x-user-id"), "x-user-id must be system principal (C-2)")
		assert.Equal(t, "iam-system", r.Header.Get("x-tenant-roles"), "x-tenant-roles must be iam-system (LLD §5.6)")
		assert.Empty(t, r.Header.Get("Idempotency-Key"), "I-1 must NOT carry Idempotency-Key (O&M idempotent by state, §9.2)")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(port.ProvisionTenantResponse{TenantID: "tid-i1-contract"})
	}))
	defer srv.Close()

	c := orgmembership.New(srv.URL, time.Second)
	resp, err := c.ProvisionTenant(context.Background(), port.ProvisionTenantRequest{
		TenantID: "tid-i1-contract",
		Slug:     "acme",
		Name:     "Acme Corp",
		Plan:     "pro",
	})
	require.NoError(t, err)
	assert.Equal(t, "tid-i1-contract", resp.TenantID)
}

func TestOrgMembership_ProvisionTenant_Created(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/internal/tenants", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(port.ProvisionTenantResponse{TenantID: "tid-001"})
	}))
	defer srv.Close()

	c := orgmembership.New(srv.URL, time.Second)
	resp, err := c.ProvisionTenant(context.Background(), port.ProvisionTenantRequest{
		TenantID: "tid-001", Slug: "acme", Name: "Acme", Plan: "pro",
	})
	require.NoError(t, err)
	assert.Equal(t, "tid-001", resp.TenantID)
}

func TestOrgMembership_ProvisionTenant_IdempotentReplay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK) // 200 = idempotent replay
		_ = json.NewEncoder(w).Encode(port.ProvisionTenantResponse{TenantID: "tid-001"})
	}))
	defer srv.Close()

	c := orgmembership.New(srv.URL, time.Second)
	resp, err := c.ProvisionTenant(context.Background(), port.ProvisionTenantRequest{TenantID: "tid-001"})
	require.NoError(t, err)
	assert.Equal(t, "tid-001", resp.TenantID)
}

func TestOrgMembership_ProvisionTenant_SlugTaken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    409,
			"errorCode": "SLUG_ALREADY_TAKEN",
			"detail":    "slug in use",
		})
	}))
	defer srv.Close()

	c := orgmembership.New(srv.URL, time.Second)
	_, err := c.ProvisionTenant(context.Background(), port.ProvisionTenantRequest{TenantID: "tid", Slug: "taken"})
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrSlugTaken, de.Cause)
}

func TestOrgMembership_ProvisionTenant_UnexpectedStatus_ReturnsInternal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot) // 418 — hits the default: case
	}))
	defer srv.Close()

	c := orgmembership.New(srv.URL, time.Second)
	_, err := c.ProvisionTenant(context.Background(), port.ProvisionTenantRequest{TenantID: "tid"})
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInternal, de.Cause)
}

func TestOrgMembership_ProvisionTenant_ServiceDown(t *testing.T) {
	c := orgmembership.New("http://127.0.0.1:1", 100*time.Millisecond)
	_, err := c.ProvisionTenant(context.Background(), port.ProvisionTenantRequest{TenantID: "tid"})
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

func TestOrgMembership_ProvisionTenant_GatewayTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	defer srv.Close()

	c := orgmembership.New(srv.URL, time.Second)
	_, err := c.ProvisionTenant(context.Background(), port.ProvisionTenantRequest{TenantID: "tid"})
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

func TestOrgMembership_ProvisionTenant_Unexpected409(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"errorCode": "SOME_OTHER_CONFLICT"})
	}))
	defer srv.Close()

	c := orgmembership.New(srv.URL, time.Second)
	_, err := c.ProvisionTenant(context.Background(), port.ProvisionTenantRequest{TenantID: "tid", Slug: "xyz"})
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInternal, de.Cause)
}

func TestOrgMembership_ProvisionTenant_UnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()

	c := orgmembership.New(srv.URL, time.Second)
	_, err := c.ProvisionTenant(context.Background(), port.ProvisionTenantRequest{TenantID: "tid"})
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInternal, de.Cause)
}

func TestOrgMembership_ProvisionTenant_Created_BadJSON_ReturnsInternal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	c := orgmembership.New(srv.URL, time.Second)
	_, err := c.ProvisionTenant(context.Background(), port.ProvisionTenantRequest{TenantID: "tid"})
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInternal, de.Cause)
	assert.Contains(t, de.Message, "decode")
}

func TestOrgMembership_CheckEligibility_Unexpected409(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"errorCode": "SOME_UNKNOWN_CODE"})
	}))
	defer srv.Close()

	c := orgmembership.New(srv.URL, time.Second)
	err := c.CheckEligibility(context.Background(), "hash-999")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInternal, de.Cause)
}

func TestOrgMembership_CheckEligibility_NetworkError(t *testing.T) {
	c := orgmembership.New("http://127.0.0.1:1", 100*time.Millisecond)
	err := c.CheckEligibility(context.Background(), "hash-x")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

// TestOrgMembership_CheckSlugFree_DecodeError checks the fail-open path when
// the response body is 200 but is not valid JSON.
func TestOrgMembership_CheckSlugFree_DecodeError_FailOpen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	c := orgmembership.New(srv.URL, time.Second)
	err := c.CheckSlugFree(context.Background(), "any-slug")
	assert.NoError(t, err, "decode error must be fail-open")
}
