package http

import (
	"github.com/gin-gonic/gin"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
)

// signupLogFields builds the structured fields for a signup application log
// line (LLD §11.4, SB2-66): request_id and trace_id from the request context so
// the line can be joined to its http_request line, plus signup_id, tenant_id,
// state, stage and outcome. Empty values are omitted. No PII (no email, name,
// password or IP).
func signupLogFields(c *gin.Context, stage, outcome, signupID, tenantID, state string) map[string]interface{} {
	f := map[string]interface{}{
		"request_id": gincommon.RequestIDFromContext(c),
		"trace_id":   gincommon.TraceIDFromContext(c),
		"stage":      stage,
		"outcome":    outcome,
	}
	for k, v := range map[string]string{"signup_id": signupID, "tenant_id": tenantID, "state": state} {
		if v != "" {
			f[k] = v
		}
	}
	return f
}
