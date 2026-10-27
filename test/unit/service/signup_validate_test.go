package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/test/fixtures"
)

// newSignupService and validInput are defined in signup_service_test.go (same package).

// Tests that exercise the validate() branches not hit by the main happy-path tests.

func TestSubmit_InvalidCompanyName_ShortName(t *testing.T) {
	svc := newSignupService(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(),
		&fixtures.FakeCaptchaVerifier{}, &fixtures.FakePasswordHasher{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000030", CSRFHash: "h"}
	input := validInput(sess)
	input.CompanyName = "A" // too short (< 2)

	_, err := svc.Submit(context.Background(), input)
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrValidationFailed, de.Cause)
}

func TestSubmit_InvalidSlug_WhenProvided(t *testing.T) {
	svc := newSignupService(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(),
		&fixtures.FakeCaptchaVerifier{}, &fixtures.FakePasswordHasher{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000031", CSRFHash: "h"}
	input := validInput(sess)
	input.Slug = "UPPER_CASE_INVALID" // fails slug pattern

	_, err := svc.Submit(context.Background(), input)
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrValidationFailed, de.Cause)
}

func TestSubmit_InvalidAdminEmail(t *testing.T) {
	svc := newSignupService(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(),
		&fixtures.FakeCaptchaVerifier{}, &fixtures.FakePasswordHasher{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000032", CSRFHash: "h"}
	input := validInput(sess)
	input.AdminEmail = "not-an-email"

	_, err := svc.Submit(context.Background(), input)
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrValidationFailed, de.Cause)
}

func TestSubmit_TermsNotAccepted(t *testing.T) {
	svc := newSignupService(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(),
		&fixtures.FakeCaptchaVerifier{}, &fixtures.FakePasswordHasher{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000033", CSRFHash: "h"}
	input := validInput(sess)
	input.AcceptTerms = false

	_, err := svc.Submit(context.Background(), input)
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrValidationFailed, de.Cause)
}

func TestSubmit_WrongTermsVersion(t *testing.T) {
	svc := newSignupService(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(),
		&fixtures.FakeCaptchaVerifier{}, &fixtures.FakePasswordHasher{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000034", CSRFHash: "h"}
	input := validInput(sess)
	input.TermsVersion = "2020-01" // old version

	_, err := svc.Submit(context.Background(), input)
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrValidationFailed, de.Cause)
}

func TestSubmit_EmptyTermsVersion(t *testing.T) {
	svc := newSignupService(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(),
		&fixtures.FakeCaptchaVerifier{}, &fixtures.FakePasswordHasher{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)
	sess := &port.Session{SID: "sid", SignupID: "01JAB0000000000000000035", CSRFHash: "h"}
	input := validInput(sess)
	input.TermsVersion = ""

	_, err := svc.Submit(context.Background(), input)
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrValidationFailed, de.Cause)
}
