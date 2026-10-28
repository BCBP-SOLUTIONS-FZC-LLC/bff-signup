// Package fixtures provides hand-written fakes for all port interfaces so
// unit tests can exercise services and handlers without a real Valkey,
// RP-1, I-1, or Turnstile endpoint. Following the same pattern as
// iam-user-profile/test/fixtures — no mocking framework, no generated code.
package fixtures

import (
	"context"
	"sync"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

// ── FakeSessionStore ──────────────────────────────────────────────────────────

type FakeSessionStore struct {
	mu       sync.Mutex
	sessions map[string]*port.Session
}

func NewFakeSessionStore() *FakeSessionStore {
	return &FakeSessionStore{sessions: make(map[string]*port.Session)}
}

func (f *FakeSessionStore) Create(_ context.Context, s *port.Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[s.SID] = s
	return nil
}

func (f *FakeSessionStore) Get(_ context.Context, sid string) (*port.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions[sid], nil
}

func (f *FakeSessionStore) Refresh(_ context.Context, sid string) error { return nil }

func (f *FakeSessionStore) Delete(_ context.Context, sid string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, sid)
	return nil
}

// ── FakeHandoffStore ──────────────────────────────────────────────────────────

type FakeHandoffStore struct {
	mu       sync.Mutex
	handoffs map[string]*domain.Handoff
	locked   map[string]string // signupID → token
}

func NewFakeHandoffStore() *FakeHandoffStore {
	return &FakeHandoffStore{
		handoffs: make(map[string]*domain.Handoff),
		locked:   make(map[string]string),
	}
}

func (f *FakeHandoffStore) Create(_ context.Context, h *domain.Handoff) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *h
	f.handoffs[h.SignupID] = &cp
	return nil
}

func (f *FakeHandoffStore) Get(_ context.Context, signupID string) (*domain.Handoff, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h := f.handoffs[signupID]
	if h == nil {
		return nil, nil
	}
	cp := *h
	return &cp, nil
}

func (f *FakeHandoffStore) Update(_ context.Context, h *domain.Handoff) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *h
	f.handoffs[h.SignupID] = &cp
	return nil
}

func (f *FakeHandoffStore) Delete(_ context.Context, signupID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.handoffs, signupID)
	return nil
}

func (f *FakeHandoffStore) AcquireLock(_ context.Context, signupID string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, held := f.locked[signupID]; held {
		return "", false, nil
	}
	token := "fake-lock-token-" + signupID
	f.locked[signupID] = token
	return token, true, nil
}

func (f *FakeHandoffStore) ReleaseLock(_ context.Context, signupID, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.locked[signupID] == token {
		delete(f.locked, signupID)
	}
	return nil
}

func (f *FakeHandoffStore) Ping(_ context.Context) error { return nil }

// ── FakeRateLimiter ───────────────────────────────────────────────────────────

// FakeRateLimiter counts increments and lets tests set limits per scope.
type FakeRateLimiter struct {
	mu         sync.Mutex
	counts     map[string]int64
	IPLimit    int64
	EmailLimit int64
	SessLimit  int64
	PollLimit  int64
}

func NewFakeRateLimiter() *FakeRateLimiter {
	return &FakeRateLimiter{
		counts:     make(map[string]int64),
		IPLimit:    3,
		EmailLimit: 5,
		SessLimit:  20,
		PollLimit:  60,
	}
}

func (f *FakeRateLimiter) IncrIP(_ context.Context, ip string) (int64, time.Time, error) {
	return f.incr("ip:" + ip)
}
func (f *FakeRateLimiter) IncrEmail(_ context.Context, hmac string) (int64, time.Time, error) {
	return f.incr("email:" + hmac)
}
func (f *FakeRateLimiter) IncrSession(_ context.Context, ip string) (int64, time.Time, error) {
	return f.incr("sess:" + ip)
}
func (f *FakeRateLimiter) IncrPoll(_ context.Context, sid string) (int64, time.Time, error) {
	return f.incr("poll:" + sid)
}
func (f *FakeRateLimiter) incr(key string) (int64, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts[key]++
	return f.counts[key], time.Now().Add(time.Hour), nil
}

// ── FakeCaptchaVerifier ───────────────────────────────────────────────────────

// FakeCaptchaVerifier passes by default; set Err to inject a failure.
type FakeCaptchaVerifier struct {
	Err error
}

func (f *FakeCaptchaVerifier) Verify(_ context.Context, _, _ string) error {
	return f.Err
}

// ── FakeRealmProvisioner ──────────────────────────────────────────────────────

// FakeRealmProvisioner returns a fixed response or a scripted error.
type FakeRealmProvisioner struct {
	Resp        *port.ProvisionTrialResponse
	Err         error
	DirectResp  *port.ProvisionDirectResponse
	DirectErr   error
	mu          sync.Mutex
	Calls       []port.ProvisionTrialRequest
	DirectCalls []port.ProvisionDirectRequest
}

func NewFakeRealmProvisioner(keycloakUserID string) *FakeRealmProvisioner {
	return &FakeRealmProvisioner{
		Resp: &port.ProvisionTrialResponse{
			TenantID:              "00000000-0000-0000-0000-000000000001",
			Realm:                 "trial",
			Strategy:              "shared",
			KeycloakUserID:        keycloakUserID,
			EmailVerificationSent: true,
		},
		DirectResp: &port.ProvisionDirectResponse{
			TenantID:      "00000000-0000-0000-0000-000000000001",
			Realm:         keycloakUserID + "-realm",
			Strategy:      "dedicated",
			KeycloakShard: "shard-0",
			OIDCClients:   []string{"bff-signup-client"},
			// KeycloakUserID set so tests can verify owner_user_id forwarded to I-1 (SB-INV-4).
			// RP-2 must add this field to its real response before Phase 1.5 activates.
			KeycloakUserID: keycloakUserID,
		},
	}
}

