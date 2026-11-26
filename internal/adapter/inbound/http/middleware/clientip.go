package middleware

import (
	"net"

	"github.com/gin-gonic/gin"
)

const clientIPKey = "client_ip"

// ClientIP extracts the real client IP from the x-envoy-external-address header
// set by Envoy (xff_num_trusted_hops: 1 strips spoofed XFF headers, LLD §10.2, N-8).
// Never parses X-Forwarded-For directly.
func ClientIP() gin.HandlerFunc {
	return func(c *gin.Context) {
		// The value keys the per-IP rate limit (sbff:rl:ip:<ip>) and is sent to
		// Turnstile and RP, so only a well-formed address is accepted, in
		// canonical form. Anything else falls back like a missing header.
		ip := ""
		if parsed := net.ParseIP(c.GetHeader("x-envoy-external-address")); parsed != nil {
			ip = parsed.String()
		} else {
			// Fallback for local dev (no Envoy). In the mesh the edge Envoy must set
			// the header (use_remote_address / xff_num_trusted_hops, LLD §10.2).
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
