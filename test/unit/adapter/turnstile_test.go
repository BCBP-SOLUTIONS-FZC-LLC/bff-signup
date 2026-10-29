package adapter_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/turnstile"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
)

func TestTurnstile_New_CreatesClient(t *testing.T) {
	c := turnstile.New("test-secret", "signup.example.com", time.Second)
	assert.NotNil(t, c)
}

func TestTurnstile_Verify_NetworkError_ReturnsDependencyUnavailable(t *testing.T) {
	// The siteverify URL is hardcoded to Cloudflare; we can't intercept it.
	// Setting a 1ms timeout forces a timeout/connection error on any real network call.
	c := turnstile.New("test-secret", "", 1*time.Millisecond)
	err := c.Verify(t.Context(), "some-token", "1.2.3.4")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}
