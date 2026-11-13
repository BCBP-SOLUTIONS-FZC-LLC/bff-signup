package adapter_test

// A base URL that cannot be parsed makes the HTTP request fail to build. This
// covers the "failed to build request" branches of the O&M and RP-1 clients.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	billingadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/billing"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/orgmembership"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/realmprovisioner"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

const unparsableBaseURL = "http://bad host"

func isInternal(err error) bool {
	var de *domain.DomainError
	return errors.As(err, &de) && errors.Is(de.Cause, domain.ErrInternal)
}

func TestOrgMembership_UnparsableBaseURL(t *testing.T) {
	c := orgmembership.New(unparsableBaseURL, time.Second)

	// PUB-1 is fail-open: a request that cannot be built lets the signup go on.
	assert.NoError(t, c.CheckSlugFree(context.Background(), "acme"))
	assert.True(t, isInternal(c.CheckEligibility(context.Background(), "hash")))
	_, err := c.ProvisionTenant(context.Background(), port.ProvisionTenantRequest{TenantID: "t"})
	require.Error(t, err)
	assert.True(t, isInternal(err))
}

func TestRealmProvisioner_UnparsableBaseURL(t *testing.T) {
	c := realmprovisioner.New(unparsableBaseURL, time.Second)

	_, err1 := c.ProvisionTrial(context.Background(), port.ProvisionTrialRequest{}, "key")
	_, err2 := c.ProvisionDirect(context.Background(), port.ProvisionDirectRequest{}, "key")

	assert.True(t, isInternal(err1))
	assert.True(t, isInternal(err2))
}

func TestBilling_UnparsableBaseURL(t *testing.T) {
	c := billingadapter.New(unparsableBaseURL, time.Second)

	_, err := c.AuthorizeAndCharge(context.Background(), port.ChargeRequest{TenantID: "t"})
	require.Error(t, err)
	assert.True(t, isInternal(err))
}
