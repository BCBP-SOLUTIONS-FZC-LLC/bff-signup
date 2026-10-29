package domain_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

// ── DomainError.Unwrap ────────────────────────────────────────────────────────

func TestDomainError_Unwrap(t *testing.T) {
	cause := domain.ErrSlugTaken
	de := domain.NewError(cause, "slug already taken")
	// errors.Is traverses the Unwrap chain; confirms Unwrap() returns the sentinel.
	assert.True(t, errors.Is(de, cause))
}

// ── BillingStub ───────────────────────────────────────────────────────────────

func TestBillingStub_ReturnsNotImplemented(t *testing.T) {
	var stub port.BillingStub
	_, err := stub.AuthorizeAndCharge(context.Background(), port.ChargeRequest{})
	require.Error(t, err)
	assert.ErrorIs(t, err, port.ErrNotImplemented)
}

// ── RealClock ─────────────────────────────────────────────────────────────────

func TestRealClock_Now_ReturnsNonZero(t *testing.T) {
	clk := port.RealClock{}
	now := clk.Now()
	assert.False(t, now.IsZero())
	assert.True(t, now.After(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)))
}