func (f *FakeRealmProvisioner) ProvisionTrial(_ context.Context, req port.ProvisionTrialRequest, _ string) (*port.ProvisionTrialResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, req)
	return f.Resp, f.Err
}

func (f *FakeRealmProvisioner) ProvisionDirect(_ context.Context, req port.ProvisionDirectRequest, _ string) (*port.ProvisionDirectResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DirectCalls = append(f.DirectCalls, req)
	return f.DirectResp, f.DirectErr
}

// ── FakeOrgMembership ─────────────────────────────────────────────────────────

// FakeOrgMembership controls eligibility, PUB-1 slug check, and I-1 outcomes independently.
type FakeOrgMembership struct {
	EligibleErr error // nil = eligible; set to ErrTrialNotEligible to block
	SlugFreeErr error // nil = slug free; set to ErrSlugTaken to simulate taken slug
	I1Resp      *port.ProvisionTenantResponse
	I1Err       error
	mu          sync.Mutex
	I1Calls     []port.ProvisionTenantRequest
}

func NewFakeOrgMembership(tenantID string) *FakeOrgMembership {
	return &FakeOrgMembership{
		I1Resp: &port.ProvisionTenantResponse{TenantID: tenantID},
	}
}

func (f *FakeOrgMembership) CheckEligibility(_ context.Context, _ string) error {
	return f.EligibleErr
}

func (f *FakeOrgMembership) CheckSlugFree(_ context.Context, _ string) error {
	return f.SlugFreeErr
}

func (f *FakeOrgMembership) ProvisionTenant(_ context.Context, req port.ProvisionTenantRequest) (*port.ProvisionTenantResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.I1Calls = append(f.I1Calls, req)
	if f.I1Err != nil {
		return nil, f.I1Err
	}
	// Echo the request tenant ID — matches O&M's idempotent behaviour.
	return &port.ProvisionTenantResponse{TenantID: req.TenantID}, nil
}

// ── FakeBillingClient ─────────────────────────────────────────────────────────

// FakeBillingClient returns a fixed charge response or a scripted error.
type FakeBillingClient struct {
	Resp  *port.ChargeResponse
	Err   error
	mu    sync.Mutex
	Calls []port.ChargeRequest
}

func NewFakeBillingClient(subscriptionID string) *FakeBillingClient {
	return &FakeBillingClient{
		Resp: &port.ChargeResponse{BillingSubscriptionID: subscriptionID},
	}
}

func (f *FakeBillingClient) AuthorizeAndCharge(_ context.Context, req port.ChargeRequest) (*port.ChargeResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, req)
	return f.Resp, f.Err
}

// ── FakeCatalog ───────────────────────────────────────────────────────────────

// FakeCatalog returns a fixed plan list or a scripted error.
type FakeCatalog struct {
	PlanList []domain.PlanInfo
	Err      error
}

func NewFakeCatalog() *FakeCatalog {
	return &FakeCatalog{
		PlanList: []domain.PlanInfo{
			{Code: domain.PlanStarter, Name: "Starter", TrialDays: 14},
			{Code: domain.PlanPro, Name: "Pro", TrialDays: 14},
			{Code: domain.PlanEnterprise, Name: "Enterprise", TrialDays: 14},
		},
	}
}

func (f *FakeCatalog) Plans(_ context.Context) ([]domain.PlanInfo, error) {
	return f.PlanList, f.Err
}

// ── FakeClock ─────────────────────────────────────────────────────────────────

// FakeClock returns a fixed time. Advance it in tests that need time progression.
type FakeClock struct {
	T time.Time
}

func NewFakeClock() *FakeClock {
	return &FakeClock{T: time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)}
}

func (f *FakeClock) Now() time.Time { return f.T }

// ── FakeIDGenerator ───────────────────────────────────────────────────────────

// FakeIDGenerator returns deterministic IDs for assertions in tests.
type FakeIDGenerator struct {
	ULID   string
	UUIDv7 string
}

func NewFakeIDGenerator() *FakeIDGenerator {
	return &FakeIDGenerator{
		ULID:   "01JAB0000000000000000000",
		UUIDv7: "0192f0c4-0000-7000-0000-000000000001",
	}
}

func (f *FakeIDGenerator) NewULID() string   { return f.ULID }
func (f *FakeIDGenerator) NewUUIDv7() string { return f.UUIDv7 }

// ── FakePasswordHasher ────────────────────────────────────────────────────────

// FakePasswordHasher returns a deterministic seed without running PBKDF2
// (tests don't need CPU-intensive hashing).
type FakePasswordHasher struct {
	Err error
}

func (f *FakePasswordHasher) Hash(_ string) (*port.HashedPasswordSeed, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	return &port.HashedPasswordSeed{
		Algorithm:  "pbkdf2-sha512",
		Iterations: 210000,
		Salt:       "AAAAAAAAAAAAAAAAAAAAAA==",
		Hash:       "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA==",
	}, nil
}
