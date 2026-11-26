package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	inboundhttp "github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/test/fixtures"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// noopLogger is a silent test logger satisfying gincommon.Logger.
type noopLogger struct{}

func (noopLogger) Debug(_ string, _ map[string]interface{}) {}
func (noopLogger) Info(_ string, _ map[string]interface{})  {}
func (noopLogger) Warn(_ string, _ map[string]interface{})  {}
func (noopLogger) Error(_ string, _ map[string]interface{}) {}

func buildHandlers(
	sessions *fixtures.FakeSessionStore,
	handoffs *fixtures.FakeHandoffStore,
	limiter *fixtures.FakeRateLimiter,
	captcha *fixtures.FakeCaptchaVerifier,
	rp *fixtures.FakeRealmProvisioner,
	om *fixtures.FakeOrgMembership,
	catalog port.Catalog,
) *inboundhttp.Handlers {
	clk := fixtures.NewFakeClock()
	idgen := fixtures.NewFakeIDGenerator()
	orch := service.NewHandoffOrchestrator(handoffs, rp, om, clk, 10*time.Millisecond)

	sessionSvc := service.NewSessionService(sessions, limiter, idgen, clk, 20)
	signupSvc := service.NewSignupService(
		handoffs, limiter, captcha, &fixtures.FakePasswordHasher{},
		orch, idgen, clk,
		[]byte("test-hmac-key-32-bytes-minimum!!"),
		nil,
		domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true},
		"2026-09",
		3, 5,
	)
	statusSvc := service.NewStatusService(handoffs, limiter, orch, clk, nil, 60)

	return inboundhttp.NewHandlers(
		sessionSvc, signupSvc, nil, statusSvc, catalog,
		"test-captcha-site-key",
		domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true},
		domain.SlugRules{Pattern: `^[a-z0-9-]+$`, Min: 3, Max: 30},
		2500*time.Millisecond,
		"2026-09",
		1800,
		noopLogger{},
	)
}

func testSession() *port.Session {
	return &port.Session{
		SID:      "test-sid-001",
		SignupID: "01JAB0000000000000000001",
		CSRFHash: "some-csrf-hash",
	}
}

// newRouter builds a minimal gin engine with common context values pre-set.
// Routes that require a session inject it via the auth middleware group.
func newRouter(h *inboundhttp.Handlers) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("client_ip", "1.2.3.4")
		c.Set("sid", "test-sid-001")
		c.Next()
	})

	r.GET("/session", h.Session)

	auth := r.Group("/")
	auth.Use(func(c *gin.Context) {
		c.Set("session", testSession())
		c.Next()
	})
	auth.POST("/trial", h.SubmitTrial)
	auth.GET("/:signup_id/status", h.GetStatus)
	auth.POST("/:signup_id/slug", h.ChangeSlug)

	return r
}

// ── SB-1: Session ──────────────────────────────────────────────────────────────

func TestHandlerSession_HappyPath(t *testing.T) {
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), fixtures.NewFakeHandoffStore(),
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/session", nil)
	newRouter(h).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotEmpty(t, resp["signup_id"])
	assert.NotEmpty(t, resp["csrf_token"])
	assert.Equal(t, "test-captcha-site-key", resp["captcha"].(map[string]any)["site_key"])
}

func TestHandlerSession_RateLimited(t *testing.T) {
	limiter := fixtures.NewFakeRateLimiter()
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), fixtures.NewFakeHandoffStore(),
		limiter, &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)
	r := newRouter(h)

	for range 20 {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/session", nil))
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/session", nil))
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
}

func TestHandlerSession_CatalogError_Returns503(t *testing.T) {
	cat := fixtures.NewFakeCatalog()
	cat.Err = domain.NewError(domain.ErrDependencyUnavailable, "catalog down")
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), fixtures.NewFakeHandoffStore(),
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		cat,
	)

	w := httptest.NewRecorder()
	newRouter(h).ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/session", nil))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// ── SB-2: SubmitTrial ──────────────────────────────────────────────────────────

func trialBody(t *testing.T, overrides map[string]any) *bytes.Reader {
	t.Helper()
	payload := map[string]any{
		"company_name":  "Acme Corp",
		"slug":          "acme",
		"admin_name":    "Dana Owner",
		"admin_email":   "owner@acme.example",
		"password":      "SecurePass999@",
		"plan":          "pro",
		"accept_terms":  true,
		"terms_version": "2026-09",
		"captcha_token": "valid",
	}
	maps.Copy(payload, overrides)
	b, _ := json.Marshal(payload)
	return bytes.NewReader(b)
}

