package http

import (
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/problem"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
)

// ── SB-1 response ─────────────────────────────────────────────────────────────

type SessionResponse struct {
	SignupID            string                `json:"signup_id"`
	CSRFToken           string                `json:"csrf_token"`
	Captcha             CaptchaInfo           `json:"captcha"`
	Plans               []domain.PlanInfo     `json:"plans"`
	PasswordPolicy      domain.PasswordPolicy `json:"password_policy"`
	SlugRules           domain.SlugRules      `json:"slug_rules"`
	CurrentTermsVersion string                `json:"current_terms_version"` // browser must echo this back in SB-2
}

type CaptchaInfo struct {
	Provider string `json:"provider"`
	SiteKey  string `json:"site_key"`
}

// ── SB-2 request / response ───────────────────────────────────────────────────

type TrialSignupRequest struct {
	CompanyName  string      `json:"company_name"  binding:"required"`
	Slug         string      `json:"slug"`
	AdminName    string      `json:"admin_name"    binding:"required"`
	AdminEmail   string      `json:"admin_email"   binding:"required"`
	Password     string      `json:"password"      binding:"required"`
	Plan         domain.Plan `json:"plan"          binding:"required"`
	AcceptTerms  bool        `json:"accept_terms"`
	TermsVersion string      `json:"terms_version" binding:"required"`
	CaptchaToken string      `json:"captcha_token" binding:"required"`
}

type TrialSignupResponse struct {
	SignupID              string `json:"signup_id"`
	Status                string `json:"status"`
	EmailVerificationSent bool   `json:"email_verification_sent,omitempty"`
	Next                  string `json:"next,omitempty"`
	PollAfterMs           int    `json:"poll_after_ms,omitempty"`
}

// ── SB-3 response ─────────────────────────────────────────────────────────────

type StatusResponse struct {
	SignupID    string `json:"signup_id"`
	Status      string `json:"status"`
	Action      string `json:"action,omitempty"`
	PollAfterMs int    `json:"poll_after_ms,omitempty"`
	Message     string `json:"message,omitempty"`
}

// ── SB-4 request ──────────────────────────────────────────────────────────────

type SlugChangeRequest struct {
	Slug string `json:"slug" binding:"required"`
}

// ── SB-5 request / response (LLD §19) ─────────────────────────────────────────

type PaidSignupRequest struct {
	CompanyName  string      `json:"company_name"  binding:"required"`
	Slug         string      `json:"slug"`
	AdminName    string      `json:"admin_name"    binding:"required"`
	AdminEmail   string      `json:"admin_email"   binding:"required"`
	Password     string      `json:"password"      binding:"required"`
	Plan         domain.Plan `json:"plan"          binding:"required"`
	Seats        int         `json:"seats"         binding:"required,min=1,max=1000"`
	PaymentToken string      `json:"payment_token" binding:"required"` // Stripe token from browser
	AcceptTerms  bool        `json:"accept_terms"`
	TermsVersion string      `json:"terms_version" binding:"required"`
	CaptchaToken string      `json:"captcha_token" binding:"required"`
}

type PaidSignupResponse struct {
	SignupID              string         `json:"signup_id"`
	Status                string         `json:"status"`
	EmailVerificationSent bool           `json:"email_verification_sent,omitempty"`
	Next                  string         `json:"next,omitempty"`
	PollAfterMs           int            `json:"poll_after_ms,omitempty"`
	Action                *PaymentAction `json:"action,omitempty"`     // status=payment_action_required only
	ExpiresAt             string         `json:"expires_at,omitempty"` // status=payment_action_required only
}

// PaymentAction tells the browser to complete 3-D Secure with the payment
// provider's JS, then re-POST SB-5 with the same body and a fresh CAPTCHA token
// (BL-OQ-8). client_secret is for the provider's JS only.
type PaymentAction struct {
	Type         string `json:"type"` // "3ds"
	ClientSecret string `json:"client_secret"`
}

// ProblemResponse is the RFC 9457 Problem Details type returned for every
// non-2xx response (application/problem+json). It is platform-gincommon's own
// type; all @Failure Swagger annotations reference this alias, as in the IAM
// services. Error-specific members (signup_id, suggestions, message,
// retry_after_seconds, retry_after_ms, decline_reason, reason) are RFC 9457
// §3.2 extension members at the top level, added by gincommon.AbortWithProblem.
type ProblemResponse = problem.Problem
