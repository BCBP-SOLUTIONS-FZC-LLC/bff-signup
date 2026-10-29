package port

import "context"

// CaptchaVerifier verifies a browser CAPTCHA token (LLD §5.6, BFF-3).
// The implementation uses Cloudflare Turnstile siteverify.
type CaptchaVerifier interface {
	// Verify calls the provider's verification endpoint.
	// Returns nil on success; a DomainError with ErrCaptchaFailed on rejection.
	Verify(ctx context.Context, token, remoteIP string) error
}