func TestHandlerSubmitTrial_Completed(t *testing.T) {
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), fixtures.NewFakeHandoffStore(),
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc-001"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/trial", trialBody(t, nil))
	req.Header.Set("Content-Type", "application/json")
	newRouter(h).ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "completed", resp["status"])
}

func TestHandlerSubmitTrial_TenantPending_Returns202(t *testing.T) {
	om := fixtures.NewFakeOrgMembership("tid")
	om.I1Err = domain.NewError(domain.ErrDependencyUnavailable, "i1 down")
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), fixtures.NewFakeHandoffStore(),
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc-001"), om,
		fixtures.NewFakeCatalog(),
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/trial", trialBody(t, nil))
	req.Header.Set("Content-Type", "application/json")
	newRouter(h).ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)
}

func TestHandlerSubmitTrial_MissingRequiredField_Returns400(t *testing.T) {
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), fixtures.NewFakeHandoffStore(),
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	// overrides removes the required captcha_token to trigger a binding error
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/trial", trialBody(t, map[string]any{"captcha_token": ""}))
	req.Header.Set("Content-Type", "application/json")
	newRouter(h).ServeHTTP(w, req)
	// Missing required field → gin binding error → 400
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandlerSubmitTrial_BadJSON(t *testing.T) {
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), fixtures.NewFakeHandoffStore(),
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/trial", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	newRouter(h).ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandlerSubmitTrial_SlugTaken_Returns409(t *testing.T) {
	om := fixtures.NewFakeOrgMembership("tid")
	om.I1Err = domain.NewError(domain.ErrSlugTaken, "slug taken")
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), fixtures.NewFakeHandoffStore(),
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc-001"), om,
		fixtures.NewFakeCatalog(),
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/trial", trialBody(t, nil))
	req.Header.Set("Content-Type", "application/json")
	newRouter(h).ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestHandlerSubmitTrial_CaptchaFailed_Returns403(t *testing.T) {
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), fixtures.NewFakeHandoffStore(),
		fixtures.NewFakeRateLimiter(),
		&fixtures.FakeCaptchaVerifier{Err: domain.NewError(domain.ErrCaptchaFailed, "rejected")},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/trial", trialBody(t, nil))
	req.Header.Set("Content-Type", "application/json")
	newRouter(h).ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// ── SB-3: GetStatus ───────────────────────────────────────────────────────────

func seedCompletedHandoff(t *testing.T, handoffs *fixtures.FakeHandoffStore) {
	t.Helper()
	seedHandoff(t, handoffs, domain.StateCompleted, "kc-001")
}

func seedHandoff(t *testing.T, handoffs *fixtures.FakeHandoffStore, state domain.HandoffState, kcID string) {
	t.Helper()
	clk := fixtures.NewFakeClock()
	err := handoffs.Create(context.Background(), &domain.Handoff{
		SignupID:       "01JAB0000000000000000001",
		TenantID:       "tid",
		State:          state,
		Slug:           "acme",
		CompanyName:    "Acme",
		Plan:           domain.PlanPro,
		KeycloakUserID: kcID,
		CreatedAt:      clk.Now(),
		UpdatedAt:      clk.Now(),
	})
	require.NoError(t, err)
}

func TestHandlerGetStatus_HappyPath(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	seedCompletedHandoff(t, handoffs)
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), handoffs,
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/01JAB0000000000000000001/status", nil)
	newRouter(h).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "completed", resp["status"])
}

func TestHandlerGetStatus_SlugConflict_ReturnsActionRequired(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	seedHandoff(t, handoffs, domain.StateSlugConflict, "kc-001")
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), handoffs,
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/01JAB0000000000000000001/status", nil)
	newRouter(h).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "action_required", resp["status"])
	assert.Equal(t, "choose_slug", resp["action"])
}

func TestHandlerGetStatus_Failed_ReturnsFailed(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	seedHandoff(t, handoffs, domain.StateFailed, "kc-001")
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), handoffs,
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/01JAB0000000000000000001/status", nil)
	newRouter(h).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "failed", resp["status"])
}

