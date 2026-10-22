package middleware

import "github.com/gin-gonic/gin"

const CookieName = "sbff_sid"

// SessionCookie extracts the sbff_sid cookie and sets it in the context
// under the key "sid". Routes that require a session call MustSID().
func SessionCookie() gin.HandlerFunc {
	return func(c *gin.Context) {
		sid, _ := c.Cookie(CookieName)
		c.Set("sid", sid)
		c.Next()
	}
}

// SetSessionCookie writes the sbff_sid cookie onto the response (SB-1, LLD §10.4).
func SetSessionCookie(c *gin.Context, sid string, maxAgeSecs int) {
	c.SetCookie(
		CookieName,
		sid,
		maxAgeSecs,
		"/api/v1/signup",
		"",   // host-only — no domain attribute
		true, // Secure
		true, // HttpOnly
	)
	// SameSite=Strict requires manual header because gin uses SameSite=Lax by default
	c.Header("Set-Cookie", c.Writer.Header().Get("Set-Cookie")+"; SameSite=Strict")
}

// SIDFromContext returns the session id from the gin context.
func SIDFromContext(c *gin.Context) string {
	sid, _ := c.Get("sid")
	if s, ok := sid.(string); ok {
		return s
	}
	return ""
}
