package service_test

// BL-OQ-8: SB-5 against Billing B-I1 — retries, 3-D Secure, declines.

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

type (
	paidInput  = service.PaidSignupInput
	paidResult = service.PaidSignupResult
)

type paidHarness struct {
	handoffs *fixtures.FakeHandoffStore
	limiter  *fixtures.FakeRateLimiter
	captcha  *fixtures.FakeCaptchaVerifier
	billing  *fixtures.FakeBillingClient
	rp       *fixtures.FakeRealmProvisioner
	om       *fixtures.FakeOrgMembership
	sess     *port.Session
	svc      *service.PaidSignupService
}

func newPaidHarness(signupID string, steps ...fixtures.FakeBillingStep) *paidHarness {
	billing := fixtures.NewFakeBillingClient("sub-paid")
	billing.Script = steps
	p := &paidHarness{
		handoffs: fixtures.NewFakeHandoffStore(),
		limiter:  fixtures.NewFakeRateLimiter(),
		captcha:  &fixtures.FakeCaptchaVerifier{},
		billing:  billing,
		rp:       fixtures.NewFakeRealmProvisioner("kc"),
		om:       fixtures.NewFakeOrgMembership("tid"),
		sess:     &port.Session{SID: "sid", SignupID: signupID, CSRFHash: "h"},
	}
	p.svc = newPaidSignupSvc(p.handoffs, p.limiter, p.captcha, p.billing, p.rp, p.om)
	return p
}

func (p *paidHarness) submit(t *testing.T, mutate ...func(*paidInput)) (*paidResult, error) {
	t.Helper()
	in := validPaidInput(p.sess)
	for _, m := range mutate {
		m(&in)
	}
	return p.svc.Submit(context.Background(), in)
}

func (p *paidHarness) handoff(t *testing.T) *domain.Handoff {
	t.Helper()
	h, err := p.handoffs.Get(context.Background(), p.sess.SignupID)
	require.NoError(t, err)
	return h
}

func actionRequired() fixtures.FakeBillingStep {
	return fixtures.FakeBillingStep{Resp: &port.ChargeResponse{
		Outcome:               port.ChargeActionRequired,
		BillingSubscriptionID: "sub-3ds",
		Action:                &port.PaymentAction{Type: "3ds", ClientSecret: "pi_secret"},
		ExpiresAt:             "2026-11-26T12:30:00Z",
	}}
}

func confirmed(id string) fixtures.FakeBillingStep {
	return fixtures.FakeBillingStep{Resp: &port.ChargeResponse{Outcome: port.ChargeConfirmed, BillingSubscriptionID: id}}
}

func failing(cause error) fixtures.FakeBillingStep {
	return fixtures.FakeBillingStep{Err: domain.NewError(cause, "billing says no")}
}

func causeOf(t *testing.T, err error) error {
	t.Helper()
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	return de.Cause
}

// ── 3-D Secure ────────────────────────────────────────────────────────────────

func TestPaidBilling_3DS_ReturnsActionThenResumesSameKeyAndBody(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000B1", actionRequired(), confirmed("sub-3ds"))

	res, err := p.submit(t)
	require.NoError(t, err)
	assert.Equal(t, domain.StatePaymentActionRequired, res.State)
	require.NotNil(t, res.PaymentAction)
	assert.Equal(t, "pi_secret", res.PaymentAction.ClientSecret)
	assert.Equal(t, "2026-11-26T12:30:00Z", res.PaymentExpiresAt)
	assert.Empty(t, p.rp.DirectCalls, "RP-2 must not run before payment is confirmed")

	h := p.handoff(t)
	assert.Equal(t, domain.StatePaymentActionRequired, h.State)
	assert.NotEmpty(t, h.PaymentRefHMAC)
	assert.NotContains(t, h.PaymentRefHMAC, "tok_", "the payment token itself is never stored")

	// Browser completes 3-D Secure, then re-POSTs the same body with a fresh CAPTCHA.
	res, err = p.submit(t, func(in *paidInput) { in.CaptchaToken = "fresh-token" })
	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, res.State)

	require.Len(t, p.billing.Calls, 2)
	assert.Equal(t, p.billing.Calls[0], p.billing.Calls[1], "retry must repeat tenant_id, key and body")
	assert.Equal(t, "charge:"+h.TenantID, p.billing.Calls[1].IdempotencyKey)
	require.Len(t, p.rp.DirectCalls, 1)
	assert.Equal(t, "sub-3ds", p.rp.DirectCalls[0].BillingSubscriptionID)
	assert.Equal(t, h.TenantID, p.rp.DirectCalls[0].TenantID)
}