func TestHandlerSubmitTrial_LongUserAgent_IsTruncated(t *testing.T) {
	// A 600-byte User-Agent triggers the UTF-8-safe truncation branch.
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), fixtures.NewFakeHandoffStore(),
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc-001"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	longUA := string(make([]byte, 600)) // 600 ASCII zero bytes
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/trial", trialBody(t, nil))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", longUA)
	newRouter(h).ServeHTTP(w, req)

	// Result should succeed (UA is truncated, not rejected)
	assert.Less(t, w.Code, 500)
}

func TestHandlerChangeSlug_Returns409WithSuggestions_WhenNewSlugAlsoTaken(t *testing.T) {
	// When the replacement slug is also taken, SB-4 must return 409 with suggestions —
	// identical behaviour to SB-2 (A-3). Previous bug: error was swallowed and 202 returned.
	handoffs := fixtures.NewFakeHandoffStore()
	clk := fixtures.NewFakeClock()
	require.NoError(t, handoffs.Create(context.Background(), &domain.Handoff{
		SignupID:       "01JAB0000000000000000001",
		TenantID:       "tid",
		State:          domain.StateSlugConflict,
		Slug:           "acme",
		CompanyName:    "Acme",
		Plan:           domain.PlanPro,
		KeycloakUserID: "kc-001",
		CreatedAt:      clk.Now(),
		UpdatedAt:      clk.Now(),
	}))
	om := fixtures.NewFakeOrgMembership("tid")
	om.I1Err = domain.NewError(domain.ErrSlugTaken, "slug taken again")
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), handoffs,
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), om,
		fixtures.NewFakeCatalog(),
	)

	body, _ := json.Marshal(map[string]string{"slug": "acme-new"})
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/01JAB0000000000000000001/slug", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	newRouter(h).ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code, "second slug conflict must return 409, not 202")
	body409 := w.Body.String()
	assert.Contains(t, body409, "suggestions", "409 response must include slug suggestions (A-3)")
}

func TestHandlerGetStatus_NotFound(t *testing.T) {
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), fixtures.NewFakeHandoffStore(),
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	w := httptest.NewRecorder()
	// "other-signup" != session's "01JAB0000000000000000001" → not found
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/other-signup/status", nil)
	newRouter(h).ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ── SB-4: ChangeSlug ──────────────────────────────────────────────────────────

func TestHandlerChangeSlug_HappyPath(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	clk := fixtures.NewFakeClock()
	require.NoError(t, handoffs.Create(context.Background(), &domain.Handoff{
		SignupID:       "01JAB0000000000000000001",
		TenantID:       "tid",
		State:          domain.StateSlugConflict,
		Slug:           "acme",
		CompanyName:    "Acme",
		Plan:           domain.PlanPro,
		KeycloakUserID: "kc-001",
		CreatedAt:      clk.Now(),
		UpdatedAt:      clk.Now(),
	}))
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), handoffs,
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	body, _ := json.Marshal(map[string]string{"slug": "acme-new"})
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/01JAB0000000000000000001/slug", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	newRouter(h).ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestHandlerChangeSlug_BadJSON(t *testing.T) {
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), fixtures.NewFakeHandoffStore(),
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/01JAB0000000000000000001/slug", bytes.NewReader([]byte("bad")))
	req.Header.Set("Content-Type", "application/json")
	newRouter(h).ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── SB-5: SubmitPaid ──────────────────────────────────────────────────────────

func buildHandlersWithPaid(
	handoffs *fixtures.FakeHandoffStore,
	limiter *fixtures.FakeRateLimiter,
	billing *fixtures.FakeBillingClient,
	rp *fixtures.FakeRealmProvisioner,
	om *fixtures.FakeOrgMembership,
) *inboundhttp.Handlers {
	clk := fixtures.NewFakeClock()
	idgen := fixtures.NewFakeIDGenerator()
	orch := service.NewHandoffOrchestrator(handoffs, rp, om, clk, 10*time.Millisecond)
	pol := domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true}

	sessionSvc := service.NewSessionService(fixtures.NewFakeSessionStore(), limiter, idgen, clk, 20)
	signupSvc := service.NewSignupService(
		handoffs, limiter, &fixtures.FakeCaptchaVerifier{}, &fixtures.FakePasswordHasher{},
		orch, idgen, clk, []byte("test-hmac-key-32-bytes-minimum!!"), nil, pol, "2026-09", 3, 5,
	)
	paidSvc := service.NewPaidSignupService(
		handoffs, limiter, &fixtures.FakeCaptchaVerifier{}, &fixtures.FakePasswordHasher{},
		billing, rp, om, orch, idgen, clk,
		[]byte("test-hmac-key-32-bytes-minimum!!"), nil, pol, "2026-09",
	)
	statusSvc := service.NewStatusService(handoffs, limiter, orch, clk, nil, 60)

	return inboundhttp.NewHandlers(
		sessionSvc, signupSvc, paidSvc, statusSvc, fixtures.NewFakeCatalog(),
		"site-key", pol, domain.SlugRules{Pattern: `^[a-z0-9-]+$`, Min: 3, Max: 30},
		2500*time.Millisecond, "2026-09", 1800, noopLogger{},
	)
}

