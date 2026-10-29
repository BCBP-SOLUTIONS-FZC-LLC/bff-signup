package signupctx_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/pkg/signupctx"
)

func TestWithContext_FromContext_RoundTrip(t *testing.T) {
	sc := &signupctx.SignupContext{
		Session:  &port.Session{SID: "sid-001", SignupID: "01JAB0000000000000000001"},
		ClientIP: "1.2.3.4",
	}
	ctx := signupctx.WithContext(context.Background(), sc)

	got, ok := signupctx.FromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, "sid-001", got.Session.SID)
	assert.Equal(t, "1.2.3.4", got.ClientIP)
}

func TestFromContext_ReturnsFalseWhenNotSet(t *testing.T) {
	_, ok := signupctx.FromContext(context.Background())
	assert.False(t, ok)
}

func TestMustFromContext_PanicsWhenNotSet(t *testing.T) {
	assert.Panics(t, func() {
		signupctx.MustFromContext(context.Background())
	})
}

func TestMustFromContext_ReturnsValueWhenSet(t *testing.T) {
	sc := &signupctx.SignupContext{ClientIP: "10.0.0.1"}
	ctx := signupctx.WithContext(context.Background(), sc)

	got := signupctx.MustFromContext(ctx)
	assert.Equal(t, "10.0.0.1", got.ClientIP)
}
