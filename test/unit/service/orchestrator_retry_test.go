package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/test/fixtures"
)

// LLD §17.2: RP-1 502/503 and eligibility 5xx are retried once; RP-1 409
// OPERATION_IN_PROGRESS is retried once (same Idempotency-Key).

func isCause(err, cause error) bool {
	var de *domain.DomainError
	return errors.As(err, &de) && errors.Is(de.Cause, cause)
}

func rpUnavailable() error { return domain.NewError(domain.ErrDependencyUnavailable, "RP-1 down") }
func rpInProgress() error  { return domain.NewError(domain.ErrOperationInProgress, "in progress") }

func runRetry(t *testing.T, rp *fixtures.FakeRealmProvisioner, om *fixtures.FakeOrgMembership) error {
	t.Helper()
	handoffs := fixtures.NewFakeHandoffStore()
	h := allocatedHandoff()
	require.NoError(t, handoffs.Create(context.Background(), h))
	_, err := newOrchestrator(handoffs, rp, om).Run(context.Background(), orchInput(h))
	return err
}

func TestOrchestrator_RP1Unavailable_RetriedOnceThenSucceeds(t *testing.T) {
	rp := fixtures.NewFakeRealmProvisioner("kc")
	rp.ErrSeq = []error{rpUnavailable()}

	err := runRetry(t, rp, fixtures.NewFakeOrgMembership("tid"))

	require.NoError(t, err)
	assert.Len(t, rp.Calls, 2)
}

func TestOrchestrator_RP1Unavailable_GivesUpAfterOneRetry(t *testing.T) {
	rp := fixtures.NewFakeRealmProvisioner("kc")
	rp.Err = rpUnavailable()

	err := runRetry(t, rp, fixtures.NewFakeOrgMembership("tid"))

	require.Error(t, err)
	assert.True(t, isCause(err, domain.ErrDependencyUnavailable))
	assert.Len(t, rp.Calls, 2, "1 attempt + 1 retry")
}

func TestOrchestrator_RP1InProgress_RetriedOnceThenSucceeds(t *testing.T) {
	rp := fixtures.NewFakeRealmProvisioner("kc")
	rp.ErrSeq = []error{rpInProgress()}

	err := runRetry(t, rp, fixtures.NewFakeOrgMembership("tid"))

	require.NoError(t, err)
	assert.Len(t, rp.Calls, 2)
}

func TestOrchestrator_RP1InProgress_GivesUpAs503AfterOneRetry(t *testing.T) {
	rp := fixtures.NewFakeRealmProvisioner("kc")
	rp.Err = rpInProgress()

	err := runRetry(t, rp, fixtures.NewFakeOrgMembership("tid"))

	require.Error(t, err)
	assert.True(t, isCause(err, domain.ErrDependencyUnavailable), "the user sees 503, not 409")
	assert.Len(t, rp.Calls, 2, "1 attempt + 1 retry")
}

func TestOrchestrator_RP1OtherError_NotRetried(t *testing.T) {
	rp := fixtures.NewFakeRealmProvisioner("kc")
	rp.Err = domain.NewError(domain.ErrInternal, "RP-1 rejected request")

	err := runRetry(t, rp, fixtures.NewFakeOrgMembership("tid"))

	require.Error(t, err)
	assert.Len(t, rp.Calls, 1)
}

func TestOrchestrator_EligibilityUnavailable_RetriedOnceThenSucceeds(t *testing.T) {
	om := fixtures.NewFakeOrgMembership("tid")
	om.EligibleSeq = []error{domain.NewError(domain.ErrDependencyUnavailable, "O&M down")}

	err := runRetry(t, fixtures.NewFakeRealmProvisioner("kc"), om)

	require.NoError(t, err)
	assert.Equal(t, 2, om.EligibleCalls)
}

func TestOrchestrator_EligibilityUnavailable_GivesUpAfterOneRetry(t *testing.T) {
	om := fixtures.NewFakeOrgMembership("tid")
	om.EligibleErr = domain.NewError(domain.ErrDependencyUnavailable, "O&M down")
	rp := fixtures.NewFakeRealmProvisioner("kc")

	err := runRetry(t, rp, om)

	require.Error(t, err)
	assert.Equal(t, 2, om.EligibleCalls)
	assert.Empty(t, rp.Calls, "RP-1 is never called when eligibility is unknown")
}

func TestOrchestrator_EligibilityNotEligible_NotRetried(t *testing.T) {
	om := fixtures.NewFakeOrgMembership("tid")
	om.EligibleErr = domain.NewError(domain.ErrTrialNotEligible, "used")

	err := runRetry(t, fixtures.NewFakeRealmProvisioner("kc"), om)

	require.Error(t, err)
	assert.Equal(t, 1, om.EligibleCalls)
}
