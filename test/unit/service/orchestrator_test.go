package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/test/fixtures"
)

func newOrchestrator(
	handoffs *fixtures.FakeHandoffStore,
	rp *fixtures.FakeRealmProvisioner,
	om *fixtures.FakeOrgMembership,
) *service.HandoffOrchestrator {
	return service.NewHandoffOrchestrator(handoffs, rp, om, fixtures.NewFakeClock(), 10*time.Millisecond)
}

func allocatedHandoff() *domain.Handoff {
	clk := fixtures.NewFakeClock()
	return &domain.Handoff{
		SignupID:       "01JAB0000000000000000002",
		TenantID:       "tenant-uuid-002",
		State:          domain.StateAllocated,
		Slug:           "acme",
		CompanyName:    "Acme Corp",
		Plan:           domain.PlanPro,
		AdminEmailHMAC: "hmac-001",
		CreatedAt:      clk.Now(),
		UpdatedAt:      clk.Now(),
	}
}

func orchInput(h *domain.Handoff) service.OrchestratorInput {
	return service.OrchestratorInput{
		Handoff:            h,
		AdminEmail:         "owner@acme.example",
		AdminName:          "Dana Owner",
		HashedPasswordSeed: `{"algorithm":"pbkdf2-sha512","iterations":210000,"salt":"AA==","hash":"AA=="}`,
	}
}

func TestOrchestrator_PUB1SlugConflict(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	om := fixtures.NewFakeOrgMembership("tid")
	om.SlugFreeErr = domain.NewError(domain.ErrSlugTaken, "slug already taken at PUB-1")
	rp := fixtures.NewFakeRealmProvisioner("kc")

	h := allocatedHandoff()
	require.NoError(t, handoffs.Create(context.Background(), h))

	orch := newOrchestrator(handoffs, rp, om)
	result, err := orch.Run(context.Background(), orchInput(h))

	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrSlugTaken, de.Cause)
	require.NotNil(t, result)
	assert.Equal(t, domain.StateSlugConflict, result.State)
	assert.NotEmpty(t, result.SlugSuggestions)
	// RP-1 must NOT have been called (slug conflict stopped provisioning before it)
	assert.Empty(t, rp.Calls)
}

func TestOrchestrator_RP1EmailUnavailable(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	rp := fixtures.NewFakeRealmProvisioner("kc")
	rp.Err = domain.NewError(domain.ErrEmailUnavailable, "email already in use")

	h := allocatedHandoff()
	require.NoError(t, handoffs.Create(context.Background(), h))

	orch := newOrchestrator(handoffs, rp, fixtures.NewFakeOrgMembership("tid"))
	_, err := orch.Run(context.Background(), orchInput(h))

	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrEmailUnavailable, de.Cause)

	// Handoff must have been deleted (ErrEmailUnavailable cleanup)
	got, _ := handoffs.Get(context.Background(), h.SignupID)
	assert.Nil(t, got, "handoff must be deleted after email unavailable")
}

func TestOrchestrator_ResumeI1_HappyPath(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	om := fixtures.NewFakeOrgMembership("tid")
	clk := fixtures.NewFakeClock()

	h := &domain.Handoff{
		SignupID:       "01JAB0000000000000000003",
		TenantID:       "tid",
		State:          domain.StateTenantPending,
		KeycloakUserID: "kc-001",
		Slug:           "acme",
		CompanyName:    "Acme",
		Plan:           domain.PlanPro,
		CreatedAt:      clk.Now(), // within 15-min window
		UpdatedAt:      clk.Now(),
	}
	require.NoError(t, handoffs.Create(context.Background(), h))

	orch := newOrchestrator(handoffs, fixtures.NewFakeRealmProvisioner("kc"), om)
	result, err := orch.ResumeI1(context.Background(), h)

	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, result.State)
}

func TestOrchestrator_ResumeI1_WrongState(t *testing.T) {
	orch := newOrchestrator(fixtures.NewFakeHandoffStore(), fixtures.NewFakeRealmProvisioner(""), fixtures.NewFakeOrgMembership(""))
	h := &domain.Handoff{State: domain.StateCompleted}

	_, err := orch.ResumeI1(context.Background(), h)
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInvalidState, de.Cause)
}

func TestOrchestrator_ChangeSlug_WrongState_ReturnsInvalidState(t *testing.T) {
	// CanChangeSlug() returns false for non-slug_conflict states.
	// This path is only reachable by calling the orchestrator directly.
	orch := newOrchestrator(fixtures.NewFakeHandoffStore(), fixtures.NewFakeRealmProvisioner(""), fixtures.NewFakeOrgMembership(""))
	h := &domain.Handoff{State: domain.StateCompleted} // CanChangeSlug() = false

	_, err := orch.ChangeSlug(context.Background(), h, "newslug")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInvalidState, de.Cause)
}

func TestOrchestrator_ChangeSlug_NoKeycloakUser(t *testing.T) {
	orch := newOrchestrator(fixtures.NewFakeHandoffStore(), fixtures.NewFakeRealmProvisioner(""), fixtures.NewFakeOrgMembership(""))
	h := &domain.Handoff{
		State:          domain.StateSlugConflict,
		KeycloakUserID: "", // PUB-1 path — RP-1 never ran
	}

	_, err := orch.ChangeSlug(context.Background(), h, "newslug")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInvalidState, de.Cause)
}

