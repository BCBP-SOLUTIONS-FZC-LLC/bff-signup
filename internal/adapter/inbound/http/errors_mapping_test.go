package http

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
)

// LLD §17: every domain error maps to one fixed HTTP status.
func TestHandleError_EveryDomainErrorMapsToItsStatus(t *testing.T) {
	cases := []struct {
		cause  error
		status int
	}{
		{domain.ErrValidationFailed, http.StatusBadRequest},
		{domain.ErrSessionExpired, http.StatusUnauthorized},
		{domain.ErrCSRFInvalid, http.StatusForbidden},
		{domain.ErrCaptchaFailed, http.StatusForbidden},
		{domain.ErrSignupNotFound, http.StatusNotFound},
		{domain.ErrEmailUnavailable, http.StatusConflict},
		{domain.ErrTrialNotEligible, http.StatusConflict},
		{domain.ErrSlugTaken, http.StatusConflict},
		{domain.ErrInvalidState, http.StatusConflict},
		{domain.ErrPaymentDeclined, http.StatusUnprocessableEntity},
		{domain.ErrRateLimited, http.StatusTooManyRequests},
		{domain.ErrDependencyUnavailable, http.StatusServiceUnavailable},
		{domain.ErrInternal, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.cause.Error(), func(t *testing.T) {
			w := errorResponseFor(t, domain.NewError(tc.cause, "msg"))

			assert.Equal(t, tc.status, w.Code)
		})
	}
}

func TestHandleError_UnclassifiedErrorIs500(t *testing.T) {
	w := errorResponseFor(t, errors.New("something unexpected"))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleError_OversizedBodyIs413(t *testing.T) {
	w := errorResponseFor(t, &http.MaxBytesError{Limit: 16384})

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}
