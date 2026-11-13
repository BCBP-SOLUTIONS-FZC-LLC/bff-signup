package domain

import "errors"

// Sentinel errors — used as Cause in DomainError for HTTP mapping.
var (
	ErrSessionExpired        = errors.New("session_expired")
	ErrCSRFInvalid           = errors.New("csrf_invalid")
	ErrCaptchaFailed         = errors.New("captcha_failed")
	ErrValidationFailed      = errors.New("validation_failed")
	ErrSignupNotFound        = errors.New("signup_not_found")
	ErrEmailUnavailable      = errors.New("email_unavailable")
	ErrTrialNotEligible      = errors.New("trial_not_eligible")
	ErrSlugTaken             = errors.New("slug_taken")
	ErrInvalidState          = errors.New("invalid_state")
	ErrOperationInProgress   = errors.New("operation_in_progress") // RP-1 409 OPERATION_IN_PROGRESS; the orchestrator retries it, it never reaches the user
	ErrPaymentDeclined       = errors.New("payment_declined")      // 422 — card declined; terminal, user must fix payment method
	ErrRateLimited           = errors.New("rate_limited")
	ErrDependencyUnavailable = errors.New("dependency_unavailable")
	ErrInternal              = errors.New("internal_error")
)

// DomainError carries a sentinel plus a human message and optional structured details.
type DomainError struct {
	Cause   error
	Message string
	Details map[string]any
}

func (e *DomainError) Error() string { return e.Cause.Error() + ": " + e.Message }
func (e *DomainError) Unwrap() error { return e.Cause }

// NewError creates a DomainError.
func NewError(cause error, message string) *DomainError {
	return &DomainError{Cause: cause, Message: message}
}

// WithDetails attaches structured details and returns e for chaining.
func (e *DomainError) WithDetails(details map[string]any) *DomainError {
	e.Details = details
	return e
}
