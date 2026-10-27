package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/test/fixtures"
)

func newSessionSvc(sessions *fixtures.FakeSessionStore, limiter *fixtures.FakeRateLimiter) *service.SessionService {
	return service.NewSessionService(sessions, limiter, fixtures.NewFakeIDGenerator(), fixtures.NewFakeClock())
}

func newSessionSvcWithAny(sessions port.SessionStore, limiter port.RateLimiter) *service.SessionService {
	return service.NewSessionService(sessions, limiter, fixtures.NewFakeIDGenerator(), fixtures.NewFakeClock())
}

func TestSessionCreate_HappyPath(t *testing.T) {
	svc := newSessionSvc(fixtures.NewFakeSessionStore(), fixtures.NewFakeRateLimiter())

	result, err := svc.Create(context.Background(), "1.2.3.4")
	require.NoError(t, err)
	assert.NotEmpty(t, result.SID)
	assert.NotEmpty(t, result.SignupID)
	assert.NotEmpty(t, result.CSRFToken)
}

func TestSessionCreate_RateLimitExceeded(t *testing.T) {
	sessions := fixtures.NewFakeSessionStore()
	limiter := fixtures.NewFakeRateLimiter()
	svc := newSessionSvc(sessions, limiter)

	for range 20 {
		_, err := svc.Create(context.Background(), "1.2.3.4")
		require.NoError(t, err)
	}
	_, err := svc.Create(context.Background(), "1.2.3.4")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrRateLimited, de.Cause)
}

func TestValidateCSRF_HappyPath(t *testing.T) {
	sessions := fixtures.NewFakeSessionStore()
	svc := newSessionSvc(sessions, fixtures.NewFakeRateLimiter())

	created, err := svc.Create(context.Background(), "1.2.3.4")
	require.NoError(t, err)

	sess, err := svc.ValidateCSRF(context.Background(), created.SID, created.CSRFToken)
	require.NoError(t, err)
	assert.Equal(t, created.SignupID, sess.SignupID)
}

func TestValidateCSRF_MissingSID(t *testing.T) {
	svc := newSessionSvc(fixtures.NewFakeSessionStore(), fixtures.NewFakeRateLimiter())
	_, err := svc.ValidateCSRF(context.Background(), "", "some-token")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrSessionExpired, de.Cause)
}

func TestValidateCSRF_UnknownSID(t *testing.T) {
	svc := newSessionSvc(fixtures.NewFakeSessionStore(), fixtures.NewFakeRateLimiter())
	_, err := svc.ValidateCSRF(context.Background(), "no-such-sid", "some-token")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrSessionExpired, de.Cause)
}

func TestValidateCSRF_EmptyToken(t *testing.T) {
	sessions := fixtures.NewFakeSessionStore()
	svc := newSessionSvc(sessions, fixtures.NewFakeRateLimiter())
	created, _ := svc.Create(context.Background(), "1.2.3.4")

	_, err := svc.ValidateCSRF(context.Background(), created.SID, "")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrCSRFInvalid, de.Cause)
}

func TestValidateCSRF_WrongToken(t *testing.T) {
	sessions := fixtures.NewFakeSessionStore()
	svc := newSessionSvc(sessions, fixtures.NewFakeRateLimiter())
	created, _ := svc.Create(context.Background(), "1.2.3.4")

	_, err := svc.ValidateCSRF(context.Background(), created.SID, "wrong-token")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrCSRFInvalid, de.Cause)
}

func TestGetSession_HappyPath(t *testing.T) {
	sessions := fixtures.NewFakeSessionStore()
	svc := newSessionSvc(sessions, fixtures.NewFakeRateLimiter())
	created, err := svc.Create(context.Background(), "1.2.3.4")
	require.NoError(t, err)

	sess, err := svc.GetSession(context.Background(), created.SID)
	require.NoError(t, err)
	assert.Equal(t, created.SignupID, sess.SignupID)
}

func TestGetSession_MissingSID(t *testing.T) {
	svc := newSessionSvc(fixtures.NewFakeSessionStore(), fixtures.NewFakeRateLimiter())
	_, err := svc.GetSession(context.Background(), "")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrSessionExpired, de.Cause)
}

func TestGetSession_NotFound(t *testing.T) {
	svc := newSessionSvc(fixtures.NewFakeSessionStore(), fixtures.NewFakeRateLimiter())
	_, err := svc.GetSession(context.Background(), "no-such-sid")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrSessionExpired, de.Cause)
}

func TestSessionCreate_RateLimiterError_ReturnsDependencyUnavailable(t *testing.T) {
	svc := newSessionSvcWithAny(fixtures.NewFakeSessionStore(), &sessErrRateLimiter{})
	_, err := svc.Create(context.Background(), "1.2.3.4")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

func TestSessionCreate_StoreError_ReturnsDependencyUnavailable(t *testing.T) {
	svc := newSessionSvcWithAny(&errOnCreateSessionStore{}, fixtures.NewFakeRateLimiter())
	_, err := svc.Create(context.Background(), "1.2.3.4")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

// sessErrRateLimiter returns an error from IncrSession.
type sessErrRateLimiter struct{}

func (l *sessErrRateLimiter) IncrIP(_ context.Context, _ string) (int64, time.Time, error) {
	return 0, time.Time{}, nil
}
func (l *sessErrRateLimiter) IncrEmail(_ context.Context, _ string) (int64, time.Time, error) {
	return 0, time.Time{}, nil
}
func (l *sessErrRateLimiter) IncrSession(_ context.Context, _ string) (int64, time.Time, error) {
	return 0, time.Time{}, errors.New("rate limiter store error")
}
func (l *sessErrRateLimiter) IncrPoll(_ context.Context, _ string) (int64, time.Time, error) {
	return 0, time.Time{}, nil
}

// errOnCreateSessionStore fails on Create.
type errOnCreateSessionStore struct{}

func (s *errOnCreateSessionStore) Create(_ context.Context, _ *port.Session) error {
	return errors.New("store error")
}
func (s *errOnCreateSessionStore) Get(_ context.Context, _ string) (*port.Session, error) {
	return nil, nil
}
func (s *errOnCreateSessionStore) Refresh(_ context.Context, _ string) error { return nil }
func (s *errOnCreateSessionStore) Delete(_ context.Context, _ string) error  { return nil }
