package http

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// SB2-66 / OBS-42: signup log lines carry request_id, trace_id, signup_id,
// tenant_id, state, stage and outcome, and nothing personal.
func TestSignupLogFields_CarriesAllRequiredFields(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("request_id", "req-1")
	c.Set("trace_id", "trace-1")

	f := signupLogFields(c, "submit", "success", "sig-1", "ten-1", "completed")

	assert.Equal(t, map[string]interface{}{
		"request_id": "req-1",
		"trace_id":   "trace-1",
		"signup_id":  "sig-1",
		"tenant_id":  "ten-1",
		"state":      "completed",
		"stage":      "submit",
		"outcome":    "success",
	}, f)
}

func TestSignupLogFields_OmitsEmptyValues(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	f := signupLogFields(c, "submit", "failed", "sig-1", "", "")

	assert.NotContains(t, f, "tenant_id")
	assert.NotContains(t, f, "state")
	assert.Equal(t, "failed", f["outcome"])
}
