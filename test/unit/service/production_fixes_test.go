package service_test

// Fixes from the 2026-11-27 production-readiness review (LLD §19.4).

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/test/fixtures"
)

func reason(t *testing.T, err error) any {
	t.Helper()
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	return de.Details["reason"]
}

// ── Trial and paid never overwrite each other on one session (SB-INV-3) ─────

func TestCrossType_TrialSubmitNeverOverwritesPaidHandoff(t *testing.T) {
	for _, state := range []domain.HandoffState{domain.StatePaymentPending, domain.StatePaymentActionRequired, domain.StatePaymentConfirmed} {
		t.Run(string(state), func(t *testing.T) {
			p := newPaidHarness("01JAB00000000000000000E1",
				fixtures.FakeBillingStep{Err: domain.NewError(domain.ErrDependencyUnavailable, "timeout")})
			_, _ = p.submit(t)
			h := p.handoff(t)
			h.State = state
			require.NoError(t, p.handoffs.Update(context.Background(), h))

			trial := newSignupService(p.handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
				&fixtures.FakePasswordHasher{}, p.rp, p.om)
			_, err := trial.Submit(context.Background(), validInput(p.sess))
			assert.Equal(t, domain.ErrInvalidState, causeOf(t, err))
			assert.Equal(t, "other_signup_in_progress", reason(t, err))
			after := p.handoff(t)
			assert.Equal(t, h.TenantID, after.TenantID, "the paid handoff and its charge key survive")
			assert.Equal(t, domain.SignupTypePaid, after.Type)
		})
	}
}

func TestCrossType_PaidSubmitNeverOverwritesTrialHandoff(t *testing.T) {
	for _, state := range []domain.HandoffState{domain.StateAllocated, domain.StateIdentityCreated, domain.StateTenantPending, domain.StateCompleted} {
		t.Run(string(state), func(t *testing.T) {
			p := newPaidHarness("01JAB00000000000000000E2", confirmed("sub"))
			trial := newSignupService(p.handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
				&fixtures.FakePasswordHasher{}, fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"))
			_, err := trial.Submit(context.Background(), validInput(p.sess))
			require.NoError(t, err)
			h := p.handoff(t)
			assert.Equal(t, domain.SignupTypeTrial, h.Type, "trial handoffs record their type")
			h.State = state
			require.NoError(t, p.handoffs.Update(context.Background(), h))

			_, err = p.submit(t)
			assert.Equal(t, "other_signup_in_progress", reason(t, err))
			assert.Empty(t, p.billing.Calls, "nothing is charged")
			assert.Equal(t, h.TenantID, p.handoff(t).TenantID)
		})
	}
}

func TestCrossType_LegacyHandoffWithoutTypeCountsAsTrial(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000E3", confirmed("sub"))
	require.NoError(t, p.handoffs.Create(context.Background(), &domain.Handoff{
		SignupID: p.sess.SignupID, TenantID: "t-legacy", State: domain.StateIdentityCreated,
	}))
	_, err := p.submit(t)
	assert.Equal(t, "other_signup_in_progress", reason(t, err))
}

// ── I-1: the trial ledger hash is sent on the trial path only ────────────────

func TestI1EmailHash_TrialOnly(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000E4", confirmed("sub"))
	_, err := p.submit(t)
	require.NoError(t, err)
	require.Len(t, p.om.I1Calls, 1)
	assert.Empty(t, p.om.I1Calls[0].EmailHash, "a paid signup must not claim the one-trial ledger")

	om := fixtures.NewFakeOrgMembership("tid")
	trial := newSignupService(fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		&fixtures.FakePasswordHasher{}, fixtures.NewFakeRealmProvisioner("kc"), om)
	_, err = trial.Submit(context.Background(), validInput(&port.Session{SID: "s", SignupID: "01JAB00000000000000000E5", CSRFHash: "h"}))
	require.NoError(t, err)
	require.Len(t, om.I1Calls, 1)
	assert.NotEmpty(t, om.I1Calls[0].EmailHash)
}

// ── Billing: keep the handoff unless nothing payable exists ──────────────────

func TestBilling_UnexpectedReplyKeepsHandoff(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000E6", failing(domain.ErrInternal), confirmed("sub"))
	_, err := p.submit(t)
	assert.Equal(t, domain.ErrInternal, causeOf(t, err))
	h := p.handoff(t)
	require.NotNil(t, h, "an unexpected Billing reply may follow a charge: keep the key")

	_, err = p.submit(t)
	require.NoError(t, err)
	require.Len(t, p.billing.Calls, 2)
	assert.Equal(t, p.billing.Calls[0].IdempotencyKey, p.billing.Calls[1].IdempotencyKey)
}

// ── RP-2: key reuse never carries a different slug, name or email ────────────

func TestRP2_FreezesDetailsAfterUnknownOutcome(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000E7", confirmed("sub"))
	p.rp.DirectErr = domain.NewError(domain.ErrDependencyUnavailable, "rp timeout")
	_, err := p.submit(t)
	assert.Equal(t, domain.ErrDependencyUnavailable, causeOf(t, err))
	h := p.handoff(t)
	require.True(t, h.RP2Attempted)
	assert.Equal(t, domain.StatePaymentConfirmed, h.State)

	for name, mutate := range map[string]func(*paidInput){
		"slug":  func(in *paidInput) { in.Slug = "acme-new" },
		"name":  func(in *paidInput) { in.CompanyName = "Acme Renamed" },
		"email": func(in *paidInput) { in.AdminEmail = "new@acme.example" },
	} {
		_, err = p.submit(t, mutate)
		assert.Equal(t, "details_changed", reason(t, err), name)
	}
	assert.Len(t, p.rp.DirectCalls, 1, "no RP-2 call with a changed body under the same key")

	p.rp.DirectErr = nil
	res, err := p.submit(t)
	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, res.State)
	assert.Len(t, p.billing.Calls, 1)
}

