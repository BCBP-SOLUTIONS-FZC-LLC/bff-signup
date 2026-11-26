package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/test/fixtures"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// routerNoopLogger is a silent logger for router tests satisfying loggerAlias.
type routerNoopLogger struct{}

func (routerNoopLogger) Debug(_ string, _ map[string]interface{}) {}
func (routerNoopLogger) Info(_ string, _ map[string]interface{})  {}
func (routerNoopLogger) Warn(_ string, _ map[string]interface{})  {}
func (routerNoopLogger) Error(_ string, _ map[string]interface{}) {}

func TestNewRouter_HealthzReadyzAndPaidDisabled(t *testing.T) {
	// gincommon requires a Domain in its registry; "workflow" is valid.
	// OBSERVABILITY_DOMAIN overrides Config.Domain when Config.Domain is empty.
	t.Setenv("OBSERVABILITY_DOMAIN", "bff")
	t.Setenv("APP_ENV", "test")

	sessions := fixtures.NewFakeSessionStore()
	limiter := fixtures.NewFakeRateLimiter()
	clk := fixtures.NewFakeClock()
	idgen := fixtures.NewFakeIDGenerator()
	handoffs := fixtures.NewFakeHandoffStore()
	orch := service.NewHandoffOrchestrator(handoffs,
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"), clk, 10*time.Millisecond)

	sessionSvc := service.NewSessionService(sessions, limiter, idgen, clk, 20)
	signupSvc := service.NewSignupService(
		handoffs, limiter, &routerFakeCaptcha{}, &routerFakeHasher{},
		orch, idgen, clk,
		[]byte("test-hmac-key-32-bytes-minimum!!"), nil,
		domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true},
		"2026-09",
		3, 5,
	)
	statusSvc := service.NewStatusService(handoffs, limiter, orch, clk, nil, 60)
	h := NewHandlers(sessionSvc, signupSvc, nil, statusSvc, &routerFakeCatalog{},
		"site-key",
		domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true},
		domain.SlugRules{Pattern: `^[a-z0-9-]+$`, Min: 3, Max: 30},
		2500*time.Millisecond, "2026-09", 1800, routerNoopLogger{},
	)

	r := NewRouter(RouterConfig{
		GinConfig:         gincommon.Config{ServiceName: "bff-signup-test"},
		Handlers:          h,
		SessionService:    sessionSvc,
		Valkey:            handoffs,
		PublicHost:        "signup.example.com",
		DocsEnabled:       false,
		PaidSignupEnabled: false, // exercises the 404-stub branch for /paid
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusOK, w2.Code)

	// Paid disabled → POST /api/v1/signup/paid returns 404
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/signup/paid", nil))
	assert.Equal(t, http.StatusNotFound, w3.Code)
}

// pingFailStore wraps FakeHandoffStore and returns an error from Ping.
type pingFailStore struct{ *fixtures.FakeHandoffStore }

func (s *pingFailStore) Ping(_ context.Context) error { return errors.New("valkey down") }

// TestNewRouter_ReadyzPingFails_Returns503 exercises the /readyz → 503 path (B-1 fix).
func TestNewRouter_ReadyzPingFails_Returns503(t *testing.T) {
	t.Setenv("OBSERVABILITY_DOMAIN", "bff")
	t.Setenv("APP_ENV", "test")

	sessions := fixtures.NewFakeSessionStore()
	limiter := fixtures.NewFakeRateLimiter()
	clk := fixtures.NewFakeClock()
	idgen := fixtures.NewFakeIDGenerator()
	handoffs := &pingFailStore{fixtures.NewFakeHandoffStore()}
	orch := service.NewHandoffOrchestrator(handoffs,
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"), clk, 10*time.Millisecond)

	sessionSvc := service.NewSessionService(sessions, limiter, idgen, clk, 20)
	signupSvc := service.NewSignupService(
		handoffs, limiter, &routerFakeCaptcha{}, &routerFakeHasher{},
		orch, idgen, clk,
		[]byte("test-hmac-key-32-bytes-minimum!!"), nil,
		domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true},
		"2026-09",
		3, 5,
	)
	statusSvc := service.NewStatusService(handoffs, limiter, orch, clk, nil, 60)
	h := NewHandlers(sessionSvc, signupSvc, nil, statusSvc, &routerFakeCatalog{},
		"site-key",
		domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true},
		domain.SlugRules{Pattern: `^[a-z0-9-]+$`, Min: 3, Max: 30},
		2500*time.Millisecond, "2026-09", 1800, routerNoopLogger{},
	)

	r := NewRouter(RouterConfig{
		GinConfig:      gincommon.Config{ServiceName: "bff-signup-test"},
		Handlers:       h,
		SessionService: sessionSvc,
		Valkey:         handoffs,
		PublicHost:     "signup.example.com",
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), "valkey")
}