func TestOrchestrator_ChangeSlug_MaxAttempts(t *testing.T) {
	orch := newOrchestrator(fixtures.NewFakeHandoffStore(), fixtures.NewFakeRealmProvisioner(""), fixtures.NewFakeOrgMembership(""))
	h := &domain.Handoff{
		State:          domain.StateSlugConflict,
		KeycloakUserID: "kc-001",
		SlugAttempts:   5, // maxSlugAttempts = 5
	}

	_, err := orch.ChangeSlug(context.Background(), h, "newslug")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrInvalidState, de.Cause)
}

func TestOrchestrator_Run_ResumePath_IdentityCreated(t *testing.T) {
	// Exercises the "already past allocated" branch of Run (resume path, LLD §8.1).
	handoffs := fixtures.NewFakeHandoffStore()
	om := fixtures.NewFakeOrgMembership("tid")
	clk := fixtures.NewFakeClock()
	h := &domain.Handoff{
		SignupID:       "01JAB0000000000000000005",
		TenantID:       "tid-005",
		State:          domain.StateIdentityCreated, // not allocated → resume path
		KeycloakUserID: "kc-001",
		Slug:           "acme",
		CompanyName:    "Acme",
		Plan:           domain.PlanPro,
		CreatedAt:      clk.Now(),
		UpdatedAt:      clk.Now(),
	}
	require.NoError(t, handoffs.Create(context.Background(), h))

	orch := newOrchestrator(handoffs, fixtures.NewFakeRealmProvisioner("kc"), om)
	result, err := orch.Run(context.Background(), orchInput(h))
	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, result.State)
	assert.Len(t, om.I1Calls, 1)
}

// ── runI1 context.Done() branch (ctx cancelled before first attempt) ───────────

func TestOrchestrator_RunI1_ContextCancelled_ReturnsTenantPending(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	om := fixtures.NewFakeOrgMembership("tid")
	// Make I-1 always fail so runI1 exhausts retries normally — then we cancel.
	om.I1Err = domain.NewError(domain.ErrDependencyUnavailable, "i1 down")
	clk := fixtures.NewFakeClock()

	h := &domain.Handoff{
		SignupID:       "01JAB0000000000000000006",
		TenantID:       "tid-006",
		State:          domain.StateIdentityCreated,
		KeycloakUserID: "kc-001",
		Slug:           "acme",
		CompanyName:    "Acme",
		Plan:           domain.PlanPro,
		CreatedAt:      clk.Now(),
		UpdatedAt:      clk.Now(),
	}
	require.NoError(t, handoffs.Create(context.Background(), h))

	// Use a pre-cancelled context so ctx.Err() != nil on the very first iteration
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	orch := newOrchestrator(handoffs, fixtures.NewFakeRealmProvisioner("kc"), om)
	result, err := orch.Run(ctx, orchInput(h))

	// With a cancelled context the loop breaks immediately → StateTenantPending
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, domain.StateTenantPending, result.State)
}

func TestOrchestrator_ChangeSlug_HappyPath(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	om := fixtures.NewFakeOrgMembership("tid")
	clk := fixtures.NewFakeClock()

	h := &domain.Handoff{
		SignupID:       "01JAB0000000000000000004",
		TenantID:       "tid",
		State:          domain.StateSlugConflict,
		KeycloakUserID: "kc-001",
		Slug:           "acme",
		CompanyName:    "Acme",
		Plan:           domain.PlanPro,
		SlugAttempts:   1,
		CreatedAt:      clk.Now(),
		UpdatedAt:      clk.Now(),
	}
	require.NoError(t, handoffs.Create(context.Background(), h))

	orch := newOrchestrator(handoffs, fixtures.NewFakeRealmProvisioner("kc"), om)
	result, err := orch.ChangeSlug(context.Background(), h, "acme-2")

	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, result.State)
	require.Len(t, om.I1Calls, 1)
	assert.Equal(t, "acme-2", om.I1Calls[0].Slug)
}

// provisionRP1 is unexported, so these tests reach it through Run.
// When the request context ends while the orchestrator waits to retry an
// in-progress 409, the user must get ErrDependencyUnavailable (503) and never
// the internal ErrOperationInProgress (which has no HTTP mapping).
func TestProvisionRP1_ContextCancelledDuringInProgressRetry_ReturnsDependencyUnavailable(t *testing.T) {
	rp := fixtures.NewFakeRealmProvisioner("kc")
	rp.Err = rpInProgress()

	err := runWithContext(cancelledContext(), t, fixtures.NewFakeHandoffStore(), rp, fixtures.NewFakeOrgMembership("tid"))

	require.Error(t, err)
	assert.True(t, isCause(err, domain.ErrDependencyUnavailable))
	assert.False(t, isCause(err, domain.ErrOperationInProgress), "ErrOperationInProgress must never reach the user")
	assert.Len(t, rp.Calls, 1, "no retry once the context is cancelled")
}

func TestProvisionRP1_DeadlineExpiresDuringInProgressRetryWait_ReturnsDependencyUnavailable(t *testing.T) {
	rp := fixtures.NewFakeRealmProvisioner("kc")
	rp.Err = rpInProgress()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := runWithContext(ctx, t, fixtures.NewFakeHandoffStore(), rp, fixtures.NewFakeOrgMembership("tid"))

	require.Error(t, err)
	assert.True(t, isCause(err, domain.ErrDependencyUnavailable))
	assert.False(t, isCause(err, domain.ErrOperationInProgress), "ErrOperationInProgress must never reach the user")
	assert.Len(t, rp.Calls, 1, "the deadline cuts the retry wait short, so there is no second call")
}
