package turnstile

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/metrics"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

const maxUpstreamBodyBytes = 64 * 1024

const siteverifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// Client implements port.CaptchaVerifier using Cloudflare Turnstile (BFF-3, LLD §5.6).
type Client struct {
	secret           string
	expectedHostname string
	httpClient       *http.Client
}

var _ port.CaptchaVerifier = (*Client)(nil)

func New(secret, expectedHostname string, timeout time.Duration) *Client {
	return &Client{
		secret:           secret,
		expectedHostname: expectedHostname,
		httpClient:       &http.Client{Timeout: timeout},
	}
}

type siteverifyResponse struct {
	Success    bool     `json:"success"`
	Hostname   string   `json:"hostname"`
	ErrorCodes []string `json:"error-codes"`
}

func (c *Client) Verify(ctx context.Context, token, remoteIP string) error {
	form := url.Values{
		"secret":   {c.secret},
		"response": {token},
		"remoteip": {remoteIP},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, siteverifyURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return domain.NewError(domain.ErrDependencyUnavailable, "failed to build Turnstile request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	metrics.ObserveDependencyCall("turnstile", "siteverify", time.Since(start), resp, err)
	if err != nil {
		metrics.IncCaptchaChallenged("error") // LLD §11.2
		return domain.NewError(domain.ErrDependencyUnavailable, fmt.Sprintf("Turnstile unreachable: %v", err))
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		metrics.IncCaptchaChallenged("error")
		return domain.NewError(domain.ErrDependencyUnavailable, fmt.Sprintf("Turnstile HTTP %d", resp.StatusCode))
	}

	var result siteverifyResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxUpstreamBodyBytes)).Decode(&result); err != nil {
		metrics.IncCaptchaChallenged("error")
		return domain.NewError(domain.ErrDependencyUnavailable, "failed to decode Turnstile response")
	}

	if !result.Success {
		metrics.IncCaptchaChallenged("fail") // LLD §11.2
		return domain.NewError(domain.ErrCaptchaFailed, "CAPTCHA verification failed")
	}

	// Verify hostname to prevent token reuse across origins (LLD §5.6)
	if c.expectedHostname != "" && result.Hostname != c.expectedHostname {
		metrics.IncCaptchaChallenged("fail")
		return domain.NewError(domain.ErrCaptchaFailed, "CAPTCHA hostname mismatch")
	}

	metrics.IncCaptchaChallenged("pass") // LLD §11.2
	return nil
}
