package port

import "errors"

// BillingClient is a STUB for Phase 1.5 (LLD §19, BFF-5).
// Direct-paid signup is not implemented in Phase 1.
type BillingClient interface {
	// NotImplemented always returns ErrNotImplemented.
	NotImplemented() error
}

// ErrNotImplemented is returned by all BillingClient methods in Phase 1.
var ErrNotImplemented = errors.New("not_implemented")

// BillingStub is the Phase 1 stub adapter.
type BillingStub struct{}

func (BillingStub) NotImplemented() error { return ErrNotImplemented }
