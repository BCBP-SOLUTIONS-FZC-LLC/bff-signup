package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/test/fixtures"
)

// downSessionStore behaves like Valkey being stopped: every read fails.
type downSessionStore struct{ *fixtures.FakeSessionStore }

func (downSessionStore) Get(context.Context, string) (*port.Session, error) {
	return nil, errors.New("valkey: connection refused")
}

func newServiceWithDownStore() *service.SessionService {
	return service.NewSessionService(
		downSessionStore{fixtures.NewFakeSessionStore()},
		fixtures.NewFakeRateLimiter(), fixtures.NewFakeIDGenerator(), fixtures.NewFakeClock(), 20,
	)
}

// SB3-14 / OBS-32: a Valkey outage must give 503 dependency_unavailable (N-7),
// not 401 session_expired.
func TestGetSession_StoreDown_ReturnsDependencyUnavailable(t *testing.T) {
	_, err := newServiceWithDownStore().GetSession(context.Background(), "some-sid")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

func TestValidateCSRF_StoreDown_ReturnsDependencyUnavailable(t *testing.T) {
	_, err := newServiceWithDownStore().ValidateCSRF(context.Background(), "some-sid", "token")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

// Control: a session that is simply missing is still 401 session_expired.
func TestGetSession_MissingKey_StillSessionExpired(t *testing.T) {
	svc := service.NewSessionService(fixtures.NewFakeSessionStore(),
		fixtures.NewFakeRateLimiter(), fixtures.NewFakeIDGenerator(), fixtures.NewFakeClock(), 20)
	_, err := svc.GetSession(context.Background(), "unknown-sid")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrSessionExpired, de.Cause)
}
