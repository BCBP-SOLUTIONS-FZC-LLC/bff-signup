//go:build e2e

// Package e2e_test holds end-to-end tests: they send real HTTP requests to a
// running bff-signup, with real Valkey and the two mock servers behind it
// (O&M on :9002, Realm Provisioner on :9003). Nothing is faked in-process.
//
// Run (see test/e2e/README.md):
//
//  1. start Valkey            make docker-up
//  2. start the mocks         ~/BCBP-IAM/bff/mocks/run_all.sh
//  3. start the service       make run         (needs CAPTCHA_SKIP=true in .env)
//  4. run the tests           make test-e2e
//
// Every test uses its own random client IP (x-envoy-external-address), e-mail
// and slug, so tests do not use up each other's rate-limit budget.
package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	trialPath = "/api/v1/signup/trial"
	cookieSID = "sbff_sid"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

var (
	baseURL = env("E2E_BASE_URL", "http://127.0.0.1:8080")
	omURL   = env("E2E_OM_URL", "http://127.0.0.1:9002")
	rpURL   = env("E2E_RP_URL", "http://127.0.0.1:9003")

	// The service checks the Origin header on every POST against https://<PUBLIC_HOST>.
	origin = env("E2E_ORIGIN", "https://localhost:8080")

	httpc = &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
)

// TestMain stops early, with a clear message, when the service or a mock is not running.
func TestMain(m *testing.M) {
	for _, u := range []string{baseURL + "/healthz", omURL + "/__mock/health", rpURL + "/__mock/health"} {
		r, err := rawDo(context.Background(), http.MethodGet, u, "", nil)
		if err != nil || r.status != http.StatusOK {
			fmt.Fprintf(os.Stderr, "E2E prerequisite not reachable: %s\nStart Valkey, the mocks and the service first (see test/e2e/README.md).\n", u)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

// reply is one HTTP response, fully read.
type reply struct {
	status  int
	body    []byte
	cookies []*http.Cookie
	header  http.Header
	took    time.Duration
}

func (r reply) text() string { return string(r.body) }

func (r reply) cookie(name string) *http.Cookie {
	for _, c := range r.cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func rawDo(ctx context.Context, method, url, contentType string, body []byte) (reply, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return reply{}, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return send(req)
}

func send(req *http.Request) (reply, error) {
	start := time.Now()
	resp, err := httpc.Do(req)
	if err != nil {
		return reply{}, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return reply{}, err
	}
	return reply{status: resp.StatusCode, body: b, cookies: resp.Cookies(), header: resp.Header, took: time.Since(start)}, nil
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func randomTag() string { return fmt.Sprintf("%08x", rand.Uint32()) }

func randomIP() string { return fmt.Sprintf("198.18.%d.%d", rand.IntN(256), rand.IntN(256)) }

// session is one browser session created with SB-1.
type session struct {
	ip           string            // sent as x-envoy-external-address ("" = header not sent)
	extra        map[string]string // extra headers sent on every request
	sid          string
	csrf         string
	signupID     string
	termsVersion string
}

// newSession calls SB-1. ip "" means the Envoy header is not sent at all.
func newSession(t *testing.T, ip string, extra map[string]string) *session {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+"/api/v1/signup/session", nil)
	require.NoError(t, err)
	if ip != "" {
		req.Header.Set("x-envoy-external-address", ip)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	r, err := send(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, r.status, r.text())

	var body struct {
		SignupID            string `json:"signup_id"`
		CSRFToken           string `json:"csrf_token"`
		CurrentTermsVersion string `json:"current_terms_version"`
	}
	require.NoError(t, json.Unmarshal(r.body, &body))
	c := r.cookie(cookieSID)
	require.NotNil(t, c, "SB-1 must set the %s cookie", cookieSID)

	return &session{
		ip: ip, extra: extra, sid: c.Value, csrf: body.CSRFToken,
		signupID: body.SignupID, termsVersion: body.CurrentTermsVersion,
	}
}

// do sends a request as this session (cookie + CSRF header on POST).
// It reports failures with t.Errorf, so it is safe to call from goroutines.
func (s *session) do(t *testing.T, method, path, contentType string, body []byte) reply {
	t.Helper()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, baseURL+path, rd)
	if err != nil {
		t.Errorf("build request: %v", err)
		return reply{}
	}
	if s.ip != "" {
		req.Header.Set("x-envoy-external-address", s.ip)
	}
	for k, v := range s.extra {
		req.Header.Set(k, v)
	}
	req.Header.Set("Cookie", cookieSID+"="+s.sid)
	if method == http.MethodPost {
		req.Header.Set("X-CSRF-Token", s.csrf)
		req.Header.Set("Origin", origin)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	r, err := send(req)
	if err != nil {
		t.Errorf("%s %s: %v", method, path, err)
	}
	return r
}

// trialPayload is a valid SB-2 body with a unique company, slug and e-mail.
func (s *session) trialPayload(overrides map[string]any) map[string]any {
	tag := randomTag()
	p := map[string]any{
		"company_name":  "E2E Company " + tag,
		"slug":          "e2e-" + tag,
		"admin_name":    "E2E Admin",
		"admin_email":   "e2e+" + tag + "@example.test",
		"password":      "E2eSecurePass#" + tag,
		"plan":          "pro",
		"accept_terms":  true,
		"terms_version": s.termsVersion,
		"captcha_token": "e2e",
	}
	for k, v := range overrides {
		p[k] = v
	}
	return p
}

func (s *session) postTrial(t *testing.T, payload map[string]any) reply {
	t.Helper()
	return s.do(t, http.MethodPost, trialPath, "application/json", mustJSON(t, payload))
}

func (s *session) getStatus(t *testing.T) reply {
	t.Helper()
	return s.do(t, http.MethodGet, "/api/v1/signup/"+s.signupID+"/status", "", nil)
}

// pin makes the mock answer the next ttl calls to endpoint with scenario, and
// clears all pins on that mock when the test ends.
func pin(t *testing.T, mockURL, endpoint, scenario string, ttl int) {
	t.Helper()
	body := mustJSON(t, map[string]any{"endpoint": endpoint, "scenario": scenario, "ttl": ttl})
	r, err := rawDo(t.Context(), http.MethodPost, mockURL+"/__mock/pin", "application/json", body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, r.status, r.text())
	t.Cleanup(func() { _, _ = rawDo(context.Background(), http.MethodDelete, mockURL+"/__mock/pins", "", nil) })
}

func clearPins(t *testing.T, mockURL string) {
	t.Helper()
	_, err := rawDo(t.Context(), http.MethodDelete, mockURL+"/__mock/pins", "", nil)
	require.NoError(t, err)
}

// pinTTL returns how many calls a pin still has left (0 when it was used up).
func pinTTL(t *testing.T, mockURL, endpoint string) int {
	t.Helper()
	r, err := rawDo(t.Context(), http.MethodGet, mockURL+"/__mock/pins", "", nil)
	require.NoError(t, err)
	var pins map[string]*struct {
		TTL int `json:"ttl"`
	}
	require.NoError(t, json.Unmarshal(r.body, &pins))
	if p := pins[endpoint]; p != nil {
		return p.TTL
	}
	return 0
}