func TestPaidBilling_3DS_ResumeUsesNoRateLimitBudget(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000B2", actionRequired(), actionRequired(), actionRequired(), confirmed("sub-3ds"))
	p.limiter.IPLimit = 1

	for range 3 {
		res, err := p.submit(t)
		require.NoError(t, err)
		assert.Equal(t, domain.StatePaymentActionRequired, res.State)
	}
	res, err := p.submit(t)
	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, res.State)
}

func TestPaidBilling_3DS_ResumeStillNeedsCaptcha(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000B3", actionRequired())
	_, err := p.submit(t)
	require.NoError(t, err)

	p.captcha.Err = domain.NewError(domain.ErrCaptchaFailed, "bad token")
	_, err = p.submit(t)
	assert.Equal(t, domain.ErrCaptchaFailed, causeOf(t, err))
	assert.Len(t, p.billing.Calls, 1, "no Billing call without CAPTCHA (SB-INV-5)")
}

func TestPaidBilling_Resume_RejectsChangedPaymentDetails(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*paidInput)
	}{
		{"payment method", func(in *paidInput) { in.PaymentToken = "pm_other_card" }},
		{"plan", func(in *paidInput) { in.Plan = domain.PlanEnterprise }},
		{"seats", func(in *paidInput) { in.Seats = 6 }},
		{"email", func(in *paidInput) { in.AdminEmail = "someone.else@acme.example" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newPaidHarness("01JAB00000000000000000B4", actionRequired())
			_, err := p.submit(t)
			require.NoError(t, err)

			_, err = p.submit(t, tc.mutate)
			assert.Equal(t, domain.ErrInvalidState, causeOf(t, err))
			assert.Len(t, p.billing.Calls, 1, "Billing must not see a different body under the same key")
			assert.Equal(t, domain.StatePaymentActionRequired, p.handoff(t).State, "attempt in flight is kept")
		})
	}
}

func TestPaidBilling_Resume_TakesNewSlugAndName(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000B5", actionRequired(), confirmed("sub-3ds"))
	_, err := p.submit(t)
	require.NoError(t, err)

	_, err = p.submit(t, func(in *paidInput) { in.Slug = "acme-two"; in.CompanyName = "Acme Two" })
	require.NoError(t, err)
	require.Len(t, p.rp.DirectCalls, 1)
	assert.Equal(t, "acme-two", p.rp.DirectCalls[0].TenantSlug)
	assert.Equal(t, "Acme Two", p.rp.DirectCalls[0].TenantName)
}

// ── Retries after an unknown outcome ──────────────────────────────────────────

func TestPaidBilling_RetryAfterUnavailable_ReusesTenant(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000B6",
		fixtures.FakeBillingStep{Err: domain.NewError(domain.ErrDependencyUnavailable, "in progress").
			WithDetails(map[string]any{"retry_after_seconds": 2})},
		confirmed("sub-paid"))

	_, err := p.submit(t)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
	assert.Equal(t, 2, de.Details["retry_after_seconds"], "Retry-After must reach the browser")
	assert.Equal(t, domain.StatePaymentPending, p.handoff(t).State)

	res, err := p.submit(t)
	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, res.State)
	require.Len(t, p.billing.Calls, 2)
	assert.Equal(t, p.billing.Calls[0], p.billing.Calls[1])
}

