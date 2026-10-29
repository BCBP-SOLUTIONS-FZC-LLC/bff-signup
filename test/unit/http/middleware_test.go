package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/inbound/http/middleware"
)

// ── ClientIP ──────────────────────────────────────────────────────────────────

func TestClientIP_EnvoyHeader(t *testing.T) {
	r := gin.New()
	r.Use(middleware.ClientIP())
	r.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, middleware.ClientIPFromContext(c))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set("x-envoy-external-address", "10.0.0.1")
	r.ServeHTTP(w, req)

	assert.Equal(t, "10.0.0.1", w.Body.String())
}

func TestClientIP_FallbackToRemoteAddr(t *testing.T) {
	r := gin.New()
	r.Use(middleware.ClientIP())
	var captured string
	r.GET("/", func(c *gin.Context) {
		captured = middleware.ClientIPFromContext(c)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	// No x-envoy-external-address → falls back to c.ClientIP()
	r.ServeHTTP(w, req)
	assert.NotEmpty(t, captured)
}

// ── CSRFToken ────────────────────────────────────────────────────────────────

func TestCSRFToken_ExtractsHeader(t *testing.T) {
	r := gin.New()
	r.Use(middleware.CSRFToken())
	r.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, middleware.CSRFTokenFromContext(c))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set("X-CSRF-Token", "my-csrf-token")
	r.ServeHTTP(w, req)

	assert.Equal(t, "my-csrf-token", w.Body.String())
}

func TestCSRFToken_EmptyWhenAbsent(t *testing.T) {
	r := gin.New()
	r.Use(middleware.CSRFToken())
	r.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, middleware.CSRFTokenFromContext(c))
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	assert.Empty(t, w.Body.String())
}

// ── ValidateOrigin ────────────────────────────────────────────────────────────

func TestValidateOrigin_AllowsMatchingOrigin(t *testing.T) {
	r := gin.New()
	r.Use(middleware.ValidateOrigin("signup.example.com"))
	r.POST("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
	req.Header.Set("Origin", "https://signup.example.com")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestValidateOrigin_BlocksMismatchedOrigin(t *testing.T) {
	r := gin.New()
	r.Use(middleware.ValidateOrigin("signup.example.com"))
	r.POST("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestValidateOrigin_PassesGETWithoutOrigin(t *testing.T) {
	r := gin.New()
	r.Use(middleware.ValidateOrigin("signup.example.com"))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── SessionCookie / SIDFromContext ────────────────────────────────────────────

func TestSessionCookie_ExtractsSID(t *testing.T) {
	r := gin.New()
	r.Use(middleware.SessionCookie())
	r.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, middleware.SIDFromContext(c))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "sbff_sid", Value: "test-session-id"})
	r.ServeHTTP(w, req)

	assert.Equal(t, "test-session-id", w.Body.String())
}

func TestSessionCookie_EmptyWhenNoCookie(t *testing.T) {
	r := gin.New()
	r.Use(middleware.SessionCookie())
	r.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, middleware.SIDFromContext(c))
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	assert.Empty(t, w.Body.String())
}