func TestRP2_DefinitiveRefusalLiftsTheFreeze(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000E8", confirmed("sub"))
	p.rp.DirectErr = domain.NewError(domain.ErrEmailUnavailable, "email in use")
	_, err := p.submit(t)
	assert.Equal(t, domain.ErrEmailUnavailable, causeOf(t, err))
	assert.False(t, p.handoff(t).RP2Attempted)

	p.rp.DirectErr = nil
	res, err := p.submit(t, func(in *paidInput) { in.AdminEmail = "other@acme.example" })
	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, res.State)
}

func TestRP2_RealmAlreadyDedicatedIsASlugConflict(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000E9", confirmed("sub"))
	p.rp.DirectErr = domain.NewError(domain.ErrSlugTaken, "realm exists")
	res, err := p.submit(t)
	assert.Equal(t, domain.ErrSlugTaken, causeOf(t, err))
	require.NotNil(t, res)
	assert.NotEmpty(t, res.SlugSuggestions)
	assert.Equal(t, domain.StatePaymentConfirmed, p.handoff(t).State)

	p.rp.DirectErr = nil
	res, err = p.submit(t, func(in *paidInput) { in.Slug = "acme-two" })
	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, res.State)
	assert.Equal(t, "acme-two", p.rp.DirectCalls[1].TenantSlug)
	assert.Len(t, p.billing.Calls, 1)
}

func TestRP2_OperationInProgressIsRetryable(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000EA", confirmed("sub"))
	p.rp.DirectErr = domain.NewError(domain.ErrOperationInProgress, "busy")
	_, err := p.submit(t)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
	assert.Equal(t, 2, de.Details["retry_after_seconds"])
}

func TestRP2_EmailVerificationSentIsRead(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000EB", confirmed("sub"))
	no := false
	p.rp.DirectResp = &port.ProvisionDirectResponse{TenantID: "tid", KeycloakUserID: "kc", EmailVerificationSent: &no}
	res, err := p.submit(t)
	require.NoError(t, err)
	assert.False(t, res.EmailVerificationSent)
}

// ── Validation and cost ──────────────────────────────────────────────────────

func TestPaid_SeatsAbove1000Rejected(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000EC", confirmed("sub"))
	_, err := p.submit(t, func(in *paidInput) { in.Seats = 1001 })
	assert.Equal(t, domain.ErrValidationFailed, causeOf(t, err))
	assert.Empty(t, p.billing.Calls)
}

func TestPaid_MismatchedResumeIsRejectedBeforeHashing(t *testing.T) {
	billing := fixtures.NewFakeBillingClient("sub")
	billing.Script = []fixtures.FakeBillingStep{actionRequired()}
	hasher := &fixtures.FakePasswordHasher{}
	handoffs := fixtures.NewFakeHandoffStore()
	clk := fixtures.NewFakeClock()
	rp, om := fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid")
	svc := service.NewPaidSignupService(handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{}, hasher,
		billing, rp, om, service.NewHandoffOrchestrator(handoffs, rp, om, clk, 10*time.Millisecond),
		fixtures.NewFakeIDGenerator(), clk, []byte("test-hmac-key-32-bytes-minimum!!"), nil,
		domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true}, "2026-09")
	sess := &port.Session{SID: "s", SignupID: "01JAB00000000000000000ED", CSRFHash: "h"}

	_, err := svc.Submit(context.Background(), validPaidInput(sess))
	require.NoError(t, err)
	require.Equal(t, 1, hasher.Calls)

	in := validPaidInput(sess)
	in.PaymentToken = "pm_other"
	_, err = svc.Submit(context.Background(), in)
	assert.Equal(t, "details_changed", reason(t, err))
	assert.Equal(t, 1, hasher.Calls, "no PBKDF2 for a resume that cannot match")
}

// SB-3 tells the browser what moves a paid handoff on: nothing runs in the
// background for these states.
func TestStatus_PaymentStatesCarryAnAction(t *testing.T) {
	for state, want := range map[domain.HandoffState]string{
		domain.StatePaymentActionRequired: "complete_payment",
		domain.StatePaymentPending:        "resubmit",
		domain.StatePaymentConfirmed:      "resubmit",
	} {
		handoffs := fixtures.NewFakeHandoffStore()
		clk := fixtures.NewFakeClock()
		require.NoError(t, handoffs.Create(context.Background(), &domain.Handoff{
			SignupID: "01JAB00000000000000000F1", TenantID: "t", Type: domain.SignupTypePaid, State: state,
		}))
		rp, om := fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("t")
		svc := service.NewStatusService(handoffs, fixtures.NewFakeRateLimiter(),
			service.NewHandoffOrchestrator(handoffs, rp, om, clk, 10*time.Millisecond), clk, nil, 60)
		res, err := svc.GetStatus(context.Background(), "01JAB00000000000000000F1", "01JAB00000000000000000F1", "sid")
		require.NoError(t, err)
		assert.Equal(t, want, res.Action, string(state))
	}
}
