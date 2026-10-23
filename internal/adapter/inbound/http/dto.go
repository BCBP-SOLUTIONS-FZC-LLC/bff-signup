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

// ── Slug taken (409) response ─────────────────────────────────────────────────

type SlugTakenResponse struct {
	Error     string `json:"error"`
	Status    int    `json:"status"`
	Message   string `json:"message,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Details   struct {
		SignupID    string   `json:"signup_id"`
		Suggestions []string `json:"suggestions"`
	} `json:"details"`
}
