package middleware

import "github.com/gin-gonic/gin"

const clientIPKey = "client_ip"

// ClientIP extracts the real client IP from the x-envoy-external-address header
// set by Envoy (xff_num_trusted_hops: 1 strips spoofed XFF headers, LLD §10.2, N-8).
// Never parses X-Forwarded-For directly.
func ClientIP() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.GetHeader("x-envoy-external-address")
		if ip == "" {
			// Fallback for local dev (no Envoy)
			ip = c.ClientIP()
		}
		c.Set(clientIPKey, ip)
		c.Next()
	}
}

// ClientIPFromContext returns the client IP stored by the ClientIP middleware.
func ClientIPFromContext(c *gin.Context) string {
	ip, _ := c.Get(clientIPKey)
	if s, ok := ip.(string); ok {
		return s
	}
	return ""
}
