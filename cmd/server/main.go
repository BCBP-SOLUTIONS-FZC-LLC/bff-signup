package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/service"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("startup: %v", err)
	}
}

func run() error {
	// ── 1. Config (fail-fast before any connections) ──────────────────────────
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	// ── 2. Valkey connection ──────────────────────────────────────────────────
	rdb, err := buildValkeyClient(cfg.ValkeyURL)
	if err != nil {
		return fmt.Errorf("valkey URL: %w", err)
	}
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		return fmt.Errorf("valkey ping: %w", err)
	}

	// ── 3. Valkey store adapters (wiring.go) ──────────────────────────────────
	sessionStore, handoffStore, rateLimiter := buildValkeyStores(rdb)

	// ── 4. Outbound HTTP adapters (wiring.go) ─────────────────────────────────
	rp := buildRealmProvisioner(cfg)
	om := buildOrgMembership(cfg)
	captchaVerifier := buildTurnstile(cfg)
	passwordHasher := buildPasswordHasher(cfg)
	catalogClient := buildCatalog(cfg)

	// ── 5. Services ───────────────────────────────────────────────────────────
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
	statusSvc := service.NewStatusService(handoffStore, rateLimiter, orchestrator, clk, cfg.ReservedSlugs)

	// ── 6. HTTP handlers and router ───────────────────────────────────────────
	handlers := httpadapter.NewHandlers(
		sessionSvc, signupSvc, statusSvc,
		catalogClient,
		cfg.CaptchaSiteKey,
		cfg.PasswordPolicy(),
		cfg.SlugRules(),
		cfg.RequestDeadline(),
		cfg.CurrentTermsVersion,
	)
	router := httpadapter.NewRouter(httpadapter.RouterConfig{
		Handlers:       handlers,
		SessionService: sessionSvc,
		Valkey:         handoffStore,
		PublicHost:     cfg.PublicHost,
		DocsEnabled:    cfg.AppEnv != "prod",
	})

	// ── 7. HTTP server ────────────────────────────────────────────────────────
	srv := &http.Server{
		Addr:         ":" + cfg.AppPort,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: time.Duration(cfg.RequestDeadlineMs+500) * time.Millisecond,
		IdleTimeout:  60 * time.Second,
	}

	// ── 8. Graceful shutdown ──────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		log.Printf("bff-signup listening on :%s (env=%s)", cfg.AppPort, cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("server: %w", err)
	case sig := <-quit:
		log.Printf("shutting down (signal=%s)", sig)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown: %v", err)
	}
	if err := rdb.Close(); err != nil {
		log.Printf("valkey close: %v", err)
	}
	log.Println("shutdown complete")
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
