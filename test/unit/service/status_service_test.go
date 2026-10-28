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

func newStatusSvc(
	handoffs *fixtures.FakeHandoffStore,
	limiter *fixtures.FakeRateLimiter,
	rp *fixtures.FakeRealmProvisioner,
	om *fixtures.FakeOrgMembership,
) *service.StatusService {
	clk := fixtures.NewFakeClock()
	orch := service.NewHandoffOrchestrator(handoffs, rp, om, clk, 10*time.Millisecond)
	return service.NewStatusService(handoffs, limiter, orch, clk, nil, 60)
}

func newStatusSvcWithLimiter(
	handoffs *fixtures.FakeHandoffStore,
	limiter port.RateLimiter,
	rp *fixtures.FakeRealmProvisioner,
	om *fixtures.FakeOrgMembership,
) *service.StatusService {
	clk := fixtures.NewFakeClock()
	orch := service.NewHandoffOrchestrator(handoffs, rp, om, clk, 10*time.Millisecond)
	return service.NewStatusService(handoffs, limiter, orch, clk, nil, 60)
}

func seedStateHandoff(t *testing.T, handoffs *fixtures.FakeHandoffStore, state domain.HandoffState) {
	t.Helper()
	clk := fixtures.NewFakeClock()
	h := &domain.Handoff{
		SignupID:       "01JAB0000000000000000001",
		TenantID:       "tenant-uuid-001",
		State:          state,
		Slug:           "acme",
		CompanyName:    "Acme Corp",
		Plan:           domain.PlanPro,
		AdminEmailHMAC: "abc123",
		KeycloakUserID: "kc-001",
		CreatedAt:      clk.Now(),
		UpdatedAt:      clk.Now(),
	}
	require.NoError(t, handoffs.Create(context.Background(), h))
}

func TestGetStatus_Completed(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	seedStateHandoff(t, handoffs, domain.StateCompleted)
	svc := newStatusSvc(handoffs, fixtures.NewFakeRateLimiter(), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"))

	result, err := svc.GetStatus(context.Background(), "01JAB0000000000000000001", "01JAB0000000000000000001", "sid-001")
	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, result.State)
}

func TestGetStatus_SignupIDMismatch(t *testing.T) {
	svc := newStatusSvc(fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(), fixtures.NewFakeRealmProvisioner(""), fixtures.NewFakeOrgMembership(""))
	_, err := svc.GetStatus(context.Background(), "session-signup", "different-signup", "sid")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrSignupNotFound, de.Cause)
}

func TestGetStatus_RateLimited(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	limiter := fixtures.NewFakeRateLimiter()
	seedStateHandoff(t, handoffs, domain.StateCompleted)
	svc := newStatusSvc(handoffs, limiter, fixtures.NewFakeRealmProvisioner(""), fixtures.NewFakeOrgMembership(""))

	for range 60 {
		_, _ = svc.GetStatus(context.Background(), "01JAB0000000000000000001", "01JAB0000000000000000001", "sid-001")
	}
	_, err := svc.GetStatus(context.Background(), "01JAB0000000000000000001", "01JAB0000000000000000001", "sid-001")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrRateLimited, de.Cause)
}

// TestGetStatus_HandoffStoreError_Returns503 verifies that a Valkey read error on
// GetStatus is mapped to ErrDependencyUnavailable (503), not ErrSignupNotFound (404).
func TestGetStatus_HandoffStoreError_Returns503(t *testing.T) {
	clk := fixtures.NewFakeClock()
	handoffs := &errOnGetHandoffStore{FakeHandoffStore: fixtures.NewFakeHandoffStore()}
	orch := service.NewHandoffOrchestrator(handoffs, fixtures.NewFakeRealmProvisioner(""), fixtures.NewFakeOrgMembership(""), clk, 10*time.Millisecond)
	svc := service.NewStatusService(handoffs, fixtures.NewFakeRateLimiter(), orch, clk, nil, 60)

	_, err := svc.GetStatus(context.Background(), "any-id", "any-id", "sid")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause, "Valkey error must map to 503, not 404")
}

