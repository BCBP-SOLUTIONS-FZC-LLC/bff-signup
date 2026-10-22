package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

const plansPath = "/api/v1/plans"

// Client implements port.Catalog with in-process 5-minute single-flight caching
// and a static fallback (LLD §6, N-2).
type Client struct {
	httpClient *http.Client
	baseURL    string
	fallback   []domain.PlanInfo
	cacheTTL   time.Duration

	mu        sync.Mutex
	cached    []domain.PlanInfo
	expiresAt time.Time
}

var _ port.Catalog = (*Client)(nil)

func New(baseURL string, timeout, cacheTTL time.Duration, fallback []domain.PlanInfo) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    baseURL,
		fallback:   fallback,
		cacheTTL:   cacheTTL,
	}
}

func (c *Client) Plans(ctx context.Context) ([]domain.PlanInfo, error) {
	c.mu.Lock()
	if c.cached != nil && time.Now().Before(c.expiresAt) {
		plans := c.cached
		c.mu.Unlock()
		return plans, nil
	}
	c.mu.Unlock()

	plans, err := c.fetch(ctx)
	if err != nil {
		// Serve last good copy or static fallback
		c.mu.Lock()
		if c.cached != nil {
			plans = c.cached
		} else {
			plans = c.fallback
		}
		c.mu.Unlock()
		return plans, nil
	}

	c.mu.Lock()
	c.cached = plans
	c.expiresAt = time.Now().Add(c.cacheTTL)
	c.mu.Unlock()

	return plans, nil
}

type catalogResponse struct {
	Plans []domain.PlanInfo `json:"plans"`
}

func (c *Client) fetch(ctx context.Context) ([]domain.PlanInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+plansPath, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("catalog unavailable: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, fmt.Sprintf("catalog HTTP %d", resp.StatusCode))
	}

	var out catalogResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("catalog decode: %w", err)
	}
	return out.Plans, nil
}
