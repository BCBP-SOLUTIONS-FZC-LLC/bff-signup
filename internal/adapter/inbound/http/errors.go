package http

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
)

// ErrorResponse is the public error envelope (LLD §5.1, §17).
type ErrorResponse struct {
	Error     string         `json:"error"`
	Status    int            `json:"status"`
	TraceID   string         `json:"trace_id,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

// handleError maps a domain or infrastructure error to an HTTP response.
func handleError(c *gin.Context, err error) {
	var de *domain.DomainError
	if errors.As(err, &de) {
		status := domainStatus(de.Cause)
		c.JSON(status, ErrorResponse{
			Error:     de.Cause.Error(),
			Status:    status,
			RequestID: c.GetString("X-Request-Id"),
			Details:   de.Details,
		})
		c.Abort()
		return
	}
	// Unknown / internal error
	c.JSON(http.StatusInternalServerError, ErrorResponse{
		Error:     domain.ErrInternal.Error(),
		Status:    http.StatusInternalServerError,
		RequestID: c.GetString("X-Request-Id"),
	})
	c.Abort()
}

func domainStatus(cause error) int {
	switch cause {
	case domain.ErrValidationFailed:
		return http.StatusBadRequest
	case domain.ErrSessionExpired:
		return http.StatusUnauthorized
	case domain.ErrCSRFInvalid, domain.ErrCaptchaFailed:
		return http.StatusForbidden
	case domain.ErrSignupNotFound:
		return http.StatusNotFound
	case domain.ErrEmailUnavailable, domain.ErrTrialNotEligible,
		domain.ErrSlugTaken, domain.ErrInvalidState:
		return http.StatusConflict
	case domain.ErrRateLimited:
		return http.StatusTooManyRequests
	case domain.ErrDependencyUnavailable:
		return http.StatusServiceUnavailable
	case domain.ErrInternal:
		return http.StatusInternalServerError
	}
	return http.StatusInternalServerError
}
