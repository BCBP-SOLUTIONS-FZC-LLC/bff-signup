package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const csrfHeader = "X-CSRF-Token"

// CSRFToken extracts the X-CSRF-Token header and sets it as "csrf_token" in context.
func CSRFToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("csrf_token", c.GetHeader(csrfHeader))
		c.Next()
	}
}

// CSRFTokenFromContext returns the CSRF token from the gin context.
func CSRFTokenFromContext(c *gin.Context) string {
	t, _ := c.Get("csrf_token")
	if s, ok := t.(string); ok {
		return s
	}
	return ""
}

// ValidateOrigin checks that the Origin header matches the expected host on POST
// requests (LLD §10.4). Must be used on all state-changing routes.
func ValidateOrigin(expectedHost string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodPost {
			c.Next()
			return
		}
		origin := c.GetHeader("Origin")
		if origin == "" || origin != "https://"+expectedHost {
			c.JSON(http.StatusForbidden, gin.H{
				"error":  "csrf_invalid",
				"status": http.StatusForbidden,
			})
			c.Abort()
			return
		}
		c.Next()
	}
}
