package realmprovisioner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/metrics"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/tracing"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

const maxUpstreamBodyBytes = 64 * 1024

const (
	rp1Path         = "/api/v1/internal/tenants/provision-trial"
	rp2Path         = "/api/v1/internal/tenants/provision-direct"
	systemPrincipal = "00000000-0000-0000-0000-0000000000a1"
)

// Client implements port.RealmProvisioner (RP-1 and RP-2, LLD §5.6, §18.1, §19).
type Client struct {
	httpClient       *http.Client // RP-1 (trial): timeout RP_TIMEOUT_MS
	directHTTPClient *http.Client // RP-2 (paid): timeout RP2_TIMEOUT_MS
	baseURL          string
}

var _ port.RealmProvisioner = (*Client)(nil)

// New returns an RP client. timeout bounds RP-1; RP-2 uses the same value
// unless WithDirectTimeout sets its own (RP-2 is much slower: realm clone).
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL:          baseURL,
		httpClient:       &http.Client{Timeout: timeout, Transport: tracing.Transport("realm_provisioner", true)},
		directHTTPClient: &http.Client{Timeout: timeout, Transport: tracing.Transport("realm_provisioner", true)},
	}
}

// WithDirectTimeout sets the RP-2 (provision-direct) timeout (RP2_TIMEOUT_MS).
func (c *Client) WithDirectTimeout(timeout time.Duration) *Client {
	if timeout > 0 {
		c.directHTTPClient = &http.Client{Timeout: timeout, Transport: tracing.Transport("realm_provisioner", true)}
	}
	return c
}

// rp1Error is the RFC 9457 error shape RP returns (LLD §17.2).
type rp1Error struct {
	Status    int    `json:"status"`
	ErrorCode string `json:"errorCode"`
	Detail    string `json:"detail"`
}

func (c *Client) ProvisionTrial(ctx context.Context, req port.ProvisionTrialRequest, idempotencyKey string) (*port.ProvisionTrialResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, domain.NewError(domain.ErrInternal, "failed to marshal RP-1 request")
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+rp1Path, bytes.NewReader(body))
	if err != nil {
		return nil, domain.NewError(domain.ErrInternal, "failed to build RP-1 request")
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-tenant-id", req.TenantID)
	httpReq.Header.Set("x-user-id", systemPrincipal)
	httpReq.Header.Set("x-tenant-roles", "iam-system")
	httpReq.Header.Set("Idempotency-Key", idempotencyKey)

	start := time.Now()
	resp, err := c.httpClient.Do(httpReq)
	metrics.ObserveDependencyCall("realm_provisioner", "provision_trial", time.Since(start), resp, err)
	if err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "realm provisioner temporarily unavailable")
	}
	defer resp.Body.Close() //nolint:errcheck

	switch resp.StatusCode {
	case http.StatusCreated:
		var out port.ProvisionTrialResponse
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxUpstreamBodyBytes)).Decode(&out); err != nil {
			return nil, domain.NewError(domain.ErrInternal, "failed to decode RP-1 response")
		}
		return &out, nil

	case http.StatusConflict:
		// Branch on errorCode (RFC 9457) — never on detail (LLD §17.2)
		var rpe rp1Error
		_ = json.NewDecoder(io.LimitReader(resp.Body, maxUpstreamBodyBytes)).Decode(&rpe)
		switch rpe.ErrorCode {
		case "EMAIL_ALREADY_REGISTERED":
			return nil, domain.NewError(domain.ErrEmailUnavailable, "email already registered in Keycloak")
		case "OPERATION_IN_PROGRESS":
			// Caller should retry with the same idempotency key
			return nil, domain.NewError(domain.ErrOperationInProgress, "RP-1 operation in progress, retry")
		case "REALM_ALREADY_DEDICATED":
			return nil, domain.NewError(domain.ErrInvalidState, "tenant realm is already dedicated — trial re-provision not allowed")
		case "TENANT_OFFBOARDED":
			return nil, domain.NewError(domain.ErrInvalidState, "tenant has been offboarded — signup not allowed")
		}
		return nil, domain.NewError(domain.ErrInternal, fmt.Sprintf("unexpected RP-1 409: %s", rpe.ErrorCode))

	case http.StatusBadRequest:
		var rpe rp1Error
		_ = json.NewDecoder(io.LimitReader(resp.Body, maxUpstreamBodyBytes)).Decode(&rpe)
		// INVALID_REQUEST means a BFF bug (bad plan value etc.) — page, do not retry
		return nil, domain.NewError(domain.ErrInternal, fmt.Sprintf("RP-1 rejected request: %s", rpe.ErrorCode))

	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "RP-1 / Keycloak temporarily unavailable")

	default:
		return nil, domain.NewError(domain.ErrInternal, fmt.Sprintf("unexpected RP-1 status %d", resp.StatusCode))
	}
}

