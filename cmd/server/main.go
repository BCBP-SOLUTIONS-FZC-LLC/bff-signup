package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/inbound/http"
	bffmetrics "github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/metrics"
	valkeystore "github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/valkey"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/logger"
)

// buildVersion is injected at build time via -ldflags (see Makefile / Dockerfile).
var buildVersion = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "startup: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	appEnv := resolveAppEnv()

	if !isDevLikeAppEnv(appEnv) {
		gin.SetMode(gin.ReleaseMode)
	}

	// ── 1. Logger — structured Zap via platform-gincommon ────────────────────
	// All logs go through gincommon's sink: consistent JSON in prod, human-
	// readable in dev/test. Never use the standard library log package after
	// this point — it bypasses the OTel trace-ID injection and sampling.
	log, err := logger.NewLogger(loggerEnv(appEnv))
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}

	// ── 2. Config (fail-fast before any connections) ──────────────────────────
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	// ── 3. Platform observability — tracing + metrics ─────────────────────────
	ginCfg := gincommon.Config{
		Logger:       log,
		ServiceName:  "bff-signup",
		BuildVersion: buildVersion,
		Domain:       "bff",
		Environment:  appEnv,
		// RequestTimeout is the hard per-request deadline enforced by
		// gincommon's TimeoutMiddleware (innermost in ObservabilityMiddlewares).
		// It matches the config value so metrics, the access log and the server
		// span all record the same timeout the caller receives.
		RequestTimeout: time.Duration(cfg.RequestDeadlineMs) * time.Millisecond,
	}

	// Install gincommon's TracerProvider so every request gets a valid trace ID
	// even when OTEL_EXPORTER_OTLP_ENDPOINT is unset (dev). Uses the same
	// identity triple (domain, service, environment) as metrics.
	shutdownTracing, err := gincommon.InitTracingWithConfig(gincommon.TracingConfig{
		ServiceName:  ginCfg.ServiceName,
		BuildVersion: ginCfg.BuildVersion,
		Domain:       ginCfg.Domain,
		Environment:  ginCfg.Environment,
		Logger:       log,
	})
	if err != nil {
		return fmt.Errorf("init tracing: %w", err)
	}

	// Initialise gincommon's metrics registry once before the router so all
	// bff_signup_* collectors share the same {domain, service, environment}
	// const labels. NewRouter applies the same middleware slice to the engine.
	_ = gincommon.ObservabilityMiddlewares(ginCfg)

	// Register bff-signup's business metrics (LLD §11.2) after gincommon so
	// the {domain, service, environment} identity is fixed. Mirrors the
	// registration order used by iam-org-membership (cmd/server/main.go).
	bffmetrics.Register(appEnv)

	// ── 4. Valkey store adapters (wiring.go) ─────────────────────────────────
	// buildValkeyStores calls valkeystore.NewClient which sets tight timeouts
	// (100ms dial / 50ms read+write) matching iam-org-membership's pattern.
	sessionStore, handoffStore, rateLimiter, rdb := buildValkeyStores(cfg.ValkeyURL)
	defer func() { _ = rdb.Close() }()

	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if err := valkeystore.Ping(pingCtx, rdb); err != nil {
		return fmt.Errorf("valkey ping: %w", err)
	}

	// ── 6. Outbound HTTP adapters (wiring.go) ─────────────────────────────────
	rp := buildRealmProvisioner(cfg)
	om := buildOrgMembership(cfg)
	captchaVerifier := buildTurnstile(cfg)
	passwordHasher := buildPasswordHasher(cfg)
	catalogClient := buildCatalog(cfg)
	billingClient := buildBilling(cfg) // Phase 1.5: stub unless PAID_SIGNUP_ENABLED=true

	// ── 7. Services ───────────────────────────────────────────────────────────
	clk := port.RealClock{}
	idgen := &defaultIDGen{}

	orchestrator := service.NewHandoffOrchestrator(handoffStore, rp, om, clk)
	sessionSvc := service.NewSessionService(sessionStore, rateLimiter, idgen, clk)
	signupSvc := service.NewSignupService(
		handoffStore, rateLimiter, captchaVerifier, passwordHasher,
		orchestrator, idgen, clk,
		[]byte(cfg.HMACKey), cfg.ReservedSlugs, cfg.PasswordPolicy(),
		cfg.CurrentTermsVersion,
	)
	paidSignupSvc := service.NewPaidSignupService(
		handoffStore, rateLimiter, captchaVerifier, passwordHasher,
		billingClient, rp, om, idgen, clk,
		[]byte(cfg.HMACKey), cfg.ReservedSlugs, cfg.PasswordPolicy(),
		cfg.CurrentTermsVersion,
	)
	statusSvc := service.NewStatusService(handoffStore, rateLimiter, orchestrator, clk, cfg.ReservedSlugs)

	// ── 8. HTTP handlers and router ───────────────────────────────────────────
	handlers := httpadapter.NewHandlers(
		sessionSvc, signupSvc, paidSignupSvc, statusSvc,
		catalogClient,
		cfg.CaptchaSiteKey,
		cfg.PasswordPolicy(),
		cfg.SlugRules(),
		cfg.RequestDeadline(),
		cfg.CurrentTermsVersion,
	)
	router := httpadapter.NewRouter(httpadapter.RouterConfig{
		GinConfig:         ginCfg,
		Handlers:          handlers,
		SessionService:    sessionSvc,
		Valkey:            handoffStore,
		PublicHost:        cfg.PublicHost,
		DocsEnabled:       isDevLikeAppEnv(appEnv),
		PaidSignupEnabled: cfg.PaidSignupEnabled,
	})

	// ── 9. Metrics server (port 9090 — monitoring namespace only) ────────────
	// A dedicated mux on METRICS_PORT exposes /metrics so it is never reachable
	// from the public internet (Istio NetworkPolicy restricts :9090 to the
	// monitoring namespace). Matches the platform standard (LLD §11, BFF HLD §11).
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", gincommon.MetricsHandler())
	metricsSrv := &http.Server{
		Addr:        ":" + cfg.MetricsPort,
		Handler:     metricsMux,
		ReadTimeout: 5 * time.Second,
		IdleTimeout: 30 * time.Second,
	}
	go func() {
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("metrics server error", map[string]interface{}{"err": err.Error()})
		}
	}()

	// ── 10. API server ────────────────────────────────────────────────────────
	srv := &http.Server{
		Addr:         ":" + cfg.AppPort,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: time.Duration(cfg.RequestDeadlineMs+500) * time.Millisecond,
		IdleTimeout:  60 * time.Second,
	}

	// ── 11. Graceful shutdown ─────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		log.Info("bff-signup starting", map[string]interface{}{
			"port": cfg.AppPort,
			"env":  appEnv,
		})
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("server: %w", err)
	case sig := <-quit:
		log.Info("shutting down", map[string]interface{}{"signal": sig.String()})
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("api server shutdown", map[string]interface{}{"err": err.Error()})
	}
	if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("metrics server shutdown", map[string]interface{}{"err": err.Error()})
	}
	if err := rdb.Close(); err != nil {
		log.Error("valkey close", map[string]interface{}{"err": err.Error()})
	}
	shutdownTracing()
	if err := gincommon.Shutdown(log); err != nil {
		log.Error("gincommon shutdown", map[string]interface{}{"err": err.Error()})
	}
	log.Info("shutdown complete", nil)
	return nil
}

// ── ID generator ──────────────────────────────────────────────────────────────

type defaultIDGen struct{}

func (g *defaultIDGen) NewULID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

func (g *defaultIDGen) NewUUIDv7() string {
	id, _ := uuid.NewV7()
	return id.String()
}
