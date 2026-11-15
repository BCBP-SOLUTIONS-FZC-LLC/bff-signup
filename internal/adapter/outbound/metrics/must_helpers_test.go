package metrics

import (
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func newTestCounter(name string) prometheus.Counter {
	return prometheus.NewCounter(prometheus.CounterOpts{Name: name, Help: "test"})
}

func TestMust_ReturnsValueOrPanics(t *testing.T) {
	c := newTestCounter("must_ok")

	assert.Same(t, c, must(c, nil))
	assert.PanicsWithValue(t, "metrics: register service collector: bad label",
		func() { must(c, errors.New("bad label")) })
}

func TestMustShared_ReusesAnAlreadyRegisteredCollector(t *testing.T) {
	built := newTestCounter("shared_built")
	existing := newTestCounter("shared_existing")

	assert.Same(t, built, mustShared(built, nil))
	assert.Same(t, existing, mustShared(built, prometheus.AlreadyRegisteredError{ExistingCollector: existing}),
		"platform-events registered it first: reuse, do not fail")
}

func TestMustShared_PanicsOnAnyOtherError(t *testing.T) {
	built := newTestCounter("shared_other")

	assert.Panics(t, func() { mustShared(built, errors.New("bad name")) })
	// An already-registered collector of a different type (a histogram is not a
	// counter) cannot be reused.
	assert.Panics(t, func() {
		mustShared(built, prometheus.AlreadyRegisteredError{ExistingCollector: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "h", Help: "t"})})
	})
}

// TestMustShared_ReusesHistogramVec covers the AlreadyRegisteredError success branch for
// *prometheus.HistogramVec — the instantiation used by registerShared() in business.go.
func TestMustShared_ReusesHistogramVec(t *testing.T) {
	hv := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "reuse_hv_ok_coverage", Help: "test"}, nil)
	err := prometheus.AlreadyRegisteredError{ExistingCollector: hv}
	result := mustShared(hv, err)
	assert.Same(t, hv, result, "mustShared must return the already-registered HistogramVec")
}
