package http

import "github.com/gin-gonic/gin"

// newEngine returns the Gin engine with no trusted proxies (OBS-48, LLD §10.2 N-8).
//
// With an empty trusted-proxy list Gin never believes X-Forwarded-For or
// X-Real-IP, so c.ClientIP() is the TCP peer address. The real client IP comes
// only from x-envoy-external-address (middleware.ClientIP); when that header is
// missing, a client can no longer choose its own rate-limit key by sending a
// forged X-Forwarded-For. A nil list cannot fail to parse, so the error is dropped.
func newEngine() *gin.Engine {
	r := gin.New()
	_ = r.SetTrustedProxies(nil)
	return r
}