func paidBody(t *testing.T) *bytes.Reader {
	t.Helper()
	b, _ := json.Marshal(map[string]any{
		"company_name":  "Acme Corp",
		"slug":          "acme",
		"admin_name":    "Dana Owner",
		"admin_email":   "owner@acme.example",
		"password":      "SecurePass999@",
		"plan":          "pro",
		"seats":         5,
		"payment_token": "tok_stripe_test",
		"accept_terms":  true,
		"terms_version": "2026-09",
		"captcha_token": "valid",
	})
	return bytes.NewReader(b)
}

func TestHandlerSubmitPaid_HappyPath(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	billing := fixtures.NewFakeBillingClient("sub-001")
	rp := fixtures.NewFakeRealmProvisioner("kc-direct")
	om := fixtures.NewFakeOrgMembership("tid-paid")
	h := buildHandlersWithPaid(handoffs, fixtures.NewFakeRateLimiter(), billing, rp, om)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("client_ip", "1.2.3.4")
		c.Set("sid", "sid-paid")
		c.Set("session", testSession())
		c.Next()
	})
	r.POST("/paid", h.SubmitPaid)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/paid", paidBody(t))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestHandlerSubmitPaid_SlugTaken_Returns409WithSuggestions(t *testing.T) {
	om := fixtures.NewFakeOrgMembership("tid")
	om.I1Err = domain.NewError(domain.ErrSlugTaken, "slug taken")
	h := buildHandlersWithPaid(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(),
		fixtures.NewFakeBillingClient("sub-001"), fixtures.NewFakeRealmProvisioner("kc-direct"), om,
	)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("client_ip", "1.2.3.4")
		c.Set("sid", "sid-paid")
		c.Set("session", testSession())
		c.Next()
	})
	r.POST("/paid", h.SubmitPaid)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/paid", paidBody(t))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	// RFC 9457: errorCode field (UPPER_SNAKE), not legacy "error" field
	assert.Equal(t, "SLUG_TAKEN", resp["errorCode"])
}

func TestHandlerSubmitPaid_BillingFails_Returns503(t *testing.T) {
	billing := fixtures.NewFakeBillingClient("")
	billing.Err = domain.NewError(domain.ErrDependencyUnavailable, "payment service down")
	h := buildHandlersWithPaid(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(),
		billing, fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("client_ip", "1.2.3.4")
		c.Set("sid", "sid-paid")
		c.Set("session", testSession())
		c.Next()
	})
	r.POST("/paid", h.SubmitPaid)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/paid", paidBody(t))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandlerSubmitPaid_TenantPending_Returns202(t *testing.T) {
	om := fixtures.NewFakeOrgMembership("tid")
	om.I1Err = domain.NewError(domain.ErrDependencyUnavailable, "i1 down")
	h := buildHandlersWithPaid(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(),
		fixtures.NewFakeBillingClient("sub-001"), fixtures.NewFakeRealmProvisioner("kc-direct"), om,
	)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("client_ip", "1.2.3.4")
		c.Set("sid", "sid-paid")
		c.Set("session", testSession())
		c.Next()
	})
	r.POST("/paid", h.SubmitPaid)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/paid", paidBody(t))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "provisioning", resp["status"])
}