// ── Terminal Billing answers ──────────────────────────────────────────────────

func TestPaidBilling_TerminalErrors_DeleteHandoff(t *testing.T) {
	for _, cause := range []error{domain.ErrPaymentDeclined, domain.ErrChargeExpired, domain.ErrValidationFailed} {
		t.Run(cause.Error(), func(t *testing.T) {
			p := newPaidHarness("01JAB00000000000000000B7", failing(cause), confirmed("sub-new"))
			_, err := p.submit(t)
			assert.Equal(t, cause, causeOf(t, err))
			assert.Nil(t, p.handoff(t), "nothing payable exists: next submit starts fresh")

			// The next submit allocates afresh (new tenant_id from the ID generator)
			// and uses rate-limit budget like any new attempt.
			res, err := p.submit(t)
			require.NoError(t, err)
			assert.Equal(t, domain.StateCompleted, res.State)
			require.Len(t, p.billing.Calls, 2)
		})
	}
}

func TestPaidBilling_DeclineAfter3DS_DeletesHandoff(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000B8", actionRequired(), failing(domain.ErrPaymentDeclined))
	_, err := p.submit(t)
	require.NoError(t, err)
	_, err = p.submit(t)
	assert.Equal(t, domain.ErrPaymentDeclined, causeOf(t, err))
	assert.Nil(t, p.handoff(t))
}

func TestPaidBilling_AlreadyExists_IsNotTrusted(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000B9", fixtures.FakeBillingStep{Resp: &port.ChargeResponse{
		Outcome: port.ChargeAlreadyExists, BillingSubscriptionID: "sub-someone",
	}})
	_, err := p.submit(t)
	assert.Equal(t, domain.ErrInvalidState, causeOf(t, err))
	assert.Empty(t, p.rp.DirectCalls, "never provision against a subscription this attempt did not create")
	assert.Equal(t, domain.StatePaymentPending, p.handoff(t).State)
}

// ── After payment is confirmed ────────────────────────────────────────────────

func TestPaidBilling_PaymentConfirmed_ResumeSkipsBillingAndAllowsNewEmail(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000BA", confirmed("sub-paid"))
	p.rp.DirectErr = domain.NewError(domain.ErrEmailUnavailable, "email in use")

	_, err := p.submit(t)
	assert.Equal(t, domain.ErrEmailUnavailable, causeOf(t, err))
	h := p.handoff(t)
	require.NotNil(t, h)
	assert.Equal(t, domain.StatePaymentConfirmed, h.State)

	p.rp.DirectErr = nil
	res, err := p.submit(t, func(in *paidInput) { in.AdminEmail = "other@acme.example"; in.PaymentToken = "pm_spent" })
	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, res.State)
	assert.Len(t, p.billing.Calls, 1, "a paid subscription is never charged again")
	require.Len(t, p.rp.DirectCalls, 2)
	assert.Equal(t, "other@acme.example", p.rp.DirectCalls[1].AdminEmail)
	assert.Equal(t, h.TenantID, p.rp.DirectCalls[1].TenantID)
	assert.Equal(t, "sub-paid", p.rp.DirectCalls[1].BillingSubscriptionID)
}

func TestPaidBilling_PaymentConfirmed_RejectsPlanChange(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000BB", confirmed("sub-paid"))
	p.rp.DirectErr = domain.NewError(domain.ErrDependencyUnavailable, "rp down")
	_, err := p.submit(t)
	require.Error(t, err)

	_, err = p.submit(t, func(in *paidInput) { in.Plan = domain.PlanStarter })
	assert.Equal(t, domain.ErrInvalidState, causeOf(t, err))
}

