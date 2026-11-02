package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRequireJSON(t *testing.T) {
	r := gin.New()
	r.Use(RequireJSON())
	r.POST("/", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	cases := []struct {
		name        string
		method      string
		contentType string
		want        int
	}{
		{"missing content-type (SB2-H11)", http.MethodPost, "", http.StatusBadRequest},
		{"text/plain (SB2-H12)", http.MethodPost, "text/plain", http.StatusBadRequest},
		{"form encoded", http.MethodPost, "application/x-www-form-urlencoded", http.StatusBadRequest},
		{"application/json", http.MethodPost, "application/json", http.StatusOK},
		{"json with charset", http.MethodPost, "application/json; charset=utf-8", http.StatusOK},
		{"GET needs no content-type", http.MethodGet, "", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), tc.method, "/", strings.NewReader("{}"))
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			assert.Equal(t, tc.want, w.Code)
		})
	}
}