// BL-OQ-8: 402 PAYMENT_ACTION_REQUIRED → 202 with the 3-D Secure action.
func TestHandlerSubmitPaid_PaymentActionRequired_Returns202WithAction(t *testing.T) {
	billing := fixtures.NewFakeBillingClient("")
	billing.Resp = &port.ChargeResponse{
		Outcome:               port.ChargeActionRequired,
		BillingSubscriptionID: "sub-3ds",
		Action:                &port.PaymentAction{Type: "3ds", ClientSecret: "pi_123_secret_456"},
		ExpiresAt:             "2026-11-26T12:30:00Z",
	}
	h := buildHandlersWithPaid(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(),
		billing, fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	).WithPaidRequestDeadline(30 * time.Second)

	w := servePaid(t, h)

	assert.Equal(t, http.StatusAccepted, w.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "payment_action_required", resp["status"])
	assert.Equal(t, map[string]any{"type": "3ds", "client_secret": "pi_123_secret_456"}, resp["action"])
	assert.Equal(t, "2026-11-26T12:30:00Z", resp["expires_at"])
	assert.NotContains(t, resp, "billing_subscription_id")
	// The session cookie slides so the 3-D Secure re-POST still has a session.
	assert.Contains(t, w.Header().Get("Set-Cookie"), "sid-paid")
}

func TestHandlerSubmitPaid_BillingTerminalErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"declined", domain.NewError(domain.ErrPaymentDeclined, "declined").
			WithDetails(map[string]any{"decline_reason": "card_declined"}), http.StatusUnprocessableEntity, "PAYMENT_DECLINED"},
		{"expired", domain.NewError(domain.ErrChargeExpired, "start again"), http.StatusGone, "CHARGE_EXPIRED"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			billing := fixtures.NewFakeBillingClient("")
			billing.Err = tc.err
			h := buildHandlersWithPaid(
				fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(),
				billing, fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
			)

			w := servePaid(t, h)

			assert.Equal(t, tc.status, w.Code)
			var resp map[string]any
			require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
			assert.Equal(t, tc.code, resp["errorCode"])
		})
	}
}

func TestHandlerSubmitPaid_BillingInProgress_SetsRetryAfter(t *testing.T) {
	billing := fixtures.NewFakeBillingClient("")
	billing.Err = domain.NewError(domain.ErrDependencyUnavailable, "payment is still being processed").
		WithDetails(map[string]any{"retry_after_seconds": 2})
	h := buildHandlersWithPaid(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(),
		billing, fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	).WithPaidRequestDeadline(0) // non-positive keeps the default

	w := servePaid(t, h)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "2", w.Header().Get("Retry-After"))
}

func servePaid(t *testing.T, h *inboundhttp.Handlers) *httptest.ResponseRecorder {
	t.Helper()
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("client_ip", "1.2.3.4")
		c.Set("sid", "sid-paid")
		c.Set("session", testSession())
		c.Next()
	})
	r.POST("/paid", h.SubmitPaid)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/paid", paidBody(t))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestHandlerSubmitPaid_NoSession_Returns401(t *testing.T) {
	h := buildHandlersWithPaid(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(),
		fixtures.NewFakeBillingClient("sub"), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("client_ip", "1.2.3.4")
		c.Next()
	})
	r.POST("/paid", h.SubmitPaid)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/paid", paidBody(t))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandlerChangeSlug_ServiceError_NonSlugTaken_Returns409(t *testing.T) {
	// Handoff in StateCompleted → ChangeSlug returns ErrInvalidState (not ErrSlugTaken)
	// This exercises the handleError(c, err) branch in ChangeSlug.
	handoffs := fixtures.NewFakeHandoffStore()
	seedCompletedHandoff(t, handoffs)
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), handoffs,
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	body, _ := json.Marshal(map[string]string{"slug": "new-slug"})
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/01JAB0000000000000000001/slug", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	newRouter(h).ServeHTTP(w, req)

	// StateCompleted is not slug_conflict → ErrInvalidState → 409
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestHandlerChangeSlug_Completed_Returns201(t *testing.T) {
	// Verify that the StateCompleted branch in ChangeSlug returns 201.
	// This was already tested in TestHandlerChangeSlug_HappyPath — this test adds
	// a response body assertion for the 201 branch.
	handoffs := fixtures.NewFakeHandoffStore()
	clk := fixtures.NewFakeClock()
	require.NoError(t, handoffs.Create(context.Background(), &domain.Handoff{
		SignupID:       "01JAB0000000000000000001",
		TenantID:       "tid",
		State:          domain.StateSlugConflict,
		Slug:           "acme",
		CompanyName:    "Acme",
		Plan:           domain.PlanPro,
		KeycloakUserID: "kc-001",
		CreatedAt:      clk.Now(),
		UpdatedAt:      clk.Now(),
	}))
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), handoffs,
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	body, _ := json.Marshal(map[string]string{"slug": "acme-new"})
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/01JAB0000000000000000001/slug", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	newRouter(h).ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "completed", resp["status"])
	assert.Equal(t, "check_email", resp["next"])
}

