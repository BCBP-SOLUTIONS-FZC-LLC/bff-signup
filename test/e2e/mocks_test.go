//go:build e2e

package e2e_test

// In-process versions of the two downstream mocks (Org & Membership and Realm
// Provisioner). They behave like the Python mocks in ~/BCBP-IAM/bff/mocks so
// the E2E suite can run in CI with nothing installed outside the repo:
//
//   - scenario selection: a pin set with POST /__mock/pin wins over the
//     X-Mock-Scenario header, which wins over "default"; a pin is used up
//     after ttl calls
//   - every call to the recorded endpoints is kept (headers lower-cased, JSON
//     body) and served by GET /__mock/requests, cleared by DELETE
//   - errors are RFC 9457 problem+json with an errorCode member

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

const mockSlowDelay = 3 * time.Second // longer than every client timeout in the service

// mockLatency is added to every API answer (not to /__mock/ control calls). A real
// downstream, and the Flask mocks, take a few milliseconds; without it a mock
// answers before a concurrent request has even arrived, and tests that check the
// per-signup lock (TestSB3_12) no longer overlap their requests.
const mockLatency = 25 * time.Millisecond

type pinState struct {
	Scenario string `json:"scenario"`
	TTL      int    `json:"ttl"`
}

type recordedCall struct {
	Label    string            `json:"label"`
	Scenario string            `json:"scenario"`
	Headers  map[string]string `json:"headers"`
	Body     any               `json:"body"`
}

type mockServer struct {
	mu        sync.Mutex
	endpoints []string
	pins      map[string]*pinState
	calls     []recordedCall
	catalogue map[string]any
	mux       *http.ServeMux
	srv       *httptest.Server
}

func newMockServer(catalogue map[string]any, endpoints ...string) *mockServer {
	m := &mockServer{
		endpoints: endpoints,
		pins:      map[string]*pinState{},
		catalogue: catalogue,
		mux:       http.NewServeMux(),
	}
	for _, ep := range endpoints {
		m.pins[ep] = nil
	}
	m.mux.HandleFunc("POST /__mock/pin", m.handlePin)
	m.mux.HandleFunc("GET /__mock/requests", m.handleRequests)
	m.mux.HandleFunc("DELETE /__mock/requests", m.handleClearRequests)
	m.mux.HandleFunc("GET /__mock/pins", m.handlePins)
	m.mux.HandleFunc("DELETE /__mock/pins", m.handleClearPins)
	m.mux.HandleFunc("GET /__mock/scenarios", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, m.catalogue)
	})
	m.mux.HandleFunc("GET /__mock/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	return m
}

func (m *mockServer) start() {
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/__mock/") {
			select {
			case <-time.After(mockLatency):
			case <-r.Context().Done():
				return
			}
		}
		m.mux.ServeHTTP(w, r)
	}))
}

func (m *mockServer) url() string { return m.srv.URL }
func (m *mockServer) close()      { m.srv.Close() }

// scenario picks pin > X-Mock-Scenario header > "default" and uses up one pin call.
func (m *mockServer) scenario(r *http.Request, endpoint string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p := m.pins[endpoint]; p != nil {
		sc := p.Scenario
		if p.TTL-1 <= 0 {
			m.pins[endpoint] = nil
		} else {
			m.pins[endpoint] = &pinState{Scenario: sc, TTL: p.TTL - 1}
		}
		return sc
	}
	if h := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Mock-Scenario"))); h != "" {
		return h
	}
	return "default"
}

// record stores the call (keeping the last 500) and returns the parsed JSON body.
func (m *mockServer) record(r *http.Request, label, scenario string) map[string]any {
	raw, _ := io.ReadAll(r.Body)
	headers := map[string]string{"host": r.Host}
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[strings.ToLower(k)] = v[0]
		}
	}
	var body any
	var obj map[string]any
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "json") && json.Unmarshal(raw, &body) == nil {
		obj, _ = body.(map[string]any)
	} else {
		body = nil
	}
	m.mu.Lock()
	m.calls = append(m.calls, recordedCall{Label: label, Scenario: scenario, Headers: headers, Body: body})
	if len(m.calls) > 500 {
		m.calls = m.calls[len(m.calls)-500:]
	}
	m.mu.Unlock()
	if obj == nil {
		obj = map[string]any{}
	}
	return obj
}

