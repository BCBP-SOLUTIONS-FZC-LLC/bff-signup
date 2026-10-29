package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
)

func TestHandoffStateTransitions(t *testing.T) {
	h := &domain.Handoff{State: domain.StateAllocated}

	assert.False(t, h.IsTerminal())
	assert.False(t, h.CanResume())
	assert.False(t, h.CanChangeSlug())

	h.State = domain.StateIdentityCreated
	assert.False(t, h.IsTerminal())
	assert.False(t, h.CanResume())
	assert.False(t, h.CanChangeSlug())

	h.State = domain.StateTenantPending
	assert.False(t, h.IsTerminal())
	assert.True(t, h.CanResume())
	assert.False(t, h.CanChangeSlug())

	h.State = domain.StateSlugConflict
	assert.False(t, h.IsTerminal())
	assert.False(t, h.CanResume())
	assert.True(t, h.CanChangeSlug())

	h.State = domain.StateCompleted
	assert.True(t, h.IsTerminal())
	assert.False(t, h.CanResume())
	assert.False(t, h.CanChangeSlug())

	h.State = domain.StateRejected
	assert.True(t, h.IsTerminal())

	h.State = domain.StateFailed
	assert.True(t, h.IsTerminal())
}