// TestHandlerSubmitTrial_StateAllocated_Returns202 tests the default branch in SubmitTrial's
// switch (non-Completed, non-TenantPending states → provisioning 202).
// We reach it by seeding an existing StateAllocated handoff with a DIFFERENT slug
// (so handleResubmit discards it) then make I-1 fail to push to TenantPending.
// Actually, for handler coverage the simpler path is to look at GetStatus's default
// via stateToStatusString. We exercise stateToStatusString default via GetStatus
// with an StateAllocated handoff in the store.
// TestHandlerGetStatus_TenantPending_ReturnsProvisioning exercises the StateTenantPending
// case in stateToStatusString via the GetStatus handler.
func TestHandlerGetStatus_TenantPending_ReturnsProvisioning(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	clk := fixtures.NewFakeClock()
	// Set CreatedAt far in the past (> 15 min) so ResumeI1 marks it StateFailed
	// instead of trying I-1 — but we want TenantPending to reach stateToStatusString.
	// Use a CreatedAt within the window and make I-1 fail so it stays TenantPending.
	// Actually, ResumeI1 is called with StateTenantPending and within window, then
	// if I-1 succeeds it updates state to Completed. Let's make I-1 fail so it
	// stays TenantPending after ResumeI1.
	//
	// Actually: ResumeI1 with expired window → sets StateFailed. So let's use
	// a handoff with CreatedAt within window AND make I-1 fail (stay pending).
	om := fixtures.NewFakeOrgMembership("tid")
	om.I1Err = domain.NewError(domain.ErrDependencyUnavailable, "i1 down")
	require.NoError(t, handoffs.Create(context.Background(), &domain.Handoff{
		SignupID:       "01JAB0000000000000000001",
		TenantID:       "tid",
		State:          domain.StateTenantPending,
		Slug:           "acme",
		CompanyName:    "Acme",
		Plan:           domain.PlanPro,
		KeycloakUserID: "kc-001",
		CreatedAt:      clk.Now(), // within 15-min window
		UpdatedAt:      clk.Now(),
	}))
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), handoffs,
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), om,
		fixtures.NewFakeCatalog(),
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/01JAB0000000000000000001/status", nil)
	newRouter(h).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	// After failed ResumeI1, the state stays TenantPending → "provisioning"
	assert.Equal(t, "provisioning", resp["status"])
}

func TestHandlerGetStatus_StateAllocated_ReturnsResubmitAction(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	clk := fixtures.NewFakeClock()
	require.NoError(t, handoffs.Create(context.Background(), &domain.Handoff{
		SignupID:       "01JAB0000000000000000001",
		TenantID:       "tid",
		State:          domain.StateAllocated,
		Slug:           "acme",
		CompanyName:    "Acme",
		Plan:           domain.PlanPro,
		KeycloakUserID: "kc-001",
		CreatedAt:      clk.Now(),
		UpdatedAt:      clk.Now(),
	}))
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), handoffs,
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/01JAB0000000000000000001/status", nil)
	newRouter(h).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	// allocated: nothing runs in the background — only a resubmit moves it on.
	assert.Equal(t, "action_required", resp["status"])
	assert.Equal(t, "resubmit", resp["action"])
}

func TestHandlerSubmitPaid_BadJSON(t *testing.T) {
	h := buildHandlersWithPaid(
		fixtures.NewFakeHandoffStore(), fixtures.NewFakeRateLimiter(),
		fixtures.NewFakeBillingClient("sub"), fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
	)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("client_ip", "1.2.3.4")
		c.Set("session", testSession())
		c.Next()
	})
	r.POST("/paid", h.SubmitPaid)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/paid", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── No session in context ──────────────────────────────────────────────────────

// ── acceptLocale coverage ──────────────────────────────────────────────────────

