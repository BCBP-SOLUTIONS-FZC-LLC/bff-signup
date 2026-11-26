package metrics

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMust_ReturnsValueOrPanics(t *testing.T) {
	c := &struct{ name string }{"must_ok"}

	assert.Same(t, c, must(c, nil))
	assert.PanicsWithValue(t, "metrics: register service collector: bad label",
		func() { must(c, errors.New("bad label")) })
}