func (m *mockServer) handlePin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Endpoint string `json:"endpoint"`
		Scenario string `json:"scenario"`
		TTL      int    `json:"ttl"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	ep := strings.ToLower(strings.TrimSpace(req.Endpoint))
	sc := strings.ToLower(strings.TrimSpace(req.Scenario))
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.pins[ep]; !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown endpoint '" + ep + "'"})
		return
	}
	if sc == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "scenario is required"})
		return
	}
	if req.TTL < 1 {
		req.TTL = 1
	}
	m.pins[ep] = &pinState{Scenario: sc, TTL: req.TTL}
	writeJSON(w, http.StatusOK, map[string]any{"pinned": map[string]any{ep: m.pins[ep]}, "all_pins": m.pins})
}

func (m *mockServer) handleRequests(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.calls
	if out == nil {
		out = []recordedCall{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (m *mockServer) handleClearRequests(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	m.calls = nil
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, []any{})
}

func (m *mockServer) handlePins(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	writeJSON(w, http.StatusOK, m.pins)
}

func (m *mockServer) handleClearPins(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.pins {
		m.pins[k] = nil
	}
	writeJSON(w, http.StatusOK, m.pins)
}

// ── response helpers ────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeProblem(w http.ResponseWriter, status int, code, detail string, extra map[string]any) {
	words := strings.Fields(strings.ReplaceAll(code, "_", " "))
	for i, wd := range words {
		words[i] = strings.ToUpper(wd[:1]) + strings.ToLower(wd[1:])
	}
	body := map[string]any{
		"type":      "https://errors.bcbpsolutions.ae/" + code,
		"title":     strings.Join(words, " "),
		"status":    status,
		"detail":    detail,
		"errorCode": code,
	}
	for k, v := range extra {
		body[k] = v
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// slowWait sleeps mockSlowDelay (the "slow" scenario) and reports false if the caller gave up first.
func slowWait(r *http.Request) bool {
	select {
	case <-time.After(mockSlowDelay):
		return true
	case <-r.Context().Done():
		return false
	}
}

func strField(m map[string]any, key, def string) string {
	if v, ok := m[key].(string); ok && v != "" {
		return v
	}
	return def
}

// ── Org & Membership mock (eligibility CR-4, PUB-1 resolve, I-1 create) ─────

const zeroTenantID = "00000000-0000-7000-a000-000000000000"

func newOMMock() *mockServer {
	m := newMockServer(map[string]any{
		"eligibility": []string{"default", "trial_already_used", "unavailable", "slow", "unexpected_409"},
		"pub1":        []string{"default", "slug_taken", "unavailable", "slow"},
		"i1":          []string{"default", "idempotent_replay", "slug_already_taken", "unavailable", "slow", "unexpected_4xx"},
	}, "eligibility", "pub1", "i1")

	m.mux.HandleFunc("POST /api/v1/internal/trial-eligibility", func(w http.ResponseWriter, r *http.Request) {
		sc := m.scenario(r, "eligibility")
		m.record(r, "ELIGIBILITY", sc)
		switch sc {
		case "slow":
			if slowWait(r) {
				writeJSON(w, http.StatusOK, map[string]any{"eligible": true})
			}
		case "trial_already_used":
			writeProblem(w, http.StatusConflict, "TRIAL_ALREADY_USED", "this email has already been used for a trial", nil)
		case "unexpected_409":
			writeProblem(w, http.StatusConflict, "SOMETHING_ELSE", "unknown error code for contract test", nil)
		case "unavailable":
			writeProblem(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "eligibility backend is down", nil)
		default:
			writeJSON(w, http.StatusOK, map[string]any{"eligible": true})
		}
	})

	m.mux.HandleFunc("GET /api/v1/public/tenants/resolve", func(w http.ResponseWriter, r *http.Request) {
		sc := m.scenario(r, "pub1")
		switch sc {
		case "slow":
			if slowWait(r) {
				writeJSON(w, http.StatusOK, map[string]any{"found": false})
			}
		case "slug_taken":
			writeJSON(w, http.StatusOK, map[string]any{
				"found":     true,
				"tenant_id": "00000000-0000-7000-a000-000000000001",
				"slug":      r.URL.Query().Get("identifier"),
			})
		case "unavailable":
			writeProblem(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "PUB-1 backend is down", nil)
		default:
			writeJSON(w, http.StatusOK, map[string]any{"found": false})
		}
	})

	m.mux.HandleFunc("POST /api/v1/internal/tenants", func(w http.ResponseWriter, r *http.Request) {
		sc := m.scenario(r, "i1")
		body := m.record(r, "I-1", sc)
		tid := strField(body, "tenant_id", zeroTenantID)
		switch sc {
		case "slow":
			if slowWait(r) {
				writeJSON(w, http.StatusCreated, map[string]any{"tenant_id": body["tenant_id"]})
			}
		case "idempotent_replay":
			writeJSON(w, http.StatusOK, map[string]any{"tenant_id": tid})
		case "slug_already_taken":
			writeProblem(w, http.StatusConflict, "SLUG_ALREADY_TAKEN", "another tenant already owns this slug",
				map[string]any{"slug": strField(body, "slug", "")})
		case "unexpected_4xx":
			writeProblem(w, http.StatusBadRequest, "BAD_REQUEST", "a 4xx we did not expect", nil)
		case "unavailable":
			writeProblem(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "I-1 backend is down", nil)
		default:
			writeJSON(w, http.StatusCreated, map[string]any{"tenant_id": tid})
		}
	})
	return m
}

// ── Realm Provisioner mock (RP-1 provision-trial, RP-2 provision-direct) ────

func newRPMock() *mockServer {
	m := newMockServer(map[string]any{
		"rp1": []string{"default", "email_already_registered", "operation_in_progress", "realm_already_dedicated",
			"tenant_offboarded", "unavailable", "slow", "unexpected_4xx", "invalid_plan"},
		"rp2": []string{"default", "operation_in_progress", "billing_mismatch", "unavailable", "slow"},
	}, "rp1", "rp2")

	happy := func(w http.ResponseWriter, body map[string]any) {
		tid := strField(body, "tenant_id", zeroTenantID)
		suffix := strField(body, "tenant_id", "0")
		if len(suffix) > 6 {
			suffix = suffix[len(suffix)-6:]
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"tenant_id":               tid,
			"realm":                   "trial",
			"strategy":                "shared",
			"keycloak_user_id":        "mock-kc-" + suffix,
			"email_verification_sent": true,
		})
	}

	m.mux.HandleFunc("POST /api/v1/internal/tenants/provision-trial", func(w http.ResponseWriter, r *http.Request) {
		sc := m.scenario(r, "rp1")
		body := m.record(r, "RP-1", sc)
		switch sc {
		case "slow":
			if slowWait(r) {
				happy(w, body)
			}
		case "email_already_registered":
			writeProblem(w, http.StatusConflict, "EMAIL_ALREADY_REGISTERED", "an account with this email already exists", nil)
		case "operation_in_progress":
			writeProblem(w, http.StatusConflict, "OPERATION_IN_PROGRESS", "another provisioning request with the same idempotency key is running", nil)
		case "realm_already_dedicated":
			writeProblem(w, http.StatusConflict, "REALM_ALREADY_DEDICATED", "this tenant already has a dedicated realm", nil)
		case "tenant_offboarded":
			writeProblem(w, http.StatusConflict, "TENANT_OFFBOARDED", "this tenant has been offboarded and cannot be re-created", nil)
		case "invalid_plan":
			writeProblem(w, http.StatusBadRequest, "INVALID_PLAN", "plan must be starter | pro | enterprise", nil)
		case "unexpected_4xx":
			writeProblem(w, http.StatusBadRequest, "BAD_REQUEST", "a 4xx we did not expect", nil)
		case "unavailable":
			writeProblem(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "RP-1 backend is down", nil)
		default:
			happy(w, body)
		}
	})

	m.mux.HandleFunc("POST /api/v1/internal/tenants/provision-direct", func(w http.ResponseWriter, r *http.Request) {
		sc := m.scenario(r, "rp2")
		body := m.record(r, "RP-2", sc)
		switch sc {
		case "slow":
			if slowWait(r) {
				happy(w, body)
			}
		case "operation_in_progress":
			writeProblem(w, http.StatusConflict, "OPERATION_IN_PROGRESS", "duplicate RP-2 in flight", nil)
		case "billing_mismatch":
			writeProblem(w, http.StatusConflict, "BILLING_MISMATCH", "billing subscription does not match the submitted plan", nil)
		case "unavailable":
			writeProblem(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "RP-2 backend is down", nil)
		default:
			happy(w, body)
		}
	})
	return m
}
