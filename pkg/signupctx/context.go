// Package signupctx threads the signup-specific request context through
// Go's context.Context so service-layer functions can access it without
// taking gin as a dependency (LLD §3.2 dependency rules).
//
// This is the BFF equivalent of the IAM services' requestctx package.
// Unlike authenticated services, the BFF has no user identity at request
// time — the context carries only what is available on a public, unauthenticated
// signup request: the session and the client IP.
package signupctx

import (
	"context"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

type contextKey struct{}

// SignupContext holds the validated session and client IP extracted by
// the inbound HTTP middleware stack (session.go, clientip.go).
type SignupContext struct {
	// Session is the validated sbff_sid session. Nil only on SB-1 (session
	// creation) where no session yet exists.
	Session *port.Session

	// ClientIP is the real client address extracted from x-envoy-external-address
	// (set by Envoy with xff_num_trusted_hops: 1). Used for per-IP rate limiting.
	ClientIP string
}

// WithContext returns a new context carrying sc.
func WithContext(ctx context.Context, sc *SignupContext) context.Context {
	return context.WithValue(ctx, contextKey{}, sc)
}

// FromContext retrieves the SignupContext set by the middleware stack.
// Returns (nil, false) if the context was not set — callers should treat
// this as a missing session and return 401.
func FromContext(ctx context.Context) (*SignupContext, bool) {
	sc, ok := ctx.Value(contextKey{}).(*SignupContext)
	return sc, ok && sc != nil
}

// MustFromContext retrieves the SignupContext or panics.
// Use only inside handlers that are always protected by the session middleware.
func MustFromContext(ctx context.Context) *SignupContext {
	sc, ok := FromContext(ctx)
	if !ok {
		panic("signupctx: context not set — is the session middleware wired?")
	}
	return sc
}