// TestChangeSlug_HandoffStoreError_Returns503 verifies that a Valkey read error on
// ChangeSlug is mapped to ErrDependencyUnavailable (503), not ErrSignupNotFound (404).
func TestChangeSlug_HandoffStoreError_Returns503(t *testing.T) {
	clk := fixtures.NewFakeClock()
	handoffs := &errOnGetHandoffStore{FakeHandoffStore: fixtures.NewFakeHandoffStore()}
	orch := service.NewHandoffOrchestrator(handoffs, fixtures.NewFakeRealmProvisioner(""), fixtures.NewFakeOrgMembership(""), clk, 10*time.Millisecond)
	svc := service.NewStatusService(handoffs, fixtures.NewFakeRateLimiter(), orch, clk, nil, 60)

	_, err := svc.ChangeSlug(context.Background(), "any-id", "any-id", "newslug")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause, "Valkey error must map to 503, not 404")
}

func TestGetStatus_NotFound(t *testing.T) {
	svc := newStatusSvc(fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(), fixtures.NewFakeRealmProvisioner(""), fixtures.NewFakeOrgMembership(""))
	_, err := svc.GetStatus(context.Background(), "no-id", "no-id", "sid")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrSignupNotFound, de.Cause)
}

func TestGetStatus_RateLimiterError_ReturnsDependencyUnavailable(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	seedStateHandoff(t, handoffs, domain.StateCompleted)
	svc := newStatusSvcWithLimiter(handoffs, &errRateLimiter{}, fixtures.NewFakeRealmProvisioner(""), fixtures.NewFakeOrgMembership(""))

	_, err := svc.GetStatus(context.Background(), "01JAB0000000000000000001", "01JAB0000000000000000001", "sid-001")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

// errRateLimiter always returns an error from IncrPoll.
type errRateLimiter struct{}

func (l *errRateLimiter) IncrIP(_ context.Context, _ string) (int64, time.Time, error) {
	return 0, time.Time{}, nil
}
func (l *errRateLimiter) IncrEmail(_ context.Context, _ string) (int64, time.Time, error) {
	return 0, time.Time{}, nil
}
func (l *errRateLimiter) IncrSession(_ context.Context, _ string) (int64, time.Time, error) {
	return 0, time.Time{}, nil
}
func (l *errRateLimiter) IncrPoll(_ context.Context, _ string) (int64, time.Time, error) {
	return 0, time.Time{}, errors.New("rate limiter store error")
}

func TestGetStatus_TenantPending_ResumesI1(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	om := fixtures.NewFakeOrgMembership("tid")
	seedStateHandoff(t, handoffs, domain.StateTenantPending)
	svc := newStatusSvc(handoffs, fixtures.NewFakeRateLimiter(), fixtures.NewFakeRealmProvisioner("kc"), om)

	result, err := svc.GetStatus(context.Background(), "01JAB0000000000000000001", "01JAB0000000000000000001", "sid")
	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, result.State)
	assert.Len(t, om.I1Calls, 1)
}

func TestGetStatus_SlugConflict_ReturnsActionRequired(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	seedStateHandoff(t, handoffs, domain.StateSlugConflict)
	svc := newStatusSvc(handoffs, fixtures.NewFakeRateLimiter(), fixtures.NewFakeRealmProvisioner(""), fixtures.NewFakeOrgMembership(""))

	result, err := svc.GetStatus(context.Background(), "01JAB0000000000000000001", "01JAB0000000000000000001", "sid")
	require.NoError(t, err)
	assert.Equal(t, domain.StateSlugConflict, result.State)
	assert.Equal(t, "choose_slug", result.Action)
}

func TestGetStatus_TenantPending_ResumeExpired(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	clk := fixtures.NewFakeClock()
	h := &domain.Handoff{
		SignupID:       "01JAB0000000000000000001",
		TenantID:       "tid",
		State:          domain.StateTenantPending,
		Slug:           "acme",
		CompanyName:    "Acme",
		Plan:           domain.PlanPro,
		KeycloakUserID: "kc-001",
		// CreatedAt 16 minutes before the clock's Now() — outside the 15-min resume window.
		CreatedAt: clk.Now().Add(-16 * time.Minute),
		UpdatedAt: clk.Now(),
	}
	require.NoError(t, handoffs.Create(context.Background(), h))
	svc := newStatusSvc(handoffs, fixtures.NewFakeRateLimiter(), fixtures.NewFakeRealmProvisioner(""), fixtures.NewFakeOrgMembership(""))

	result, err := svc.GetStatus(context.Background(), "01JAB0000000000000000001", "01JAB0000000000000000001", "sid")
	require.NoError(t, err)
	assert.Equal(t, domain.StateFailed, result.State)
}