// ProvisionDirect calls RP-2 for direct paid signup (LLD §19). The error mapping
// mirrors ProvisionTrial except OPERATION_IN_PROGRESS → ErrInvalidState; RP-2
// response codes are to be re-confirmed against RP LLD §5.3 (LLD §19.6).
func (c *Client) ProvisionDirect(ctx context.Context, req port.ProvisionDirectRequest, idempotencyKey string) (*port.ProvisionDirectResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, domain.NewError(domain.ErrInternal, "failed to marshal RP-2 request")
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+rp2Path, bytes.NewReader(body))
	if err != nil {
		return nil, domain.NewError(domain.ErrInternal, "failed to build RP-2 request")
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-tenant-id", req.TenantID)
	httpReq.Header.Set("x-user-id", systemPrincipal)
	httpReq.Header.Set("x-tenant-roles", "iam-system")
	httpReq.Header.Set("Idempotency-Key", idempotencyKey)

	start := time.Now()
	resp, err := c.directHTTPClient.Do(httpReq)
	metrics.ObserveDependencyCall("realm_provisioner", "provision_direct", time.Since(start), resp, err)
	if err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "realm provisioner temporarily unavailable")
	}
	defer resp.Body.Close() //nolint:errcheck

	switch resp.StatusCode {
	case http.StatusCreated:
		var out port.ProvisionDirectResponse
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxUpstreamBodyBytes)).Decode(&out); err != nil {
			return nil, domain.NewError(domain.ErrInternal, "failed to decode RP-2 response")
		}
		return &out, nil

	case http.StatusConflict:
		var rpe rp1Error
		_ = json.NewDecoder(io.LimitReader(resp.Body, maxUpstreamBodyBytes)).Decode(&rpe)
		switch rpe.ErrorCode {
		case "EMAIL_ALREADY_REGISTERED":
			return nil, domain.NewError(domain.ErrEmailUnavailable, "email already registered in Keycloak")
		case "OPERATION_IN_PROGRESS":
			// RP is still running this key; the caller retries with the same key.
			return nil, domain.NewError(domain.ErrOperationInProgress, "RP-2 operation in progress, retry")
		case "REALM_ALREADY_DEDICATED":
			// Realm tenant-<slug> already exists in Keycloak: a slug collision
			// PUB-1 (fail-open) did not catch, or a leftover realm.
			return nil, domain.NewError(domain.ErrSlugTaken, "a realm for this workspace address already exists")
		case "TENANT_OFFBOARDED":
			return nil, domain.NewError(domain.ErrInvalidState, "tenant has been offboarded — signup not allowed")
		}
		return nil, domain.NewError(domain.ErrInternal, fmt.Sprintf("unexpected RP-2 409: %s", rpe.ErrorCode))

	case http.StatusBadRequest:
		var rpe rp1Error
		_ = json.NewDecoder(io.LimitReader(resp.Body, maxUpstreamBodyBytes)).Decode(&rpe)
		return nil, domain.NewError(domain.ErrInternal, fmt.Sprintf("RP-2 rejected request: %s", rpe.ErrorCode))

	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "RP-2 / Keycloak temporarily unavailable")

	default:
		return nil, domain.NewError(domain.ErrInternal, fmt.Sprintf("unexpected RP-2 status %d", resp.StatusCode))
	}
}
