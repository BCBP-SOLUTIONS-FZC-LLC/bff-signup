package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/test/fixtures"
)

func slugConflictHandoff(signupID string) *domain.Handoff {
	clk := fixtures.NewFakeClock()
	return &domain.Handoff{
		SignupID:       signupID,
		TenantID:       "tid-" + signupID,
		State:          domain.StateSlugConflict,
		KeycloakUserID: "kc-001",
		Slug:           "acme",
		CompanyName:    "Acme",
		Plan:           domain.PlanPro,
		CreatedAt:      clk.Now(),
		UpdatedAt:      clk.Now(),
	}
}

// SB4-12 / OBS-31: when I-1 is down or slow during a slug change, the signup must
// move to tenant_pending (so SB-3 resumes it) and SB-4 answers 202, not an error.
func TestOrchestrator_ChangeSlug_I1Unavailable_ParksAsTenantPending(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	om := fixtures.NewFakeOrgMembership("tid")
	om.I1Err = domain.NewError(domain.ErrDependencyUnavailable, "O&M temporarily unavailable")

	h := slugConflictHandoff("01JAB0000000000000000070")
	require.NoError(t, handoffs.Create(context.Background(), h))

	orch := newOrchestrator(handoffs, fixtures.NewFakeRealmProvisioner("kc"), om)
	result, err := orch.ChangeSlug(context.Background(), h, "acme-new")

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, domain.StateTenantPending, result.State)
	assert.Len(t, om.I1Calls, 1)

	stored, _ := handoffs.Get(context.Background(), h.SignupID)
	require.NotNil(t, stored)
	assert.Equal(t, domain.StateTenantPending, stored.State)
	assert.Equal(t, "acme-new", stored.Slug)
}

// An unexpected I-1 4xx during a slug change is a contract error: fail fast.
func TestOrchestrator_ChangeSlug_ContractError_FailsFast(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	om := fixtures.NewFakeOrgMembership("tid")
	om.I1Err = domain.NewError(domain.ErrInternal, "unexpected I-1 status 400")

	h := slugConflictHandoff("01JAB0000000000000000071")
	require.NoError(t, handoffs.Create(context.Background(), h))

	orch := newOrchestrator(handoffs, fixtures.NewFakeRealmProvisioner("kc"), om)
	result, err := orch.ChangeSlug(context.Background(), h, "acme-new")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Len(t, om.I1Calls, 1)
}

// Control: a slug that is taken again still returns the conflict with suggestions.
func TestOrchestrator_ChangeSlug_SlugTakenAgain_StillConflict(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	om := fixtures.NewFakeOrgMembership("tid")
	om.I1Err = domain.NewError(domain.ErrSlugTaken, "taken")

	h := slugConflictHandoff("01JAB0000000000000000072")
	require.NoError(t, handoffs.Create(context.Background(), h))

	orch := newOrchestrator(handoffs, fixtures.NewFakeRealmProvisioner("kc"), om)
	result, err := orch.ChangeSlug(context.Background(), h, "acme-new")

	require.Error(t, err)
	require.NotNil(t, result)
	assert.Equal(t, domain.StateSlugConflict, result.State)
}