// TestChangeSlug_SlugConflict_Returns409WithSuggestions verifies that SB-4 propagates
// ErrSlugTaken and slug suggestions when the new slug is also taken (A-3, LLD §5.5).
// Prior bug: the error was swallowed and 202 was returned instead of 409.
func TestChangeSlug_SlugConflict_Returns409WithSuggestions(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	om := fixtures.NewFakeOrgMembership("tid")
	om.I1Err = domain.NewError(domain.ErrSlugTaken, "slug taken on second attempt")
	seedStateHandoff(t, handoffs, domain.StateSlugConflict)

	// KeycloakUserID must be set so CanChangeSlug guard passes
	h, _ := handoffs.Get(context.Background(), "01JAB0000000000000000001")
	h.KeycloakUserID = "kc-001"
	require.NoError(t, handoffs.Update(context.Background(), h))

	svc := newStatusSvc(handoffs, fixtures.NewFakeRateLimiter(), fixtures.NewFakeRealmProvisioner("kc"), om)

	result, err := svc.ChangeSlug(context.Background(), "01JAB0000000000000000001", "01JAB0000000000000000001", "also-taken")
	require.Error(t, err, "ErrSlugTaken must be returned so the handler builds a 409")
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrSlugTaken, de.Cause)
	require.NotNil(t, result, "result must be non-nil so handler can read suggestions")
	assert.NotEmpty(t, result.SlugSuggestions, "409 response must include suggestions (A-3)")
}

func TestChangeSlug_HappyPath(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	om := fixtures.NewFakeOrgMembership("tid")
	seedStateHandoff(t, handoffs, domain.StateSlugConflict)
	svc := newStatusSvc(handoffs, fixtures.NewFakeRateLimiter(), fixtures.NewFakeRealmProvisioner("kc"), om)

	result, err := svc.ChangeSlug(context.Background(), "01JAB0000000000000000001", "01JAB0000000000000000001", "newslug")
	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, result.State)
	assert.Len(t, om.I1Calls, 1)
	assert.Equal(t, "newslug", om.I1Calls[0].Slug)
}

// errUpdateHandoffStore embeds FakeHandoffStore but returns an error on Update.
type errUpdateHandoffStore struct {
	*fixtures.FakeHandoffStore
}

func (e *errUpdateHandoffStore) Update(_ context.Context, _ *domain.Handoff) error {
	return errors.New("store update error")
}

func TestChangeSlug_OrchestratorUpdateFails_ReturnsError(t *testing.T) {
	// The orchestrator.ChangeSlug calls handoffs.Update; if it fails,
	// ErrDependencyUnavailable propagates through StatusService.ChangeSlug.
	inner := fixtures.NewFakeHandoffStore()
	seedStateHandoff(t, inner, domain.StateSlugConflict)
	store := &errUpdateHandoffStore{inner}
	clk := fixtures.NewFakeClock()
	orch := service.NewHandoffOrchestrator(store, fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"), clk, 10*time.Millisecond)
	svc := service.NewStatusService(store, fixtures.NewFakeRateLimiter(), orch, clk, nil, 60)

	_, err := svc.ChangeSlug(context.Background(), "01JAB0000000000000000001", "01JAB0000000000000000001", "newslug")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

func TestChangeSlug_LockNotAcquired_ReturnsInvalidState(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	seedStateHandoff(t, handoffs, domain.StateSlugConflict)
	// Pre-acquire the lock so ChangeSlug cannot get it
	_, _, _ = handoffs.AcquireLock(context.Background(), "01JAB0000000000000000001")
	svc := newStatusSvc(handoffs, fixtures.NewFakeRateLimiter(), fixtures.NewFakeRealmProvisioner(""), fixtures.NewFakeOrgMembership(""))

	_, err := svc.ChangeSlug(context.Background(), "01JAB0000000000000000001", "01JAB0000000000000000001", "newslug")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInvalidState, de.Cause)
}

func TestChangeSlug_SignupIDMismatch(t *testing.T) {
	svc := newStatusSvc(fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(), fixtures.NewFakeRealmProvisioner(""), fixtures.NewFakeOrgMembership(""))
	_, err := svc.ChangeSlug(context.Background(), "session-id", "path-id", "newslug")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrSignupNotFound, de.Cause)
}

