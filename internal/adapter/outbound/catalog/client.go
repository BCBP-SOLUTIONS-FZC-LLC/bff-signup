package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/metrics"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

const (
	plansPath           = "/api/v1/plans"
	maxCatalogBodyBytes = 64 * 1024
)

// Client implements port.Catalog with in-process 5-minute caching, singleflight
// deduplication on cache miss, and a static fallback (LLD §6, N-2).
type Client struct {
	httpClient *http.Client
	baseURL    string
	fallback   []domain.PlanInfo
	cacheTTL   time.Duration
	sf         singleflight.Group

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

	// Collapse concurrent cache misses into one upstream fetch — prevents thundering herd
	// when the TTL expires under load.
	v, err, _ := c.sf.Do("plans", func() (interface{}, error) {
		plans, e := c.fetch(ctx)
		if e != nil {
			return nil, e
		}
		c.mu.Lock()
		c.cached = plans
		c.expiresAt = time.Now().Add(c.cacheTTL)
		c.mu.Unlock()
		return plans, nil
	})

	if err != nil {
		c.mu.Lock()
		if c.cached != nil {
			plans := c.cached
			c.mu.Unlock()
			return plans, nil
		}
		c.mu.Unlock()
		return c.fallback, nil
	}

	return v.([]domain.PlanInfo), nil
}

type catalogResponse struct {
	Plans []domain.PlanInfo `json:"plans"`
}

func (c *Client) fetch(ctx context.Context) ([]domain.PlanInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+plansPath, nil)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	resp, err := c.httpClient.Do(req)
	metrics.ObserveDependencyCall("catalog", "plans", time.Since(start), resp, err)
	if err != nil {
		return nil, fmt.Errorf("catalog unavailable: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, fmt.Sprintf("catalog HTTP %d", resp.StatusCode))
	}

	var out catalogResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxCatalogBodyBytes)).Decode(&out); err != nil {
		return nil, fmt.Errorf("catalog decode: %w", err)
	}
	return out.Plans, nil
}
