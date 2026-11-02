package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/test/fixtures"
)

func identityCreatedHandoff(signupID string) *domain.Handoff {
	clk := fixtures.NewFakeClock()
	return &domain.Handoff{
		SignupID:       signupID,
		TenantID:       "tid-" + signupID,
		State:          domain.StateIdentityCreated,
		KeycloakUserID: "kc-001",
		Slug:           "acme",
		CompanyName:    "Acme",
		Plan:           domain.PlanPro,
		CreatedAt:      clk.Now(),
		UpdatedAt:      clk.Now(),
	}
}

// SB2-48 / OBS-27: an unexpected I-1 4xx is a contract error (LLD §9.5). It must
// fail fast: one call only, an error returned (→ 500), state kept at identity_created.
func TestOrchestrator_RunI1_ContractError_FailsFastWithoutRetry(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	om := fixtures.NewFakeOrgMembership("tid")
	om.I1Err = domain.NewError(domain.ErrInternal, "unexpected I-1 status 400")

	h := identityCreatedHandoff("01JAB0000000000000000048")
	require.NoError(t, handoffs.Create(context.Background(), h))

	orch := newOrchestrator(handoffs, fixtures.NewFakeRealmProvisioner("kc"), om)
	result, err := orch.Run(context.Background(), orchInput(h))

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Len(t, om.I1Calls, 1, "a contract error must not be retried")
	assert.Equal(t, domain.StateIdentityCreated, h.State)
}

// Control: a transient I-1 failure (5xx / timeout) is still retried inline and
// ends as tenant_pending (202), exactly as before.
func TestOrchestrator_RunI1_TransientError_StillRetries(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	om := fixtures.NewFakeOrgMembership("tid")
	om.I1Err = domain.NewError(domain.ErrDependencyUnavailable, "O&M temporarily unavailable")

	h := identityCreatedHandoff("01JAB0000000000000000049")
	require.NoError(t, handoffs.Create(context.Background(), h))

	orch := newOrchestrator(handoffs, fixtures.NewFakeRealmProvisioner("kc"), om)
	result, err := orch.Run(context.Background(), orchInput(h))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, domain.StateTenantPending, result.State)
	assert.Len(t, om.I1Calls, 3, "1 attempt + 2 inline retries")
}
