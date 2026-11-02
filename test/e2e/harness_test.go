//go:build e2e

package e2e_test

// The in-process stack used when the E2E suite runs without E2E_BASE_URL (CI and
// a plain `make test-e2e`): a real Valkey in a testcontainers container, the two
// downstream mocks from mocks_test.go, and the real router and services behind an
// httptest.Server. This follows the iam-org-membership E2E harness.
//
// The wiring below mirrors cmd/server/main.go and wiring.go (package main cannot
// be imported). When main.go changes how services or the router are built, change
// this file the same way. Captcha is the CAPTCHA_SKIP=true path and paid signup is
// off, so the captcha/paid-mode tests stay skipped, as in a normal local run.

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/hasher"
	bffmetrics "github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/metrics"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/orgmembership"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/realmprovisioner"
	valkeystore "github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/valkey"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/logger"
)

const (
	valkeyImage = "valkey/valkey:8-alpine" // same image as docker-compose.yml
	publicHost  = "localhost:8080"         // origin checks use https://<PUBLIC_HOST>
)

// stack is everything the tests talk to.
type stack struct {
	baseURL    string
	omURL      string
	rpURL      string
	metricsURL string
	origin     string
	closers    []func()
}

func (s *stack) onClose(f func()) { s.closers = append(s.closers, f) }

func (s *stack) close() {
	for i := len(s.closers) - 1; i >= 0; i-- {
		s.closers[i]()
	}
	s.closers = nil
}

