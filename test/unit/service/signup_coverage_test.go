package service_test

// Extra unit tests for SignupService paths that had no test: the e-mail rate
// limit, the slug derived from the company name, a hashing failure on a new and
// on a resumed signup, and validation errors for the admin name and password.

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/test/fixtures"
)

func TestSubmit_SixthAttemptForSameEmailIsRateLimited(t *testing.T) {
	svc := newSignupService(fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		&fixtures.FakePasswordHasher{}, fixtures.NewFakeRealmProvisioner("kc-001"), fixtures.NewFakeOrgMembership("tid"))
	var err error

	for i := 1; i <= 6; i++ {
		in := validInput(&port.Session{SID: fmt.Sprintf("sid-%d", i), SignupID: fmt.Sprintf("01JAB000000000000000EM%02d", i), CSRFHash: "h"})
		in.ClientIP = fmt.Sprintf("10.0.0.%d", i) // a different IP each time, so only the e-mail limit can trigger
		_, err = svc.Submit(context.Background(), in)
		if i <= 5 {
			require.NoError(t, err, "attempt %d", i)
		}
	}

	require.Error(t, err)
	assert.True(t, isCause(err, domain.ErrRateLimited))
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, "email", de.Details["scope"])
}

func TestSubmit_SlugAbsentIsDerivedFromCompanyName(t *testing.T) {
	svc := newSignupService(fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		&fixtures.FakePasswordHasher{}, fixtures.NewFakeRealmProvisioner("kc-001"), fixtures.NewFakeOrgMembership("tid"))
	in := validInput(&port.Session{SID: "sid", SignupID: "01JAB0000000000000000DS1", CSRFHash: "h"})
	in.Slug = ""

	result, err := svc.Submit(context.Background(), in)

	require.NoError(t, err)
	assert.Equal(t, domain.StateCompleted, result.State)
}

func TestSubmit_PasswordHashingFailureIsInternalError(t *testing.T) {
	hasher := &fixtures.FakePasswordHasher{Err: errors.New("boom")}
	rp := fixtures.NewFakeRealmProvisioner("kc-001")
	svc := newSignupService(fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		hasher, rp, fixtures.NewFakeOrgMembership("tid"))

	_, err := svc.Submit(context.Background(), validInput(&port.Session{SID: "sid", SignupID: "01JAB0000000000000000HF1", CSRFHash: "h"}))

	assert.True(t, isCause(err, domain.ErrInternal))
	assert.Empty(t, rp.Calls, "RP-1 must not be called when hashing fails")
}

// A resumed signup (RP-1 not confirmed, same details) hashes the password again.
func TestSubmit_ResumeAllocated_PasswordHashingFailureIsInternalError(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	hasher := &fixtures.FakePasswordHasher{}
	rp := fixtures.NewFakeRealmProvisioner("kc-001")
	svc := newSignupService(handoffs, fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{}, hasher, rp, fixtures.NewFakeOrgMembership("tid"))
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000HF2", CSRFHash: "h"}
	in := validInput(sess)
	_, err := svc.Submit(context.Background(), in)
	require.NoError(t, err)
	h, _ := handoffs.Get(context.Background(), sess.SignupID)
	h.State = domain.StateAllocated
	require.NoError(t, handoffs.Update(context.Background(), h))
	hasher.Err = errors.New("boom")

	_, err = svc.Submit(context.Background(), in)

	assert.True(t, isCause(err, domain.ErrInternal))
	assert.Len(t, rp.Calls, 1, "RP-1 must not be called again when hashing fails")
}

func TestSubmit_EmptyAdminNameIsValidationError(t *testing.T) {
	svc := newSignupService(fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		&fixtures.FakePasswordHasher{}, fixtures.NewFakeRealmProvisioner("kc-001"), fixtures.NewFakeOrgMembership("tid"))
	in := validInput(&port.Session{SID: "sid", SignupID: "01JAB0000000000000000VN1", CSRFHash: "h"})
	in.AdminName = ""

	_, err := svc.Submit(context.Background(), in)

	assert.True(t, isCause(err, domain.ErrValidationFailed))
}

func TestSubmit_WeakPasswordIsValidationError(t *testing.T) {
	svc := newSignupService(fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		&fixtures.FakePasswordHasher{}, fixtures.NewFakeRealmProvisioner("kc-001"), fixtures.NewFakeOrgMembership("tid"))
	in := validInput(&port.Session{SID: "sid", SignupID: "01JAB0000000000000000VN2", CSRFHash: "h"})
	in.Password = "short"

	_, err := svc.Submit(context.Background(), in)

	assert.True(t, isCause(err, domain.ErrValidationFailed))
}
