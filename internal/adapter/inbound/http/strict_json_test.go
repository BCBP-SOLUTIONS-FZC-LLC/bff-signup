package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/gin-gonic/gin"
)

const validTrialBody = `"company_name":"Acme","admin_name":"Ann","admin_email":"ann@example.com",` +
	`"password":"Str0ng!Passw0rd","plan":"starter","accept_terms":true,` +
	`"terms_version":"2026-09","captcha_token":"t"`

func bindTrial(t *testing.T, body string) error {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/trial", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	var req TrialSignupRequest
	return gincommon.ShouldBindJSON(c, &req)
}

// SB2-07: an undeclared field must be rejected (LLD §10.5 strict decoding).
func TestTrialSignupRequest_UnknownField_Rejected(t *testing.T) {
	err := bindTrial(t, `{`+validTrialBody+`,"evil":"x"}`)
	assert.Error(t, err)
}

// Control: the same body without the extra field still binds cleanly.
func TestTrialSignupRequest_KnownFieldsOnly_Accepted(t *testing.T) {
	err := bindTrial(t, `{`+validTrialBody+`}`)
	assert.NoError(t, err)
}
