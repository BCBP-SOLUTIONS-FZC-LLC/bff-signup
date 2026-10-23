package orgmembership

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

const (
	i1Path          = "/api/v1/internal/tenants"
	eligibilityPath = "/api/v1/internal/trial-eligibility"
	pub1Path        = "/api/v1/public/tenants/resolve"
	systemPrincipal = "00000000-0000-0000-0000-0000000000a1"
)

// Client implements port.OrgMembership (I-1 + trial-eligibility, LLD §5.6, §18.2).
type Client struct {
	httpClient *http.Client
	baseURL    string
}

var _ port.OrgMembership = (*Client)(nil)

func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// omError is the RFC 9457 error shape O&M returns (O&M rev 2.29+, LLD §17.2).
type omError struct {
	Status    int    `json:"status"`
	ErrorCode string `json:"errorCode"`
	Detail    string `json:"detail"`
}

// eligibilityRequest is the POST /internal/trial-eligibility body (CR-4).
type eligibilityRequest struct {
	EmailHash string `json:"email_hash"`
}

// pub1Response is the PUB-1 GET /api/v1/public/tenants/resolve response shape.
type pub1Response struct {
	Found bool `json:"found"`
}

// CheckSlugFree calls PUB-1 (GET /api/v1/public/tenants/resolve?identifier=<slug>).
// Fail-open: any non-200 response or network error returns nil so the signup can proceed.
// Only confirmed found:true returns ErrSlugTaken (A-3, LLD §7).
func (c *Client) CheckSlugFree(ctx context.Context, slug string) error {
	u, err := url.Parse(c.baseURL + pub1Path)
	if err != nil {
		return nil // can't build URL, proceed
	}
	q := u.Query()
	q.Set("identifier", slug)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil // proceed
	}
	req.Header.Set("x-user-id", systemPrincipal)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil // network error = couldn't check, proceed
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return nil // 4xx/5xx = couldn't confirm, proceed
	}

	var out pub1Response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil // decode error = proceed
	}

	if out.Found {
		return domain.NewError(domain.ErrSlugTaken, "slug is already in use by an existing tenant")
	}
	return nil
}

// CheckEligibility calls POST /internal/trial-eligibility (CR-4, LLD §5.6 step E).
func (c *Client) CheckEligibility(ctx context.Context, emailHash string) error {
	body, _ := json.Marshal(eligibilityRequest{EmailHash: emailHash})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+eligibilityPath, bytes.NewReader(body))
	if err != nil {
		return domain.NewError(domain.ErrInternal, "failed to build eligibility request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-user-id", systemPrincipal)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return domain.NewError(domain.ErrDependencyUnavailable, fmt.Sprintf("eligibility service unreachable: %v", err))
	}
	defer resp.Body.Close() //nolint:errcheck

	switch resp.StatusCode {
	case http.StatusOK:
		return nil // eligible
	case http.StatusConflict:
		var ome omError
		_ = json.NewDecoder(resp.Body).Decode(&ome)
		if ome.ErrorCode == "TRIAL_ALREADY_USED" {
			return domain.NewError(domain.ErrTrialNotEligible, "this email has already been used for a trial")
		}
		return domain.NewError(domain.ErrInternal, fmt.Sprintf("unexpected eligibility 409: %s", ome.ErrorCode))
	default:
		return domain.NewError(domain.ErrDependencyUnavailable, fmt.Sprintf("eligibility service error: %d", resp.StatusCode))
	}
}

// ProvisionTenant calls I-1 (LLD §5.6 step 2, §18.2).
// Idempotent on tenant_id — a 200 means idempotent replay, 201 means created.
func (c *Client) ProvisionTenant(ctx context.Context, req port.ProvisionTenantRequest) (*port.ProvisionTenantResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, domain.NewError(domain.ErrInternal, "failed to marshal I-1 request")
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+i1Path, bytes.NewReader(body))
	if err != nil {
		return nil, domain.NewError(domain.ErrInternal, "failed to build I-1 request")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-tenant-id", req.TenantID)
	httpReq.Header.Set("x-user-id", systemPrincipal)
	// No Idempotency-Key — O&M I-1 is idempotent by state on tenant_id (LLD §9.2)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, fmt.Sprintf("I-1 unreachable: %v", err))
	}
	defer resp.Body.Close() //nolint:errcheck

	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
		var out port.ProvisionTenantResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return nil, domain.NewError(domain.ErrInternal, "failed to decode I-1 response")
		}
		return &out, nil

	case http.StatusConflict:
		var ome omError
		_ = json.NewDecoder(resp.Body).Decode(&ome)
		if ome.ErrorCode == "SLUG_ALREADY_TAKEN" {
			return nil, domain.NewError(domain.ErrSlugTaken, "slug is already taken by another tenant")
		}
		return nil, domain.NewError(domain.ErrInternal, fmt.Sprintf("unexpected I-1 409: %s", ome.ErrorCode))

	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "O&M temporarily unavailable")

	default:
		return nil, domain.NewError(domain.ErrInternal, fmt.Sprintf("unexpected I-1 status %d", resp.StatusCode))
	}
}