func TestPaidBilling_LaterStates_ReportProgressWithoutCaptcha(t *testing.T) {
	for _, state := range []domain.HandoffState{domain.StateIdentityCreated, domain.StateTenantPending, domain.StateCompleted} {
		t.Run(string(state), func(t *testing.T) {
			p := newPaidHarness("01JAB00000000000000000BC", confirmed("sub-paid"))
			_, err := p.submit(t)
			require.NoError(t, err)
			h := p.handoff(t)
			h.State = state
			require.NoError(t, p.handoffs.Update(context.Background(), h))

			p.captcha.Err = errors.New("must not be called")
			res, err := p.submit(t)
			require.NoError(t, err)
			assert.Equal(t, state, res.State)
			assert.Len(t, p.billing.Calls, 1)
		})
	}
}

func TestPaidBilling_SlugConflictState_ReturnsSlugTaken(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000BD", confirmed("sub-paid"))
	_, err := p.submit(t)
	require.NoError(t, err)
	h := p.handoff(t)
	h.State = domain.StateSlugConflict
	require.NoError(t, p.handoffs.Update(context.Background(), h))

	res, err := p.submit(t)
	assert.Equal(t, domain.ErrSlugTaken, causeOf(t, err))
	require.NotNil(t, res)
	assert.Equal(t, domain.StateSlugConflict, res.State)
	assert.NotEmpty(t, res.SlugSuggestions)
}

func TestPaidBilling_ActionRequired_HandoffUpdateFails(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000BE", actionRequired())
	p.handoffs.UpdateErr = errors.New("valkey down")
	_, err := p.submit(t)
	assert.Equal(t, domain.ErrDependencyUnavailable, causeOf(t, err))
}

// ── Past payment: never allocate a new tenant (SB-INV-3) ─────────────────────

func TestPaidBilling_PastPayment_DifferentEmailIsRejected(t *testing.T) {
	for _, state := range []domain.HandoffState{
		domain.StateIdentityCreated, domain.StateTenantPending, domain.StateSlugConflict,
		domain.StateCompleted, domain.StateFailed,
	} {
		t.Run(string(state), func(t *testing.T) {
			p := newPaidHarness("01JAB00000000000000000C1", confirmed("sub-paid"), confirmed("sub-second"))
			_, err := p.submit(t)
			require.NoError(t, err)
			h := p.handoff(t)
			h.State = state
			require.NoError(t, p.handoffs.Update(context.Background(), h))

			_, err = p.submit(t, func(in *paidInput) { in.AdminEmail = "other@acme.example" })
			var de *domain.DomainError
			require.ErrorAs(t, err, &de)
			assert.Equal(t, domain.ErrInvalidState, de.Cause)
			assert.Equal(t, "details_changed", de.Details["reason"])
			assert.Len(t, p.billing.Calls, 1, "no second charge")
			assert.Equal(t, h.TenantID, p.handoff(t).TenantID, "the paid handoff is not overwritten")
		})
	}
}

func TestPaidBilling_PastPayment_FailedSameEmailReportsState(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000C2", confirmed("sub-paid"))
	_, err := p.submit(t)
	require.NoError(t, err)
	h := p.handoff(t)
	h.State = domain.StateFailed
	require.NoError(t, p.handoffs.Update(context.Background(), h))

	res, err := p.submit(t)
	require.NoError(t, err)
	assert.Equal(t, domain.StateFailed, res.State)
	assert.Len(t, p.billing.Calls, 1)
}

// ── PUB-1 conflict after payment: resubmit with a new slug (no SB-4) ─────────

func TestPaidBilling_SlugPreCheckConflict_ResubmitWithNewSlug(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000C3", confirmed("sub-paid"))
	p.om.SlugFreeErr = domain.NewError(domain.ErrSlugTaken, "taken")

	res, err := p.submit(t)
	assert.Equal(t, domain.ErrSlugTaken, causeOf(t, err))
	require.NotNil(t, res)
	assert.NotEmpty(t, res.SlugSuggestions)
	assert.Equal(t, domain.StatePaymentConfirmed, p.handoff(t).State,
		"no Keycloak user yet, so the handoff stays resumable rather than slug_conflict")
	assert.Empty(t, p.rp.DirectCalls)

	p.om.SlugFreeErr = nil
	res, err = p.submit(t, func(in *paidInput) { in.Slug = "acme-new" })
	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, res.State)
	assert.Len(t, p.billing.Calls, 1, "the paid subscription is reused")
	require.Len(t, p.rp.DirectCalls, 1)
	assert.Equal(t, "acme-new", p.rp.DirectCalls[0].TenantSlug)
}

