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

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/catalog"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
)

func catalogFallback() []domain.PlanInfo {
	return []domain.PlanInfo{
		{Code: domain.PlanStarter, Name: "Starter (fallback)", TrialDays: 14},
	}
}

func TestCatalogClient_Plans_HappyPath(t *testing.T) {
	want := []domain.PlanInfo{
		{Code: domain.PlanStarter, Name: "Starter", TrialDays: 14},
		{Code: domain.PlanPro, Name: "Pro", TrialDays: 14},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/plans", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"plans": want})
	}))
	defer srv.Close()

	c := catalog.New(srv.URL, time.Second, 5*time.Minute, catalogFallback())
	plans, err := c.Plans(context.Background())
	require.NoError(t, err)
	assert.Len(t, plans, 2)
	assert.Equal(t, domain.PlanStarter, plans[0].Code)
}

func TestCatalogClient_Plans_CacheHit(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"plans": catalogFallback()})
	}))
	defer srv.Close()

	c := catalog.New(srv.URL, time.Second, 5*time.Minute, nil)
	_, _ = c.Plans(context.Background())
	_, _ = c.Plans(context.Background()) // should hit cache
	assert.Equal(t, 1, calls, "second call should be served from cache")
}

func TestCatalogClient_Plans_ServerError_ReturnsFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	fb := catalogFallback()
	c := catalog.New(srv.URL, time.Second, 5*time.Minute, fb)
	plans, err := c.Plans(context.Background())
	require.NoError(t, err, "catalog client must be fail-open")
	assert.Equal(t, fb, plans, "fallback plans must be returned on server error")
}

func TestCatalogClient_Plans_InvalidBaseURL_ReturnsFallback(t *testing.T) {
	// "\x00" in the URL causes http.NewRequestWithContext to fail, exercising
	// the "return nil, err" path in fetch (fail-open: Plans returns the fallback).
	fb := catalogFallback()
	c := catalog.New("http://host\x00invalid", time.Second, 5*time.Minute, fb)
	plans, err := c.Plans(context.Background())
	require.NoError(t, err, "catalog client must be fail-open on request-build error")
	assert.Equal(t, fb, plans)
}

func TestCatalogClient_Plans_NetworkError_ReturnsFallback(t *testing.T) {
	// Point at a closed server to force a network error
	fb := catalogFallback()
	c := catalog.New("http://127.0.0.1:1", time.Millisecond*100, 5*time.Minute, fb)
	plans, err := c.Plans(context.Background())
	require.NoError(t, err, "catalog client must be fail-open")
	assert.Equal(t, fb, plans)
}

func TestCatalogClient_Plans_StaleCache_ReturnsLastGoodCopy(t *testing.T) {
	// First call succeeds and populates the cache with a very short TTL (1 ns).
	// Second call hits an expired cache (TTL=1ns) and the server is down,
	// so the client must fall back to the last good cached copy.
	firstPlans := []domain.PlanInfo{{Code: domain.PlanPro, Name: "Pro", TrialDays: 14}}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"plans": firstPlans})
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	// 1 ns TTL so the cache expires immediately after the first call
	c := catalog.New(srv.URL, time.Second, time.Nanosecond, catalogFallback())
	plans1, err1 := c.Plans(context.Background())
	require.NoError(t, err1)
	assert.Equal(t, firstPlans, plans1)

	// After the nanosecond TTL expires, a second call fetches again — server now
	// returns 500, so the client must return the last good cached copy.
	plans2, err2 := c.Plans(context.Background())
	require.NoError(t, err2, "must be fail-open on server error after cache expiry")
	assert.Equal(t, firstPlans, plans2, "must return last good cached value, not static fallback")
}

func TestCatalogClient_Plans_DecodeError_ReturnsFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-valid-json"))
	}))
	defer srv.Close()

	fb := catalogFallback()
	c := catalog.New(srv.URL, time.Second, 5*time.Minute, fb)
	plans, err := c.Plans(context.Background())
	require.NoError(t, err, "catalog client must be fail-open on decode error")
	assert.Equal(t, fb, plans, "fallback must be returned when decode fails")
}
