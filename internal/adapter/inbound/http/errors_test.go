package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
)

func errorResponseFor(t *testing.T, err error) *httptest.ResponseRecorder {
	t.Helper()
	r := newEngine()
	r.GET("/", func(c *gin.Context) { HandleError(c, err) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	return w
}

// SB1-02 / SB2-34 (OBS-51): a 429 carries the standard Retry-After header.
func TestHandleError_RateLimitedSetsRetryAfterHeader(t *testing.T) {
	err := domain.NewError(domain.ErrRateLimited, "too many").
		WithDetails(map[string]any{"retry_after_seconds": 3599, "scope": "ip"})

	w := errorResponseFor(t, err)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "3599", w.Header().Get("Retry-After"))
	assert.Contains(t, w.Body.String(), "retry_after_seconds")
}

func TestHandleError_OtherErrorsHaveNoRetryAfterHeader(t *testing.T) {
	w := errorResponseFor(t, domain.NewError(domain.ErrDependencyUnavailable, "down"))

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Empty(t, w.Header().Get("Retry-After"))
}
