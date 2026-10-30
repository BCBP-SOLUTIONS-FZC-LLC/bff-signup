package middleware

import (
	"mime"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/problem"
)

// RequireJSON rejects a request with a body method (POST, PUT, PATCH) unless its
// Content-Type is application/json (LLD §5.1). Parameters such as
// "; charset=utf-8" are allowed. A missing or different type returns
// 400 validation_failed, the same shape as the other validation errors.
func RequireJSON() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
			mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
			if err != nil || mediaType != "application/json" {
				gincommon.AbortWithProblem(c, unsupportedContentTypeError())
				return
			}
		}
		c.Next()
	}
}

func unsupportedContentTypeError() *problem.Error {
	e := problem.NewValidationError()
	e.Detail = "Content-Type must be application/json."
	return e
}
