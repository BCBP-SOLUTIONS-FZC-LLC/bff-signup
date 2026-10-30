package http

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	// generated swagger docs — imported for side-effect (registers spec)
	_ "github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/docs/swagger"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/inbound/http/middleware"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/problem"
)

// RouterConfig holds all router dependencies (LLD §3, §13.1).
type RouterConfig struct {
	GinConfig         gincommon.Config // platform observability identity (metrics, logs, traces)
	Handlers          *Handlers
	SessionService    *service.SessionService
	Valkey            port.HandoffStore // used as a pinger for /readyz
	PublicHost        string
	DocsEnabled       bool // serve Swagger UI (always on in dev; gated in prod)
	PaidSignupEnabled bool // Phase 1.5 (LLD §19): enables POST /paid; false → 404
}

// NewRouter builds the Gin engine with all routes and middleware (LLD §5.2).
func NewRouter(cfg RouterConfig) *gin.Engine {
	r := gin.New()

	// ── Platform observability middleware (metrics, structured logging, tracing) ─
	// ObservabilityMiddlewares provides: PanicRecovery, RequestID, Tracing,
	// CorrelationHeaders, Metrics, Logging, and TimeoutMiddleware (innermost).
	// Must run before any BFF-specific middleware so every request is observed.
	r.Use(gincommon.ObservabilityMiddlewares(cfg.GinConfig)...)

	// ── BFF-specific infrastructure middleware ────────────────────────────────
	r.Use(noStoreHeaders())
	r.Use(middleware.BodyLimit()) // 16 KB hard cap — LLD §5.1, §10.5
	r.Use(middleware.ClientIP())
	r.Use(middleware.SessionCookie())
	r.Use(middleware.CSRFToken())

	// ── Swagger UI — GET /swagger/*any ───────────────────────────────────────
	// Served on the same port as the API (:8080).
	// BCBP-branded dark theme (swagger_theme.go) + custom initializer (swagger_initializer.go).
	// Disabled in prod (DocsEnabled: false) since this is a public-facing host.
	if cfg.DocsEnabled {
		stdSwagger := ginSwagger.WrapHandler(swaggerFiles.Handler)
		r.GET("/swagger/*any", func(c *gin.Context) {
			switch {
			case strings.HasSuffix(c.Request.URL.Path, "/index.css"):
				SwaggerThemeHandler(c)
			case c.Request.URL.Path == "/swagger/swagger-initializer.js":
				SwaggerInitializerHandler(c)
			default:
				stdSwagger(c)
			}
		})
	}

	// ── Health / readiness (unauthenticated) ─────────────────────────────────

	// Healthz godoc
	// @Summary      Liveness probe
	// @Description  Returns 200 OK. Used by Kubernetes liveness probe.
	// @Tags         infra
	// @Produce      json
	// @Success      200  {object}  map[string]string
	// @Router       /healthz [get]
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Readyz godoc
	// @Summary      Readiness probe
	// @Description  Returns 200 OK when the service is ready to accept traffic (Valkey reachable + config loaded).
	// @Tags         infra
	// @Produce      json
	// @Success      200  {object}  map[string]string
	// @Failure      503  {object}  ErrorResponse
	// @Router       /readyz [get]
	r.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := cfg.Valkey.Ping(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "valkey unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// ── Public signup routes ──────────────────────────────────────────────────
	v1 := r.Group("/api/v1/signup")
	{
		// SB-1: start session (no CSRF required — GET)
		v1.GET("/session", cfg.Handlers.Session)

		// SB-2: submit trial (CSRF + session required)
		v1.POST("/trial",
			middleware.ValidateOrigin(cfg.PublicHost),
			middleware.RequireJSON(),
			requireValidatedSession(cfg.SessionService),
			cfg.Handlers.SubmitTrial,
		)

		// SB-3: poll status (session-bound, no CSRF)
		v1.GET("/:signup_id/status",
			requireValidatedSession(cfg.SessionService),
			cfg.Handlers.GetStatus,
		)

		// SB-4: change slug (CSRF + session required)
		v1.POST("/:signup_id/slug",
			middleware.ValidateOrigin(cfg.PublicHost),
			middleware.RequireJSON(),
			requireValidatedSession(cfg.SessionService),
			cfg.Handlers.ChangeSlug,
		)

		// SB-5: direct paid signup (Phase 1.5, LLD §19)
		// Returns 404 until PAID_SIGNUP_ENABLED=true so no card data reaches an unready handler.
		if cfg.PaidSignupEnabled {
			v1.POST("/paid",
				middleware.ValidateOrigin(cfg.PublicHost),
				middleware.RequireJSON(),
				requireValidatedSession(cfg.SessionService),
				cfg.Handlers.SubmitPaid,
			)
		} else {
			v1.POST("/paid", func(c *gin.Context) {
				gincommon.AbortWithProblem(c, problem.NewNotFoundError("paid signup is not enabled"))
			})
		}
	}

	return r
}

// requireValidatedSession validates the session cookie and CSRF token (for POST)
// and stores the session in the gin context as "session".
func requireValidatedSession(svc *service.SessionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		sid := middleware.SIDFromContext(c)
		csrfToken := middleware.CSRFTokenFromContext(c)

		var (
			sess *port.Session
			err  error
		)
		if c.Request.Method == http.MethodPost {
			sess, err = svc.ValidateCSRF(c.Request.Context(), sid, csrfToken)
		} else {
			sess, err = svc.GetSession(c.Request.Context(), sid)
		}

		if err != nil {
			HandleError(c, err)
			return
		}

		c.Set("session", sess)
		c.Next()
	}
}

// noStoreHeaders sets security response headers on every response (LLD §10.4).
//   - Cache-Control: no-store — prevents caching of session/CSRF tokens
//   - X-Content-Type-Options: nosniff — blocks MIME sniffing
//   - X-Frame-Options: DENY — blocks clickjacking via iframe embedding
//   - Referrer-Policy: strict-origin-when-cross-origin — prevents signup URLs
//     and CAPTCHA tokens from leaking to third-party origins via Referer header
func noStoreHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Next()
	}
}