// startStack boots Valkey, the mocks and the service. On error everything
// already started is shut down again.
func startStack(ctx context.Context) (*stack, error) {
	s := &stack{origin: "https://" + publicHost}
	ok := false
	defer func() {
		if !ok {
			s.close()
		}
	}()

	valkeyURL, err := s.startValkey(ctx)
	if err != nil {
		return nil, fmt.Errorf("start Valkey (is Docker running?): %w", err)
	}

	om, rp := newOMMock(), newRPMock()
	om.start()
	s.onClose(om.close)
	rp.start()
	s.onClose(rp.close)
	s.omURL, s.rpURL = om.url(), rp.url()

	// The same variables the service reads in production; values match .env-example
	// for local runs. Set explicitly so a stray variable in the shell cannot change a CI run.
	for k, v := range map[string]string{
		"APP_ENV":                    "dev",
		"PUBLIC_HOST":                publicHost,
		"VALKEY_URL":                 valkeyURL,
		"SBFF_HMAC_KEY":              "e2e-only-hmac-key-0123456789abcdef-0123456789",
		"SBFF_TURNSTILE_SECRET":      "e2e-only-turnstile-secret",
		"CAPTCHA_SITE_KEY":           "e2e-only-site-key",
		"CAPTCHA_SKIP":               "true",
		"REALM_PROVISIONER_BASE_URL": s.rpURL,
		"ORG_MEMBERSHIP_BASE_URL":    s.omURL,
		"CATALOG_BASE_URL":           "",
		"BILLING_BASE_URL":           "",
		"PAID_SIGNUP_ENABLED":        "false",
	} {
		if err := os.Setenv(k, v); err != nil {
			return nil, fmt.Errorf("set %s: %w", k, err)
		}
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	gin.SetMode(gin.TestMode)
	log, err := logger.NewLogger("dev")
	if err != nil {
		return nil, fmt.Errorf("logger: %w", err)
	}
	ginCfg := gincommon.Config{
		Logger:         log,
		ServiceName:    "bff-signup",
		BuildVersion:   "e2e",
		Domain:         "bff",
		Environment:    cfg.AppEnv,
		RequestTimeout: time.Duration(cfg.RequestDeadlineMs) * time.Millisecond,
	}
	shutdownTracing, err := gincommon.InitTracingWithConfig(gincommon.TracingConfig{
		ServiceName:  ginCfg.ServiceName,
		BuildVersion: ginCfg.BuildVersion,
		Domain:       ginCfg.Domain,
		Environment:  ginCfg.Environment,
		Logger:       log,
	})
	if err != nil {
		return nil, fmt.Errorf("tracing: %w", err)
	}
	s.onClose(shutdownTracing)
	_ = gincommon.ObservabilityMiddlewares(ginCfg)
	bffmetrics.Register(cfg.AppEnv)

	// Valkey stores.
	rdb := valkeystore.NewClient(cfg.ValkeyURL)
	s.onClose(func() { _ = rdb.Close() })
	sessionStore := valkeystore.NewSessionStore(rdb, time.Duration(cfg.SessionTTLSeconds)*time.Second)
	handoffStore := valkeystore.NewHandoffStore(rdb)
	rateLimiter := valkeystore.NewRateLimiter(rdb)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := valkeystore.Ping(pingCtx, rdb); err != nil {
		return nil, fmt.Errorf("valkey ping: %w", err)
	}

	// Outbound adapters.
	rpClient := realmprovisioner.New(cfg.RealmProvisionerURL, cfg.RPTimeout())
	omClient := orgmembership.New(cfg.OrgMembershipURL, cfg.I1Timeout())
	passwordHasher := hasher.New(cfg.PBKDF2Iterations)
	plans := &staticCatalog{plans: cfg.PlansFallback}

	// Services.
	clk := port.RealClock{}
	idgen := &defaultIDGen{}
	orchestrator := service.NewHandoffOrchestrator(handoffStore, rpClient, omClient, clk, cfg.EligibilityTimeout())
	sessionSvc := service.NewSessionService(sessionStore, rateLimiter, idgen, clk, cfg.SessionsPerIPPerHour)
	signupSvc := service.NewSignupService(
		handoffStore, rateLimiter, noopCaptcha{}, passwordHasher,
		orchestrator, idgen, clk,
		[]byte(cfg.HMACKey), cfg.ReservedSlugs, cfg.PasswordPolicy(),
		cfg.CurrentTermsVersion,
		cfg.SignupsPerIPPerHour, cfg.AttemptsPerEmailPerDay,
	)
	paidSignupSvc := service.NewPaidSignupService(
		handoffStore, rateLimiter, noopCaptcha{}, passwordHasher,
		port.BillingStub{}, rpClient, omClient, orchestrator, idgen, clk,
		[]byte(cfg.HMACKey), cfg.ReservedSlugs, cfg.PasswordPolicy(),
		cfg.CurrentTermsVersion,
	)
	statusSvc := service.NewStatusService(handoffStore, rateLimiter, orchestrator, clk, cfg.ReservedSlugs, cfg.PollsPerSessionPerMin)

	// Handlers and router.
	handlers := httpadapter.NewHandlers(
		sessionSvc, signupSvc, paidSignupSvc, statusSvc,
		plans,
		cfg.CaptchaSiteKey,
		cfg.PasswordPolicy(),
		cfg.SlugRules(),
		cfg.RequestDeadline(),
		cfg.CurrentTermsVersion,
		cfg.SessionTTLSeconds,
		log,
	)
	router := httpadapter.NewRouter(httpadapter.RouterConfig{
		GinConfig:         ginCfg,
		Handlers:          handlers,
		SessionService:    sessionSvc,
		Valkey:            handoffStore,
		PublicHost:        cfg.PublicHost,
		DocsEnabled:       true,
		PaidSignupEnabled: cfg.PaidSignupEnabled,
	})
	api := httptest.NewServer(router)
	s.onClose(api.Close)
	s.baseURL = api.URL

	// /metrics lives on its own port in production.
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", gincommon.MetricsHandler())
	metrics := httptest.NewServer(metricsMux)
	s.onClose(metrics.Close)
	s.metricsURL = metrics.URL

	ok = true
	return s, nil
}

// startValkey runs a throw-away Valkey container and returns its redis:// URL.
func (s *stack) startValkey(ctx context.Context) (string, error) {
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        valkeyImage,
			ExposedPorts: []string{"6379/tcp"},
			WaitingFor:   wait.ForLog("Ready to accept connections").WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	})
	if c != nil {
		s.onClose(func() { _ = c.Terminate(context.Background()) })
	}
	if err != nil {
		return "", err
	}
	endpoint, err := c.PortEndpoint(ctx, "6379/tcp", "")
	if err != nil {
		return "", err
	}
	return "redis://" + endpoint + "/0", nil
}

// ── small adapters copied from cmd/server (package main) ────────────────────

type noopCaptcha struct{}

func (noopCaptcha) Verify(_ context.Context, _, _ string) error { return nil }

type staticCatalog struct{ plans []domain.PlanInfo }

func (c *staticCatalog) Plans(_ context.Context) ([]domain.PlanInfo, error) { return c.plans, nil }

type defaultIDGen struct{}

func (g *defaultIDGen) NewULID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

func (g *defaultIDGen) NewUUIDv7() string {
	id, _ := uuid.NewV7()
	return id.String()
}