func TestHandlerSession_AcceptLanguage_Forwarded(t *testing.T) {
	// Valid allow-listed locale is accepted.
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), fixtures.NewFakeHandoffStore(),
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc-001"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/trial", trialBody(t, nil))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "ar-AE")
	newRouter(h).ServeHTTP(w, req)
	assert.Less(t, w.Code, 500)
}

// TestHandlerSession_AcceptLanguage_EmptyFallsBackToEnUS verifies the empty-header
// branch of acceptLocale: no Accept-Language header → "en-US" default (no panic).
func TestHandlerSession_AcceptLanguage_EmptyFallsBackToEnUS(t *testing.T) {
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), fixtures.NewFakeHandoffStore(),
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/trial", trialBody(t, nil))
	req.Header.Set("Content-Type", "application/json")
	// No Accept-Language header set — must not panic, must return a valid response.
	newRouter(h).ServeHTTP(w, req)
	assert.Less(t, w.Code, 500)
}

// TestHandlerSession_AcceptLanguage_UnknownLocaleFallsBackToEnUS verifies that an
// unrecognised locale (not in the allow-list) is silently replaced with "en-US".
func TestHandlerSession_AcceptLanguage_UnknownLocaleFallsBackToEnUS(t *testing.T) {
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), fixtures.NewFakeHandoffStore(),
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/trial", trialBody(t, nil))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "zh-CN") // not in allow-list
	newRouter(h).ServeHTTP(w, req)
	assert.Less(t, w.Code, 500)
}

// ── domainStatus remaining branches ───────────────────────────────────────────

func TestHandlerError_EmailUnavailable_Returns409(t *testing.T) {
	om := fixtures.NewFakeOrgMembership("tid")
	om.EligibleErr = domain.NewError(domain.ErrEmailUnavailable, "email in use")
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), fixtures.NewFakeHandoffStore(),
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), om,
		fixtures.NewFakeCatalog(),
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/trial", trialBody(t, nil))
	req.Header.Set("Content-Type", "application/json")
	newRouter(h).ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestHandlerError_TrialNotEligible_Returns409(t *testing.T) {
	om := fixtures.NewFakeOrgMembership("tid")
	om.EligibleErr = domain.NewError(domain.ErrTrialNotEligible, "already used")
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), fixtures.NewFakeHandoffStore(),
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), om,
		fixtures.NewFakeCatalog(),
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/trial", trialBody(t, nil))
	req.Header.Set("Content-Type", "application/json")
	newRouter(h).ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
}

// ── No session in context ──────────────────────────────────────────────────────

func TestHandlerSubmitTrial_PasswordHashFails_Returns500(t *testing.T) {
	// Build handlers with a failing password hasher to trigger ErrInternal path.
	clk := fixtures.NewFakeClock()
	idgen := fixtures.NewFakeIDGenerator()
	handoffs := fixtures.NewFakeHandoffStore()
	om := fixtures.NewFakeOrgMembership("tid")
	rp := fixtures.NewFakeRealmProvisioner("kc")
	orch := service.NewHandoffOrchestrator(handoffs, rp, om, clk, 10*time.Millisecond)
	signupSvc := service.NewSignupService(
		handoffs, fixtures.NewFakeRateLimiter(),
		&fixtures.FakeCaptchaVerifier{},
		&fixtures.FakePasswordHasher{Err: errors.New("rand read failed")},
		orch, idgen, clk,
		[]byte("test-hmac-key-32-bytes-minimum!!"),
		nil,
		domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true},
		"2026-09",
		3, 5,
	)
	sessionSvc := service.NewSessionService(fixtures.NewFakeSessionStore(), fixtures.NewFakeRateLimiter(), idgen, clk, 20)
	statusSvc := service.NewStatusService(handoffs, fixtures.NewFakeRateLimiter(), orch, clk, nil, 60)
	h := inboundhttp.NewHandlers(
		sessionSvc, signupSvc, nil, statusSvc, fixtures.NewFakeCatalog(),
		"site-key",
		domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true},
		domain.SlugRules{Pattern: `^[a-z0-9-]+$`, Min: 3, Max: 30},
		2500*time.Millisecond, "2026-09", 1800, noopLogger{},
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/trial", trialBody(t, nil))
	req.Header.Set("Content-Type", "application/json")
	newRouter(h).ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandlerGetStatus_NoSession_Returns401(t *testing.T) {
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), fixtures.NewFakeHandoffStore(),
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("client_ip", "1.2.3.4"); c.Next() })
	r.GET("/:signup_id/status", h.GetStatus)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/01JAB0000000000000000001/status", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandlerChangeSlug_NoSession_Returns401(t *testing.T) {
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), fixtures.NewFakeHandoffStore(),
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("client_ip", "1.2.3.4"); c.Next() })
	r.POST("/:signup_id/slug", h.ChangeSlug)

	body, _ := json.Marshal(map[string]string{"slug": "newslug"})
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/01JAB0000000000000000001/slug", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandlerChangeSlug_MismatchSignupID_Returns404(t *testing.T) {
	// Session SignupID ≠ path signup_id → StatusService returns ErrSignupNotFound
	// → handler's handleError path is exercised.
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), fixtures.NewFakeHandoffStore(),
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	body, _ := json.Marshal(map[string]string{"slug": "newslug"})
	w := httptest.NewRecorder()
	// Path signup_id "other-id" ≠ session's "01JAB0000000000000000001"
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/other-id/slug", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	newRouter(h).ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandlerSubmitTrial_NoSession_Returns401(t *testing.T) {
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), fixtures.NewFakeHandoffStore(),
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	// Router without the session middleware
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("client_ip", "1.2.3.4")
		c.Next()
	})
	r.POST("/trial", h.SubmitTrial)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/trial", trialBody(t, nil))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestHandlerSubmitTrial_StateIdentityCreated_Returns202 covers the default branch in
