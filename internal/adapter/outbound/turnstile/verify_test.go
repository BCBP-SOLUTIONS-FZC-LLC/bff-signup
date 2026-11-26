package turnstile

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
)

// redirect routes every HTTP request to the test server url.
type redirect struct {
	url string
}

func (r *redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	override, _ := http.NewRequestWithContext(req.Context(), req.Method, r.url, req.Body)
	override.Header = req.Header
	return http.DefaultTransport.RoundTrip(override)
}

func newTestClient(srv *httptest.Server, hostname string) *Client {
	return &Client{
		secret:           "test-secret",
		expectedHostname: hostname,
		httpClient:       &http.Client{Transport: &redirect{url: srv.URL}, Timeout: 5 * time.Second},
	}
}

func TestVerify_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		assert.Contains(t, string(body), "test-secret")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(siteverifyResponse{
			Success:  true,
			Hostname: "signup.example.com",
		})
	}))
	defer srv.Close()

	c := newTestClient(srv, "signup.example.com")
	err := c.Verify(context.Background(), "valid-token", "1.2.3.4")
	assert.NoError(t, err)
}

func TestVerify_CaptchaFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(siteverifyResponse{
			Success:    false,
			ErrorCodes: []string{"invalid-input-response"},
		})
	}))
	defer srv.Close()

	c := newTestClient(srv, "")
	err := c.Verify(context.Background(), "bad-token", "1.2.3.4")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrCaptchaFailed, de.Cause)
}

func TestVerify_HostnameMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(siteverifyResponse{
			Success:  true,
			Hostname: "evil.example.com",
		})
	}))
	defer srv.Close()

	c := newTestClient(srv, "signup.example.com")
	err := c.Verify(context.Background(), "token", "1.2.3.4")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrCaptchaFailed, de.Cause)
}

func TestVerify_Non200Response(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := newTestClient(srv, "")
	err := c.Verify(context.Background(), "token", "1.2.3.4")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

func TestVerify_DecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c := newTestClient(srv, "")
	err := c.Verify(context.Background(), "token", "1.2.3.4")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

func TestVerify_EmptyHostnameSkipsCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(siteverifyResponse{
			Success:  true,
			Hostname: "any-hostname.example.com",
		})
	}))
	defer srv.Close()

	// expectedHostname == "" means no hostname check
	c := newTestClient(srv, "")
	err := c.Verify(context.Background(), "token", "1.2.3.4")
	assert.NoError(t, err)
}

func TestVerify_RequestBuildError(t *testing.T) {
	// Context already cancelled → NewRequestWithContext will fail
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancelled

	c := &Client{
		secret:           "test-secret",
		expectedHostname: "",
		httpClient:       &http.Client{},
	}
	// Cancelled context: the Do call will fail with context error
	err := c.Verify(ctx, "token", "1.2.3.4")
	require.Error(t, err)
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

func TestNew_ReturnsNonNil(t *testing.T) {
	c := New("secret", "hostname", time.Second)
	assert.NotNil(t, c)
	assert.IsType(t, &Client{}, c)
}

func TestVerify_FormEncoding(t *testing.T) {
	var received string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received = string(body)
		assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(siteverifyResponse{Success: true})
	}))
	defer srv.Close()

	c := newTestClient(srv, "")
	_ = c.Verify(context.Background(), "my-token", "10.0.0.1")

	assert.True(t, strings.Contains(received, "response=my-token"))
	assert.True(t, strings.Contains(received, "remoteip=10.0.0.1"))
	assert.True(t, strings.Contains(received, "secret=test-secret"))
}

// Cloudflare's own fault is an outage (503), not the user's CAPTCHA failure.
func TestVerify_CloudflareInternalErrorIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(siteverifyResponse{Success: false, ErrorCodes: []string{"internal-error"}})
	}))
	defer srv.Close()

	err := newTestClient(srv, "").Verify(context.Background(), "tok", "1.2.3.4")
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrDependencyUnavailable, de.Cause)
}

// The transport error (URL, dial details) must not reach the public 503 detail.
func TestVerify_TransportErrorMessageIsFixed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	c := newTestClient(srv, "")
	srv.Close()

	err := c.Verify(context.Background(), "tok", "1.2.3.4")
	var de *domain.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, "CAPTCHA provider unreachable", de.Message)
	assert.NotContains(t, de.Message, "127.0.0.1")
}
