package http

import "github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"

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

// ── SB-5 request / response (Phase 1.5, LLD §19) ─────────────────────────────

type PaidSignupRequest struct {
	CompanyName  string      `json:"company_name"  binding:"required"`
	Slug         string      `json:"slug"`
	AdminName    string      `json:"admin_name"    binding:"required"`
	AdminEmail   string      `json:"admin_email"   binding:"required"`
	Password     string      `json:"password"      binding:"required"`
	Plan         domain.Plan `json:"plan"          binding:"required"`
	Seats        int         `json:"seats"         binding:"required"`
	PaymentToken string      `json:"payment_token" binding:"required"` // Stripe token from browser
	AcceptTerms  bool        `json:"accept_terms"`
	TermsVersion string      `json:"terms_version" binding:"required"`
	CaptchaToken string      `json:"captcha_token" binding:"required"`
}

type PaidSignupResponse struct {
	SignupID              string `json:"signup_id"`
	Status                string `json:"status"`
	EmailVerificationSent bool   `json:"email_verification_sent,omitempty"`
	Next                  string `json:"next,omitempty"`
	PollAfterMs           int    `json:"poll_after_ms,omitempty"`
}

// SlugTakenResponse is the RFC 9457 representation of a 409 slug_taken error.
// Fields are RFC 9457 §3.2 extension members, not a custom error DTO.
// The actual response is built and serialized by gincommon.AbortWithProblem.
// This type is kept only for Swagger annotation reference (@Failure 409 {object} SlugTakenResponse).
//
// Wire shape (application/problem+json):
//
//	{
//	  "type":       "...",
//	  "title":      "...",
//	  "status":     409,
//	  "errorCode":  "SLUG_TAKEN",
//	  "signup_id":  "01JAB...",
//	  "suggestions": ["acme-2", "acme-hq"],
//	  "message":    "That workspace address is taken..."
//	}
type SlugTakenResponse struct {
	SignupID    string   `json:"signup_id"`
	Suggestions []string `json:"suggestions"`
	Message     string   `json:"message,omitempty"`
}

// ErrorResponse is the RFC 9457 representation of every non-slug error response.
// The actual response is built and serialized by gincommon.AbortWithProblem.
// This type is kept only for Swagger annotation reference (@Failure ... {object} ErrorResponse).
//
// Wire shape (application/problem+json):
//
//	{
//	  "type":           "...",
//	  "title":          "...",
//	  "status":         409,
//	  "detail":         "...",
//	  "errorCode":      "INVALID_STATE",
//	  "retry_after_ms": 500
//	}
type ErrorResponse struct {
	Type         string `json:"type,omitempty"`
	Title        string `json:"title"`
	Status       int    `json:"status"`
	Detail       string `json:"detail,omitempty"`
	ErrorCode    string `json:"errorCode,omitempty"`
	RetryAfterMS int    `json:"retry_after_ms,omitempty"`
}