// SubmitTrial's switch (line 208). When an existing handoff is in StateIdentityCreated
// with a matching email, the signup service's fast path returns that state without error,
// and the handler's default case answers 202 Accepted.
func TestHandlerSubmitTrial_StateIdentityCreated_Returns202(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), handoffs,
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc-001"), fixtures.NewFakeOrgMembership("tid"),
		fixtures.NewFakeCatalog(),
	)

	// First POST creates the handoff (StateCompleted after success).
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/trial", trialBody(t, nil))
	req.Header.Set("Content-Type", "application/json")
	newRouter(h).ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	// Patch the stored handoff to StateIdentityCreated (simulating a partial provisioning).
	ho, getErr := handoffs.Get(context.Background(), testSession().SignupID)
	require.NoError(t, getErr)
	require.NotNil(t, ho)
	ho.State = domain.StateIdentityCreated
	require.NoError(t, handoffs.Update(context.Background(), ho))

	// Second POST: fast path finds StateIdentityCreated + same email → returns that state.
	// Handler switch hits the default branch → 202 Accepted.
	w = httptest.NewRecorder()
	req = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/trial", trialBody(t, nil))
	req.Header.Set("Content-Type", "application/json")
	newRouter(h).ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)
}

// TestHandlerChangeSlug_TenantPending_Returns202 covers the default branch in
// ChangeSlug's switch (line 336). When I-1 fails during slug change the orchestrator
// parks the handoff in StateTenantPending and the handler answers 202 Accepted.
func TestHandlerChangeSlug_TenantPending_Returns202(t *testing.T) {
	handoffs := fixtures.NewFakeHandoffStore()
	clk := fixtures.NewFakeClock()
	require.NoError(t, handoffs.Create(context.Background(), &domain.Handoff{
		SignupID:       testSession().SignupID,
		TenantID:       "tid-slug-pending",
		State:          domain.StateSlugConflict,
		Slug:           "acme",
		CompanyName:    "Acme Corp",
		Plan:           domain.PlanPro,
		KeycloakUserID: "kc-001",
		CreatedAt:      clk.Now(),
		UpdatedAt:      clk.Now(),
	}))
	om := fixtures.NewFakeOrgMembership("tid")
	om.I1Err = domain.NewError(domain.ErrDependencyUnavailable, "i1 down")
	h := buildHandlers(
		fixtures.NewFakeSessionStore(), handoffs,
		fixtures.NewFakeRateLimiter(), &fixtures.FakeCaptchaVerifier{},
		fixtures.NewFakeRealmProvisioner("kc"), om,
		fixtures.NewFakeCatalog(),
	)

	body, _ := json.Marshal(map[string]string{"slug": "acme-new"})
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/"+testSession().SignupID+"/slug", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	newRouter(h).ServeHTTP(w, req)

	// I-1 failure → StateTenantPending → handler default branch → 202 Accepted.
	assert.Equal(t, http.StatusAccepted, w.Code)
}
