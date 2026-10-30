package service_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/test/fixtures"
)

// I-02 / OBS-50: reading the session (SB-3 poll) must restart the 30-min Valkey TTL.
func TestGetSession_SlidesValkyTTL(t *testing.T) {
	sessions := fixtures.NewFakeSessionStore()
	require.NoError(t, sessions.Create(t.Context(), &port.Session{SID: "sid-1", SignupID: "sig-1"}))
	svc := newSessionSvc(sessions, fixtures.NewFakeRateLimiter())

	_, err := svc.GetSession(t.Context(), "sid-1")

	require.NoError(t, err)
	require.Equal(t, 1, sessions.RefreshCount)
}