// ── RATE_* limits apply to SB-5 ──────────────────────────────────────────────

func TestPaidBilling_WithRateLimits(t *testing.T) {
	p := newPaidHarness("01JAB00000000000000000C4")
	p.svc.WithRateLimits(1, 0) // 0 keeps the default email limit
	p.limiter.IPLimit = 100    // the fake's own limit is not what is tested

	_, err := p.submit(t)
	require.NoError(t, err)

	p2 := &port.Session{SID: "sid2", SignupID: "01JAB00000000000000000C5", CSRFHash: "h"}
	in := validPaidInput(p2)
	_, err = p.svc.Submit(context.Background(), in)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrRateLimited, de.Cause)
	assert.Equal(t, "ip", de.Details["scope"])
}

// ── SB-1: hold the attempt past Billing's 3-D Secure window ───────────────────

func TestPaidBilling_3DS_HoldsAttemptPastExpiresAt(t *testing.T) {
	// A late 3-D Secure: the re-POST after expires_at gets 201 (Billing's fix
	// re-reads the provider and activates the paid subscription).
	expiresAt := time.Date(2026, 10, 22, 9, 29, 0, 0, time.UTC) // fake clock 09:00 + 29 min
	step := actionRequired()
	step.Resp.ExpiresAt = expiresAt.Format(time.RFC3339)
	p := newPaidHarness("01JAB00000000000000000H1", step, confirmed("sub-3ds"))

	res, err := p.submit(t)
	require.NoError(t, err)
	assert.Equal(t, expiresAt.Add(domain.PaymentHoldGrace), res.PaymentHoldUntil,
		"the session and handoff are held until expires_at + 15 min")

	h := p.handoff(t)
	assert.True(t, h.PaymentExpiresAt.Equal(expiresAt))
	assert.Equal(t, res.PaymentHoldUntil, h.PaymentHoldUntil())
	firstTenant := h.TenantID

	// Re-POST at expires_at + 1 min: same handoff, same tenant_id and key.
	res, err = p.submit(t)
	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, res.State)
	require.Len(t, p.billing.Calls, 2)
	assert.Equal(t, p.billing.Calls[0], p.billing.Calls[1])
	assert.Equal(t, "charge:"+firstTenant, p.billing.Calls[1].IdempotencyKey)
	require.Len(t, p.rp.DirectCalls, 1)
	assert.Equal(t, firstTenant, p.rp.DirectCalls[0].TenantID)
}

func TestPaidBilling_3DS_MissingExpiresAtFallsBackToBillingWindow(t *testing.T) {
	step := actionRequired()
	step.Resp.ExpiresAt = "not-a-time"
	p := newPaidHarness("01JAB00000000000000000H2", step)

	res, err := p.submit(t)
	require.NoError(t, err)
	h := p.handoff(t)
	require.False(t, h.PaymentExpiresAt.IsZero())
	assert.Equal(t, 30*time.Minute, h.PaymentExpiresAt.Sub(h.UpdatedAt.UTC()), "Billing's SCA_ABANDON_AFTER")
	assert.Equal(t, h.PaymentHoldUntil(), res.PaymentHoldUntil)
}

// An expires_at far beyond Billing's window is not trusted: the hold stays bounded.
func TestPaidBilling_3DS_FarFutureExpiresAtIsBounded(t *testing.T) {
	step := actionRequired()
	step.Resp.ExpiresAt = "9999-12-31T23:59:59Z"
	p := newPaidHarness("01JAB00000000000000000H3", step)

	_, err := p.submit(t)
	require.NoError(t, err)
	h := p.handoff(t)
	assert.Equal(t, 30*time.Minute, h.PaymentExpiresAt.Sub(h.UpdatedAt.UTC()))
}
