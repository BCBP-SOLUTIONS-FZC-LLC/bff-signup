package middleware

// White-box tests for middleware helper functions.
// These must live in the same package to set arbitrary types in gin context.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestClientIPFromContext_WrongType exercises the `!ok` branch when context
// holds a non-string value under the client_ip key.
func TestClientIPFromContext_WrongType_ReturnsEmpty(t *testing.T) {
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		// Set an int instead of string — triggers the !ok branch
		c.Set(clientIPKey, 42)
		ip := ClientIPFromContext(c)
		assert.Equal(t, "", ip, "must return empty string when value is wrong type")
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestSIDFromContext_WrongType exercises the `!ok` branch when context
// holds a non-string value under the sid key.
func TestSIDFromContext_WrongType_ReturnsEmpty(t *testing.T) {
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		// Set a bool instead of string — triggers the !ok branch
		c.Set("sid", true)
		sid := SIDFromContext(c)
		assert.Equal(t, "", sid, "must return empty string when value is wrong type")
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestClientIPFromContext_Missing exercises the case where client_ip key is not set.
func TestClientIPFromContext_Missing_ReturnsEmpty(t *testing.T) {
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		ip := ClientIPFromContext(c)
		assert.Equal(t, "", ip)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestSetSessionCookie_SameSiteStrict verifies that the sbff_sid cookie is written
// with SameSite=Strict. A regression here would silently weaken CSRF protection.
func TestSetSessionCookie_SameSiteStrict(t *testing.T) {
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		SetSessionCookie(c, "test-session-id", 1800)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1, "exactly one cookie must be set")
	assert.Equal(t, CookieName, cookies[0].Name)
	assert.Equal(t, http.SameSiteStrictMode, cookies[0].SameSite, "SameSite must be Strict to prevent CSRF")
	assert.True(t, cookies[0].HttpOnly, "cookie must be HttpOnly")
	assert.True(t, cookies[0].Secure, "cookie must be Secure")
}

// ── Body limit middleware ──────────────────────────────────────────────────────

// TestBodyLimit_ContentLengthExceeds16KB verifies the fast-path rejection:
// a request whose Content-Length header exceeds 16 KB returns 413 before
// the body is read (LLD §5.1, §10.5).
func TestBodyLimit_ContentLengthExceeds16KB(t *testing.T) {
	r := gin.New()
	r.Use(BodyLimit())
	r.POST("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", http.NoBody)
	req.ContentLength = maxBodyBytes + 1
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	assert.Contains(t, w.Body.String(), "REQUEST_TOO_LARGE")
}

// TestBodyLimit_Exactly16KB verifies the boundary: a body of exactly 16 KB is accepted.
func TestBodyLimit_Exactly16KB(t *testing.T) {
	r := gin.New()
	r.Use(BodyLimit())
	r.POST("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	body := make([]byte, maxBodyBytes)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", bytes.NewReader(body))
	req.ContentLength = maxBodyBytes
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// TestBodyLimit_ChunkedBodyExceeds16KB verifies the slow-path rejection via
// MaxBytesReader: a chunked/streaming body that omits Content-Length but exceeds
// 16 KB returns 413 when the handler reads it (LLD §5.1, §10.5).
// Uses BodyTooLargeFromError (also in this package) to map the MaxBytesError → 413.
func TestBodyLimit_ChunkedBodyExceeds16KB(t *testing.T) {
	r := gin.New()
	r.Use(BodyLimit())
	r.POST("/", func(c *gin.Context) {
		// Force the body to be read — MaxBytesReader fires when the limit is exceeded.
		buf := make([]byte, maxBodyBytes+512)
		_, err := c.Request.Body.Read(buf)
		if p := BodyTooLargeFromError(err); p != nil {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"errorCode": "REQUEST_TOO_LARGE"})
			c.Abort()
			return
		}
		c.Status(http.StatusOK)
	})

	body := make([]byte, maxBodyBytes+512)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", bytes.NewReader(body))
	req.ContentLength = -1 // no Content-Length → slow-path via MaxBytesReader
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	assert.Contains(t, w.Body.String(), "REQUEST_TOO_LARGE")
}

// TestSIDFromContext_Missing exercises the case where sid key is not set.
func TestSIDFromContext_Missing_ReturnsEmpty(t *testing.T) {
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		sid := SIDFromContext(c)
		assert.Equal(t, "", sid)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

// ClientIP accepts only a well-formed x-envoy-external-address, in canonical
// form: the value keys the per-IP rate limit and is sent to Turnstile and RP.
func TestClientIP_ValidatesHeader(t *testing.T) {
	for header, want := range map[string]string{
		"203.0.113.7":               "203.0.113.7",
		"2001:DB8::0001":            "2001:db8::1",
		"not-an-ip":                 "192.0.2.1", // falls back to the peer
		"203.0.113.7, 198.51.100.1": "192.0.2.1",
		strings.Repeat("9", 4096):   "192.0.2.1",
	} {
		r := gin.New()
		var got string
		r.Use(ClientIP())
		r.GET("/", func(c *gin.Context) { got = ClientIPFromContext(c) })
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		req.RemoteAddr = "192.0.2.1:1234"
		req.Header.Set("x-envoy-external-address", header)
		r.ServeHTTP(httptest.NewRecorder(), req)
		assert.Equal(t, want, got, "header %.20q", header)
	}
}
