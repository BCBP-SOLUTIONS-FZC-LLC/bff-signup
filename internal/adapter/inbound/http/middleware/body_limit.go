package middleware

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/problem"
)

// maxBodyBytes is the hard cap mandated by LLD §5.1 and §10.5.
const maxBodyBytes int64 = 16 * 1024

// BodyLimit rejects requests whose body exceeds 16 KB (LLD §5.1, §10.5).
// Two layers of defence:
//   - Content-Length fast-path: rejects before the body is read.
//   - MaxBytesReader slow-path: catches chunked/streaming bodies that omit Content-Length.
func BodyLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > maxBodyBytes {
			gincommon.AbortWithProblem(c, bodyTooLargeError())
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
		c.Next()
	}
}

// BodyTooLargeFromError returns a 413 problem if err is (or wraps) *http.MaxBytesError,
// otherwise returns nil. Called from HandleError so the slow-path chunked case maps
// to 413 rather than falling through to 500.
func BodyTooLargeFromError(err error) *problem.Error {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return bodyTooLargeError()
	}
	return nil
}

func bodyTooLargeError() *problem.Error {
	return problem.NewError(
		problem.TypeForStatus(http.StatusRequestEntityTooLarge),
		"request body exceeds 16 KB limit",
	).WithCode("REQUEST_TOO_LARGE")
}
