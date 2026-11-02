package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/inbound/http/middleware"
)

func clientIPSeenBy(t *testing.T, headers map[string]string) string {
	t.Helper()
	r := newEngine()
	r.Use(middleware.ClientIP())
	var got string
	r.GET("/", func(c *gin.Context) {
		got = middleware.ClientIPFromContext(c)
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:4321"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	r.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

// S-04 / OBS-48: without the Envoy header, a forged X-Forwarded-For must not
// become the client IP (and so must not choose the rate-limit key).
func TestNewEngine_ForgedForwardedHeadersAreIgnored(t *testing.T) {
	got := clientIPSeenBy(t, map[string]string{
		"X-Forwarded-For": "198.51.100.99",
		"X-Real-IP":       "198.51.100.98",
	})
	assert.Equal(t, "10.0.0.5", got)
}

// Control: when Envoy sets x-envoy-external-address, that value is used.
func TestNewEngine_EnvoyExternalAddressStillWins(t *testing.T) {
	got := clientIPSeenBy(t, map[string]string{
		"x-envoy-external-address": "203.0.113.7",
		"X-Forwarded-For":          "198.51.100.99",
	})
	assert.Equal(t, "203.0.113.7", got)
}
