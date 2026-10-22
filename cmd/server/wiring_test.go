package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
)

// TestBuildCatalog_EmptyURL_ReturnsStaticCatalog verifies that when no Catalog
// Service URL is configured, buildCatalog returns a staticCatalog backed by
// the plansFallback list — not nil and not an error.
func TestBuildCatalog_EmptyURL_ReturnsStaticCatalog(t *testing.T) {
	setValidEnv(t)
	cfg, err := config.Load()
	require.NoError(t, err)

	cfg.CatalogURL = "" // no Catalog Service configured
	cat := buildCatalog(cfg)
	require.NotNil(t, cat)

	plans, err := cat.Plans(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, plans, "static catalog must return at least one plan")
}

// TestStaticCatalog_NeverContainsTrial asserts that the staticCatalog fallback
// never includes "trial" as a plan code (SB-INV-8 — plan must be starter|pro|enterprise).
func TestStaticCatalog_NeverContainsTrial(t *testing.T) {
	setValidEnv(t)
	cfg, err := config.Load()
	require.NoError(t, err)

	cfg.CatalogURL = ""
	cat := buildCatalog(cfg)
	plans, err := cat.Plans(context.Background())
	require.NoError(t, err)

	for _, p := range plans {
		assert.NotEqual(t, domain.Plan("trial"), p.Code,
			"plan fallback must never contain 'trial' (SB-INV-8)")
	}
}

// TestBuildPasswordHasher_WorksEndToEnd ensures the hasher is wired correctly
// and produces a valid seed with the configured PBKDF2 iteration count.
func TestBuildPasswordHasher_WorksEndToEnd(t *testing.T) {
	setValidEnv(t)
	cfg, err := config.Load()
	require.NoError(t, err)

	h := buildPasswordHasher(cfg)
	require.NotNil(t, h)

	seed, err := h.Hash("TestPassword123!")
	require.NoError(t, err)
	assert.Equal(t, "pbkdf2-sha512", seed.Algorithm)
	assert.Equal(t, cfg.PBKDF2Iterations, seed.Iterations)
	assert.NotEmpty(t, seed.Salt)
	assert.NotEmpty(t, seed.Hash)
}

// TestDefaultIDGen_ULID verifies that NewULID returns a 26-character ULID string.
func TestDefaultIDGen_ULID(t *testing.T) {
	g := &defaultIDGen{}
	id := g.NewULID()
	assert.NotEmpty(t, id)
	assert.Len(t, id, 26, "ULID must be 26 characters")
}

// TestDefaultIDGen_UUIDv7 verifies that NewUUIDv7 returns a valid UUID v7 string.
func TestDefaultIDGen_UUIDv7(t *testing.T) {
	g := &defaultIDGen{}
	id := g.NewUUIDv7()
	assert.NotEmpty(t, id)
	assert.Len(t, id, 36, "UUIDv7 must be 36 characters (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx)")
}
