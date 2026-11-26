// Package http — error handling is fully delegated to platform-gincommon's
// RFC 9457 implementation. All error responses are written via
// gincommon.AbortWithProblem (or, for an expired request deadline, by
// gincommon's timeout middleware). This file only maps domain sentinels to
// gincommon's problem constructors; it defines no error format of its own.
// Mirrors iam-org-membership's HandleError / toDomainProblem /
// domainErrorToProblem (internal/adapter/inbound/http/middleware.go).
package http

import (
	"context"
	"errors"
	stdhttp "net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/problem"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/inbound/http/middleware"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
)

// chargeExpiredType is the 410 returned when Billing reports CHARGE_EXPIRED:
// 3-D Secure was not completed in time and the browser must start a new signup.
var chargeExpiredType = problem.MustType("signup", "charge-expired", "Payment expired", stdhttp.StatusGone, "CHARGE_EXPIRED")

// HandleError maps a domain or infrastructure error to an RFC 9457 problem
// response (application/problem+json) via gincommon.AbortWithProblem.
// Matches iam-org-membership's HandleError pattern exactly.
func HandleError(c *gin.Context, err error) {
	if leaveToRequestContext(c, err) {
		return
	}
	setRetryAfter(c, err)
	gincommon.AbortWithProblem(c, toDomainProblem(err))
}

// clientClosedRequest is the de-facto status (nginx's 499) recorded when the
// client went away before a response; nothing reads the body.
const clientClosedRequest = 499

// leaveToRequestContext handles errors caused by the request's own context
// ending, which this handler must not report as its own failure (as in
// iam-org-membership):
//
//   - The request deadline expired (gincommon's Config.RequestTimeout): abort
//     without writing, so gincommon's timeout middleware writes its 503
//     REQUEST_TIMEOUT problem and counts it on platform_http_request_timeouts_total.
//   - The client disconnected: record 499 instead of a misleading 5xx.
//
// It only applies when the request context itself is done and err is one a
// context ending causes (causedByContext). A handler's own,
// shorter deadline (REQUEST_DEADLINE_MS / PAID_REQUEST_DEADLINE_MS) expiring
// leaves the request context alive, so that error is still mapped normally.
func leaveToRequestContext(c *gin.Context, err error) bool {
	if c.Request == nil {
		return false
	}
	switch ctxErr := c.Request.Context().Err(); {
	case ctxErr == nil:
		return false
	case errors.Is(ctxErr, context.DeadlineExceeded) && causedByContext(err, context.DeadlineExceeded):
		c.Abort()
		return true
	case errors.Is(ctxErr, context.Canceled) && causedByContext(err, context.Canceled):
		c.AbortWithStatus(clientClosedRequest)
		return true
	}
	return false
}

// causedByContext reports whether err can be the result of the request context
// ending: the context error itself, or ErrDependencyUnavailable, which is what
// the outbound adapters and stores return when a call is cut off (the IAM
// services' ErrRequestTimeout plays this role). Any other error, e.g. a
// validation failure, is still reported as itself.
func causedByContext(err, ctxErr error) bool {
	return errors.Is(err, ctxErr) || errors.Is(err, domain.ErrDependencyUnavailable)
}

// setRetryAfter copies details.retry_after_seconds of a rate-limit or
// dependency-unavailable error (e.g. Billing REQUEST_IN_PROGRESS, BL-OQ-8) into
// the standard Retry-After header (LLD §10.2); the body field stays as well.
func setRetryAfter(c *gin.Context, err error) {
	var de *domain.DomainError
	if !errors.As(err, &de) {
		return
	}
	if !errors.Is(de.Cause, domain.ErrRateLimited) && !errors.Is(de.Cause, domain.ErrDependencyUnavailable) {
		return
	}
	if secs, ok := de.Details["retry_after_seconds"].(int); ok && secs > 0 {
		c.Header("Retry-After", strconv.Itoa(secs))
	}
}

// toDomainProblem converts any service error to a *problem.Error for RFC 9457
// rendering. *problem.Error passes through; *domain.DomainError is mapped via
// domainErrorToProblem with its Details attached as RFC 9457 §3.2 extension
// members; *http.MaxBytesError → 413; anything else is passed to gincommon
// (DeadlineExceeded→503, else→500).
func toDomainProblem(err error) error {
	var pe *problem.Error
	if errors.As(err, &pe) {
		return pe
	}
	if p := middleware.BodyTooLargeFromError(err); p != nil {
		return p
	}
	var de *domain.DomainError
	if errors.As(err, &de) {
		return domainErrorToProblem(de).WithExtensions(de.Details)
	}
	return err
}

// domainErrorToProblem maps a *domain.DomainError to the corresponding
// platform problem type per LLD §17. The domain message becomes the RFC 9457
// detail field; the domain error code (e.g. "session_expired") is surfaced as
// an UPPER_SNAKE errorCode (e.g. "SESSION_EXPIRED") so clients can branch on
// specific errors without parsing human-readable detail strings.
func domainErrorToProblem(de *domain.DomainError) *problem.Error {
	msg := de.Message
	code := strings.ToUpper(de.Cause.Error())
	switch {
	case errors.Is(de.Cause, domain.ErrValidationFailed):
		return problem.NewBadRequestError(msg).WithCode(code)
	case errors.Is(de.Cause, domain.ErrSessionExpired):
		return problem.NewUnauthorizedError(msg).WithCode(code)
	case errors.Is(de.Cause, domain.ErrCSRFInvalid),
		errors.Is(de.Cause, domain.ErrCaptchaFailed):
		return problem.NewForbiddenError(msg).WithCode(code)
	case errors.Is(de.Cause, domain.ErrSignupNotFound):
		return problem.NewNotFoundError(msg).WithCode(code)
	case errors.Is(de.Cause, domain.ErrEmailUnavailable),
		errors.Is(de.Cause, domain.ErrTrialNotEligible),
		errors.Is(de.Cause, domain.ErrSlugTaken),
		errors.Is(de.Cause, domain.ErrInvalidState):
		return problem.NewConflictError(msg).WithCode(code)
	case errors.Is(de.Cause, domain.ErrPaymentDeclined):
		return problem.NewBusinessRuleError(msg).WithCode(code) // 422 (BL-OQ-8)
	case errors.Is(de.Cause, domain.ErrChargeExpired):
		return problem.NewError(chargeExpiredType, msg).WithCode(code) // 410 (BL-OQ-8)
	case errors.Is(de.Cause, domain.ErrRateLimited):
		return problem.NewRateLimitError(msg).WithCode(code)
	case errors.Is(de.Cause, domain.ErrDependencyUnavailable):
		return problem.NewServiceUnavailableError(msg).WithCode(code)
	default:
		// ErrInternal and any unclassified error → 500 with no detail exposed.
		return problem.NewInternalError(errors.New(msg)).WithCode(code)
	}
}
