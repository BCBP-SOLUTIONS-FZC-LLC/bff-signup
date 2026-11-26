package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

// BL-OQ-8: Billing REQUEST_IN_PROGRESS / 503 Retry-After reaches the browser.
func TestHandleError_DependencyUnavailableWithRetryAfterSetsHeader(t *testing.T) {
	err := domain.NewError(domain.ErrDependencyUnavailable, "payment is still being processed").
		WithDetails(map[string]any{"retry_after_seconds": 3})

	w := errorResponseFor(t, err)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "3", w.Header().Get("Retry-After"))
}

func TestHandleError_PaymentErrorsCarryErrorCode(t *testing.T) {
	w := errorResponseFor(t, domain.NewError(domain.ErrPaymentDeclined, "the payment was declined").
		WithDetails(map[string]any{"decline_reason": "insufficient_funds"}))
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), `"errorCode":"PAYMENT_DECLINED"`)
	assert.Contains(t, w.Body.String(), `"decline_reason":"insufficient_funds"`)

	w = errorResponseFor(t, domain.NewError(domain.ErrChargeExpired, "start again"))
	assert.Equal(t, http.StatusGone, w.Code)
	assert.Contains(t, w.Body.String(), `"errorCode":"CHARGE_EXPIRED"`)
}

// errorResponseWithCtx runs HandleError on a request whose context is ctx.
func errorResponseWithCtx(t *testing.T, ctx context.Context, err error) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	r := newEngine()
	var aborted bool
	r.GET("/", func(c *gin.Context) {
		HandleError(c, err)
		aborted = c.IsAborted()
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil))
	return w, aborted
}

// The request deadline (gincommon RequestTimeout) expired: HandleError writes
// nothing, so gincommon's timeout middleware writes its 503 REQUEST_TIMEOUT
// problem and counts the timeout (as iam-org-membership does).
func TestHandleError_RequestDeadlineExpiredLeavesResponseToGincommon(t *testing.T) {
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()

	w, aborted := errorResponseWithCtx(t, ctx, domain.NewError(domain.ErrDependencyUnavailable, "billing timed out"))

	assert.True(t, aborted)
	assert.Empty(t, w.Body.String(), "no body: gincommon's timeout middleware owns the 503")
	assert.Empty(t, w.Header().Get("Content-Type"))
}

// The client disconnected: 499, not a misleading 5xx.
func TestHandleError_ClientDisconnectedRecords499(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	w, aborted := errorResponseWithCtx(t, ctx, context.Canceled)

	assert.True(t, aborted)
	assert.Equal(t, clientClosedRequest, w.Code)
	assert.Empty(t, w.Body.String())
}

// A handler's own shorter deadline (REQUEST_DEADLINE_MS) leaves the request
// context alive, so the error is still mapped to a problem response.
func TestHandleError_HandlerDeadlineStillMapsToProblem(t *testing.T) {
	w, _ := errorResponseWithCtx(t, t.Context(), domain.NewError(domain.ErrDependencyUnavailable, "rp timed out"))

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/problem+json")
	assert.Contains(t, w.Body.String(), `"errorCode":"DEPENDENCY_UNAVAILABLE"`)
}

// The nil-request guard: no request means no request context to defer to.
func TestLeaveToRequestContext_NilRequest(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	assert.False(t, leaveToRequestContext(c, context.Canceled))
}

// A business error that happens to come back after the deadline is still that
// error, not a timeout (same rule as iam-org-membership).
func TestHandleError_BusinessErrorAfterDeadlineIsStillMapped(t *testing.T) {
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()

	w, _ := errorResponseWithCtx(t, ctx, domain.NewError(domain.ErrValidationFailed, "bad slug"))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"errorCode":"VALIDATION_FAILED"`)
}
