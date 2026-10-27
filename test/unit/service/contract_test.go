package service_test

// LLD §14.3 Contract tests — enforced in the unit tier so a BFF bug is caught
// before it reaches RP-1 or I-1.
//
//  1. Plan contract — BFF never sends "trial" to RP-1 or I-1 (SB-INV-8).
//  2. Same-values contract — for every completed signup RP-1 and I-1 carry
//     identical plan, name and slug (SB-INV-8).
//
// Header contract (x-user-id=…00a1, x-tenant-roles=iam-system, Idempotency-Key)
// is verified at the HTTP adapter level in test/unit/adapter/realmprovisioner_test.go
// and test/unit/adapter/orgmembership_test.go (LLD §14.3 bullet 4).

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/test/fixtures"
)

// TestContract_PlanNeverTrial — LLD §14.3 plan contract:
// "every plan the BFF can send is starter/pro/enterprise; a test asserts the
// BFF never sends 'trial'."
func TestContract_PlanNeverTrial(t *testing.T) {
	invalidPlans := []domain.Plan{"trial", "", "free", "TRIAL", "basic"}
	for _, badPlan := range invalidPlans {
		svc := newSignupService(
			fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(),
			&fixtures.FakeCaptchaVerifier{}, &fixtures.FakePasswordHasher{},
			fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		)
		sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000070", CSRFHash: "h"}
		input := validInput(sess)
		input.Plan = badPlan

		_, err := svc.Submit(context.Background(), input)
		require.Error(t, err, "plan %q must be rejected before any IAM call", badPlan)
		var de *domain.DomainError
		require.ErrorAs(t, err, &de)
		assert.Equal(t, domain.ErrValidationFailed, de.Cause,
			"plan %q must fail with validation_failed (BFF must never send 'trial' to RP-1/I-1 — SB-INV-8)", badPlan)
	}
}

// TestContract_ValidPlansForwarded — companion to PlanNeverTrial:
// each catalogue plan reaches RP-1 and I-1 unchanged, never remapped.
func TestContract_ValidPlansForwarded(t *testing.T) {
	validPlans := []domain.Plan{domain.PlanStarter, domain.PlanPro, domain.PlanEnterprise}
	for i, plan := range validPlans {
		rp := fixtures.NewFakeRealmProvisioner("kc")
		om := fixtures.NewFakeOrgMembership("tid")
		svc := newSignupService(
			fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(),
			&fixtures.FakeCaptchaVerifier{}, &fixtures.FakePasswordHasher{}, rp, om,
		)
		sess := &port.Session{
			SID:      "sid",
			SignupID: fmt.Sprintf("01JAB000000000000000007%d", i+1),
			CSRFHash: "h",
		}
		input := validInput(sess)
		input.Plan = plan

		_, err := svc.Submit(context.Background(), input)
		require.NoError(t, err, "plan %q must be accepted", plan)
		require.Len(t, rp.Calls, 1)
		assert.Equal(t, string(plan), rp.Calls[0].Plan,
			"RP-1 must receive plan %q unchanged (SB-INV-8)", plan)
		require.Len(t, om.I1Calls, 1)
		assert.Equal(t, string(plan), om.I1Calls[0].Plan,
			"I-1 must receive the same plan as RP-1 (SB-INV-8)")
	}
}

// TestContract_SameValuesSentToRP1AndI1 — LLD §14.3 same-values contract:
// "for every completed signup RP-1 and I-1 bodies carry identical plan, name
// and slug." Enforces SB-INV-8.
func TestContract_SameValuesSentToRP1AndI1(t *testing.T) {
	rp := fixtures.NewFakeRealmProvisioner("kc")
	om := fixtures.NewFakeOrgMembership("tid")
	svc := newSignupService(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(),
		&fixtures.FakeCaptchaVerifier{}, &fixtures.FakePasswordHasher{}, rp, om,
	)
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000080", CSRFHash: "h"}

	_, err := svc.Submit(context.Background(), validInput(sess))
	require.NoError(t, err)
	require.Len(t, rp.Calls, 1)
	require.Len(t, om.I1Calls, 1)

	rp1 := rp.Calls[0]
	i1 := om.I1Calls[0]

	assert.Equal(t, rp1.Plan, i1.Plan,
		"SB-INV-8: plan must be identical on RP-1 and I-1")
	assert.Equal(t, rp1.TenantName, i1.Name,
		"SB-INV-8: company name must be identical on RP-1 and I-1")
	assert.Equal(t, rp1.TenantSlug, i1.Slug,
		"SB-INV-8: slug must be identical on RP-1 and I-1")
	assert.NotEqual(t, "trial", rp1.Plan,
		"SB-INV-8: plan must never be 'trial'")
}