func TestNewRouter_DocsEnabledAndPaidEnabled(t *testing.T) {
	// Second NewRouter call must reuse the same service identity ("bff-signup-test")
	// established by TestNewRouter_HealthzReadyzAndPaidDisabled — metrics are global/Once.
	t.Setenv("OBSERVABILITY_DOMAIN", "bff")
	t.Setenv("APP_ENV", "test")

	sessions := fixtures.NewFakeSessionStore()
	clk := fixtures.NewFakeClock()
	idgen := fixtures.NewFakeIDGenerator()
	handoffs := fixtures.NewFakeHandoffStore()
	orch := service.NewHandoffOrchestrator(handoffs,
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"), clk, 10*time.Millisecond)
	sessionSvc := service.NewSessionService(sessions, fixtures.NewFakeRateLimiter(), idgen, clk, 20)
	signupSvc := service.NewSignupService(handoffs, fixtures.NewFakeRateLimiter(), &routerFakeCaptcha{}, &routerFakeHasher{},
		orch, idgen, clk, []byte("test-hmac-key-32-bytes-minimum!!"), nil,
		domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true}, "2026-09", 3, 5)
	statusSvc := service.NewStatusService(handoffs, fixtures.NewFakeRateLimiter(), orch, clk, nil, 60)
	h := NewHandlers(sessionSvc, signupSvc, nil, statusSvc, &routerFakeCatalog{}, "sk",
		domain.PasswordPolicy{}, domain.SlugRules{}, time.Second, "2026-09", 1800, routerNoopLogger{})

	// Same ServiceName as the first test — metrics identity matches, Init is a no-op
	r := NewRouter(RouterConfig{
		GinConfig:         gincommon.Config{ServiceName: "bff-signup-test"},
		Handlers:          h,
		SessionService:    sessionSvc,
		Valkey:            handoffs,
		PublicHost:        "signup.example.com",
		DocsEnabled:       true, // exercises the Swagger branch
		PaidSignupEnabled: true, // exercises the paid route branch
	})

	// Swagger default branch
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/swagger/index.html", nil))
	assert.NotEqual(t, http.StatusNotFound, w.Code)

	// Swagger CSS branch (→ SwaggerThemeHandler)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/swagger/index.css", nil))
	assert.Equal(t, http.StatusOK, w2.Code)

	// Swagger initializer branch (→ SwaggerInitializerHandler)
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/swagger/swagger-initializer.js", nil))
	assert.Equal(t, http.StatusOK, w3.Code)

	// Paid route is wired (not 404)
	w4 := httptest.NewRecorder()
	r.ServeHTTP(w4, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/signup/paid", nil))
	assert.NotEqual(t, http.StatusNotFound, w4.Code)
}

// ── Inline fakes for router white-box tests ──────────────────────────────────

type routerFakeCaptcha struct{}

func (f *routerFakeCaptcha) Verify(_ context.Context, _, _ string) error { return nil }

type routerFakeHasher struct{}

func (f *routerFakeHasher) Hash(_ string) (*port.HashedPasswordSeed, error) {
	return &port.HashedPasswordSeed{Algorithm: "pbkdf2-sha512", Iterations: 210000}, nil
}

type routerFakeCatalog struct{}

func (f *routerFakeCatalog) Plans(_ context.Context) ([]domain.PlanInfo, error) { return nil, nil }

func TestNoStoreHeaders_SetsHeaders(t *testing.T) {
	r := gin.New()
	r.Use(noStoreHeaders())
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	assert.Equal(t, "max-age=31536000; includeSubDomains", w.Header().Get("Strict-Transport-Security"))
	assert.Equal(t, "strict-origin-when-cross-origin", w.Header().Get("Referrer-Policy"))
}

func newTestSessionSvc(t *testing.T) (*service.SessionService, string, string) {
	t.Helper()
	sessions := fixtures.NewFakeSessionStore()
	svc := service.NewSessionService(sessions, fixtures.NewFakeRateLimiter(), fixtures.NewFakeIDGenerator(), fixtures.NewFakeClock(), 20)
	created, err := svc.Create(context.Background(), "1.2.3.4")
	require.NoError(t, err)
	return svc, created.SID, created.CSRFToken
}

func TestRequireValidatedSession_GET_ValidSID(t *testing.T) {
	svc, sid, _ := newTestSessionSvc(t)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("sid", sid)
		c.Next()
	})
	r.GET("/", requireValidatedSession(svc), func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireValidatedSession_GET_NoSID_Returns401(t *testing.T) {
	sessions := fixtures.NewFakeSessionStore()
	svc := service.NewSessionService(sessions, fixtures.NewFakeRateLimiter(), fixtures.NewFakeIDGenerator(), fixtures.NewFakeClock(), 20)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("sid", "no-such-sid")
		c.Next()
	})
	r.GET("/", requireValidatedSession(svc), func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireValidatedSession_POST_ValidCSRF(t *testing.T) {
	svc, sid, csrfToken := newTestSessionSvc(t)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("sid", sid)
		c.Set("csrf_token", csrfToken)
		c.Next()
	})
	r.POST("/", requireValidatedSession(svc), func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── Swagger handlers ──────────────────────────────────────────────────────────

func TestSwaggerInitializerHandler_ServesJS(t *testing.T) {
	r := gin.New()
	r.GET("/swagger/swagger-initializer.js", SwaggerInitializerHandler)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/swagger/swagger-initializer.js", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "javascript")
	assert.Contains(t, w.Body.String(), "SwaggerUIBundle")
}

func TestSwaggerThemeHandler_ServesCSS(t *testing.T) {
	r := gin.New()
	r.GET("/swagger/index.css", SwaggerThemeHandler)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/swagger/index.css", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, strings.Contains(w.Header().Get("Content-Type"), "css") || w.Body.Len() > 0)
}

// ── handleError / domainStatus edge cases ─────────────────────────────────────

func TestHandleError_NonDomainError_Returns500(t *testing.T) {
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		HandleError(c, errors.New("plain non-domain error"))
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestDomainStatus_UnknownCause_ReturnsDefault500(t *testing.T) {
	unknownCause := errors.New("custom_internal")
	de := domain.NewError(unknownCause, "unexpected situation")
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		HandleError(c, de)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// TestSessionFromContext_WrongType exercises the second branch of sessionFromContext:
// the key "session" exists in context but holds the wrong type (not *port.Session).
func TestSessionFromContext_WrongType_Returns401(t *testing.T) {
	sessions := fixtures.NewFakeSessionStore()
	clk := fixtures.NewFakeClock()
	idgen := fixtures.NewFakeIDGenerator()
	handoffs := fixtures.NewFakeHandoffStore()
	orch := service.NewHandoffOrchestrator(handoffs,
		fixtures.NewFakeRealmProvisioner("kc"), fixtures.NewFakeOrgMembership("tid"), clk, 10*time.Millisecond)

	sessionSvc := service.NewSessionService(sessions, fixtures.NewFakeRateLimiter(), idgen, clk, 20)
	signupSvc := service.NewSignupService(
		handoffs, fixtures.NewFakeRateLimiter(), &routerFakeCaptcha{}, &routerFakeHasher{},
		orch, idgen, clk,
		[]byte("test-hmac-key-32-bytes-minimum!!"), nil,
		domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true},
		"2026-09",
		3, 5,
	)
	statusSvc := service.NewStatusService(handoffs, fixtures.NewFakeRateLimiter(), orch, clk, nil, 60)
	h := NewHandlers(sessionSvc, signupSvc, nil, statusSvc, &routerFakeCatalog{},
		"site-key",
		domain.PasswordPolicy{MinLength: 12, MaxLength: 128, RequireClasses: 3, NotEmail: true},
		domain.SlugRules{Pattern: `^[a-z0-9-]+$`, Min: 3, Max: 30},
		2500*time.Millisecond, "2026-09", 1800, routerNoopLogger{},
	)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		// Store a string instead of *port.Session — triggers the !ok branch in sessionFromContext
		c.Set("session", "not-a-session-pointer")
		c.Set("client_ip", "1.2.3.4")
		c.Next()
	})
	r.POST("/trial", h.SubmitTrial)

	w := httptest.NewRecorder()
	body := strings.NewReader(`{"company_name":"A","captcha_token":"t"}`)
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/trial", body))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestTruncateUserAgent_MultiByteRune exercises the UTF-8 backtrack loop in truncateUserAgent
// where the byte at position 512 is a continuation byte of a multi-byte rune.
func TestTruncateUserAgent_MultiByteRune(t *testing.T) {
	// Build a 600-byte string consisting of 2-byte UTF-8 runes (U+00C9 = É, bytes 0xC3 0x89).
	// Slice at 512 bytes may land in the middle of a rune.
	twoByteRune := "É" // 2 bytes: 0xC3 0x89
	var ua string
	for len(ua) < 600 {
		ua += twoByteRune
	}

	result := truncateUserAgent(ua)
	// Result must be valid UTF-8 and at most 512 bytes
	assert.LessOrEqual(t, len(result), 512)
	assert.True(t, isValidUTF8(result), "truncated UA must be valid UTF-8")
}

// TestTruncateUserAgent_ShortString verifies no-op for strings under the limit.
func TestTruncateUserAgent_ShortString(t *testing.T) {
	short := "Mozilla/5.0"
	assert.Equal(t, short, truncateUserAgent(short))
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

func TestRequireValidatedSession_POST_WrongCSRF_Returns401(t *testing.T) {
	svc, sid, _ := newTestSessionSvc(t)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("sid", sid)
		c.Set("csrf_token", "wrong-csrf-token")
		c.Next()
	})
	r.POST("/", requireValidatedSession(svc), func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// RP accepts terms_acceptance.user_agent only when non-empty, ≤ 512 bytes and
// free of control characters; otherwise it answers 400 (after payment, on SB-5).
func TestTermsUserAgent(t *testing.T) {
	assert.Equal(t, "unknown", termsUserAgent(""))
	assert.Equal(t, "unknown", termsUserAgent("  \t\r\n "))
	assert.Equal(t, "Mozilla/5.0 x", termsUserAgent("Mozilla/5.0\x00\x07 x"))
	assert.Equal(t, "Firefox", termsUserAgent("  Firefox  "))
	long := strings.Repeat("a", 600)
	assert.Len(t, termsUserAgent(long), 512)
	assert.Equal(t, "ok", termsUserAgent("ok\xff"), "invalid UTF-8 dropped")
}
