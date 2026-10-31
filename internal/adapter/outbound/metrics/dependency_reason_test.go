package metrics

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// timeoutErr is a net.Error that reports a timeout.
type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestDependencyErrorReason(t *testing.T) {
	assert.Equal(t, "timeout", DependencyErrorReason(timeoutErr{}))
	assert.Equal(t, "timeout", DependencyErrorReason(fmt.Errorf("call failed: %w", timeoutErr{})), "wrapped timeouts are still timeouts")
	assert.Equal(t, "transport", DependencyErrorReason(errors.New("connection refused")))
}
