package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/test/fixtures"
)

// SB2-I01 / OBS-40: a same-email resume of an identity_created signup must not
// need a new CAPTCHA and must not use up rate-limit budget (LLD §5.4 G-1).
func TestSubmit_Resume_IdentityCreated_UsesNoCaptchaOrRateLimitBudget(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	captcha := &fixtures.FakeCaptchaVerifier{}
	rp := fixtures.NewFakeRealmProvisioner("kc-001")
	svc := newSignupService(handoffs, fixtures.NewFakeRateLimiter(), captcha,
		&fixtures.FakePasswordHasher{}, rp, fixtures.NewFakeOrgMembership("tid"))

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000060", CSRFHash: "h"}
	input := validInput(sess)

	_, err := svc.Submit(context.Background(), input)
	require.NoError(t, err)
	require.Len(t, rp.Calls, 1)

	h, _ := handoffs.Get(context.Background(), sess.SignupID)
	h.State = domain.StateIdentityCreated
	require.NoError(t, handoffs.Update(context.Background(), h))

	// A used CAPTCHA token would fail now, and the IP budget is 3 per hour.
	captcha.Err = errors.New("captcha token already used")

	for i := 0; i < 5; i++ {
		result, err := svc.Submit(context.Background(), input)
		require.NoError(t, err, "resume %d must not hit CAPTCHA or the rate limit", i+1)
		assert.Equal(t, domain.StateIdentityCreated, result.State)
	}
	assert.Len(t, rp.Calls, 1, "a resume must not call RP-1 again")
}

// Control: only a same-email resume skips CAPTCHA. A different email on the same
// session still goes through CAPTCHA first.
func TestSubmit_Resume_DifferentEmail_StillChecksCaptcha(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	captcha := &fixtures.FakeCaptchaVerifier{}
	svc := newSignupService(handoffs, fixtures.NewFakeRateLimiter(), captcha,
		&fixtures.FakePasswordHasher{}, fixtures.NewFakeRealmProvisioner("kc-001"), fixtures.NewFakeOrgMembership("tid"))

	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000061", CSRFHash: "h"}
	input := validInput(sess)

	_, err := svc.Submit(context.Background(), input)
	require.NoError(t, err)

	h, _ := handoffs.Get(context.Background(), sess.SignupID)
	h.State = domain.StateIdentityCreated
	require.NoError(t, handoffs.Update(context.Background(), h))

	captchaErr := errors.New("captcha token already used")
	captcha.Err = captchaErr

	other := validInput(sess)
	other.AdminEmail = "someone.else@acme.example"
	_, err = svc.Submit(context.Background(), other)
	require.ErrorIs(t, err, captchaErr)
}