func TestChangeSlug_InvalidSlug(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	seedStateHandoff(t, handoffs, domain.StateSlugConflict)
	svc := newStatusSvc(handoffs, fixtures.NewFakeRateLimiter(), fixtures.NewFakeRealmProvisioner(""), fixtures.NewFakeOrgMembership(""))

	_, err := svc.ChangeSlug(context.Background(), "01JAB0000000000000000001", "01JAB0000000000000000001", "!!bad!!")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrValidationFailed, de.Cause)
}

func TestChangeSlug_HandoffNotFound(t *testing.T) {
	svc := newStatusSvc(fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(), fixtures.NewFakeRealmProvisioner(""), fixtures.NewFakeOrgMembership(""))
	_, err := svc.ChangeSlug(context.Background(), "no-id", "no-id", "newslug")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrSignupNotFound, de.Cause)
}

func TestChangeSlug_WrongState(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	seedStateHandoff(t, handoffs, domain.StateCompleted)
	svc := newStatusSvc(handoffs, fixtures.NewFakeRateLimiter(), fixtures.NewFakeRealmProvisioner(""), fixtures.NewFakeOrgMembership(""))

	_, err := svc.ChangeSlug(context.Background(), "01JAB0000000000000000001", "01JAB0000000000000000001", "newslug")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInvalidState, de.Cause)
}

// ── H-1 / H-2 metric path tests ──────────────────────────────────────────────
// Metrics are nil-safe in unit tests (not registered); these tests verify the
// correct service transitions are reached — the metric calls are side effects.

// TestGetStatus_ResumeExpired_FiresProvisioningFailedMetric exercises H-1:
// StateFailed transition (resume window exhausted) must reach IncProvisioningFailed.
func TestGetStatus_ResumeExpired_FiresProvisioningFailedMetric(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	clk := fixtures.NewFakeClock()
	h := &domain.Handoff{
		SignupID: "01JAB0000000000000000301", TenantID: "tid-301",
		State: domain.StateTenantPending, Slug: "acme", CompanyName: "Acme",
		Plan: domain.PlanPro, KeycloakUserID: "kc-001",
		CreatedAt: clk.Now().Add(-16 * time.Minute), UpdatedAt: clk.Now(),
	}
	require.NoError(t, handoffs.Create(context.Background(), h))
	svc := newStatusSvc(handoffs, fixtures.NewFakeRateLimiter(), fixtures.NewFakeRealmProvisioner(""), fixtures.NewFakeOrgMembership(""))

	result, err := svc.GetStatus(context.Background(), "01JAB0000000000000000301", "01JAB0000000000000000301", "sid")
	require.NoError(t, err)
	assert.Equal(t, domain.StateFailed, result.State)
}

// TestGetStatus_ResumeSucceeds_FiresResumeMetric exercises H-2 (strategy=resume):
// a completed I-1 resume must reach IncProvisioningSucceeded("trial","resume").
func TestGetStatus_ResumeSucceeds_FiresResumeMetric(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	seedStateHandoff(t, handoffs, domain.StateTenantPending)
	svc := newStatusSvc(handoffs, fixtures.NewFakeRateLimiter(), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid-r"))

	result, err := svc.GetStatus(context.Background(), "01JAB0000000000000000001", "01JAB0000000000000000001", "sid")
	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, result.State)
}

// TestChangeSlug_Completes_FiresSlugChangeMetric exercises H-2 (strategy=slug_change):
// a completed slug change must reach IncProvisioningSucceeded("trial","slug_change").
func TestChangeSlug_Completes_FiresSlugChangeMetric(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	seedStateHandoff(t, handoffs, domain.StateSlugConflict)
	svc := newStatusSvc(handoffs, fixtures.NewFakeRateLimiter(), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid-sc"))

	result, err := svc.ChangeSlug(context.Background(), "01JAB0000000000000000001", "01JAB0000000000000000001", "brand-new-slug")
	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, result.State)
}
