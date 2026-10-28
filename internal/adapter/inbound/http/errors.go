// Package http — error handling is fully delegated to platform-gincommon's
// RFC 9457 implementation. All error responses are written via
// gincommon.AbortWithProblem; the legacy handleError / ErrorResponse helpers
// have been removed as part of the RFC 9457 adoption.
// Mirrors iam-org-membership/internal/adapter/inbound/http/errors.go.
package http

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/problem"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
)

// HandleError maps a domain or infrastructure error to an RFC 9457 problem
// response (application/problem+json) via gincommon.AbortWithProblem.
// Matches iam-org-membership's HandleError pattern exactly.
func HandleError(c *gin.Context, err error) {
	gincommon.AbortWithProblem(c, toDomainProblem(err))
}

// toDomainProblem converts any service error to a *problem.Error for RFC 9457
// rendering. *problem.Error passes through; *domain.DomainError is mapped via
// domainErrorToProblem with its Details attached as RFC 9457 §3.2 extension
// members; anything else is passed to gincommon (DeadlineExceeded→503, else→500).
func toDomainProblem(err error) error {
	var pe *problem.Error
	if errors.As(err, &pe) {
		return pe
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
	case errors.Is(de.Cause, domain.ErrRateLimited):
		return problem.NewRateLimitError(msg).WithCode(code)
	case errors.Is(de.Cause, domain.ErrDependencyUnavailable):
		return problem.NewServiceUnavailableError(msg).WithCode(code)
	default:
		// ErrInternal and any unclassified error → 500 with no detail exposed.
		return problem.NewInternalError(errors.New(msg)).WithCode(code)
	}
}
