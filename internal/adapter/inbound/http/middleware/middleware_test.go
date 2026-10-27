package middleware

// White-box tests for middleware helper functions.
// These must live in the same package to set arbitrary types in gin context.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
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
