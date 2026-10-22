package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/inbound/http/middleware"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/service"
)

// Handlers holds all handler dependencies.
type Handlers struct {
	session   *service.SessionService
	signup    *service.SignupService
	status    *service.StatusService
	catalog   port.Catalog
	siteKey   string
	policy    domain.PasswordPolicy
	slugRules domain.SlugRules
}

func NewHandlers(
	session *service.SessionService,
	signup *service.SignupService,
	status *service.StatusService,
	catalog port.Catalog,
	siteKey string,
	policy domain.PasswordPolicy,
	slugRules domain.SlugRules,
) *Handlers {
	return &Handlers{session, signup, status, catalog, siteKey, policy, slugRules}
}

// ── SB-1: GET /api/v1/signup/session ─────────────────────────────────────────

// Session starts a new signup session and returns the CSRF token, CAPTCHA site key, plan list,
// password policy, and slug rules needed to render the signup form.
//
// @Summary      Start signup session (SB-1)
// @Description  Creates an `sbff_sid` session cookie (30-min sliding TTL) and returns a CSRF token.
// @Description  Rate-limited to 20 session starts per IP per hour.
// @Tags         session
// @Produce      json
// @Success      200  {object}  SessionResponse
// @Failure      429  {object}  ErrorResponse  "Too many session starts from this IP"
// @Failure      503  {object}  ErrorResponse  "Valkey unavailable (fail-closed)"
// @Router       /session [get]
func (h *Handlers) Session(c *gin.Context) {
	clientIP := middleware.ClientIPFromContext(c)

	result, err := h.session.Create(c.Request.Context(), clientIP)
	if err != nil {
		handleError(c, err)
		return
	}

	plans, err := h.catalog.Plans(c.Request.Context())
	if err != nil {
		handleError(c, domain.NewError(domain.ErrDependencyUnavailable, "catalog unavailable"))
		return
	}

	middleware.SetSessionCookie(c, result.SID, 1800)

	c.JSON(http.StatusOK, SessionResponse{
		SignupID:  result.SignupID,
		CSRFToken: result.CSRFToken,
		Captcha: CaptchaInfo{
			Provider: "turnstile",
			SiteKey:  h.siteKey,
		},
		Plans:          plans,
		PasswordPolicy: h.policy,
		SlugRules:      h.slugRules,
	})
}

// ── SB-2: POST /api/v1/signup/trial ──────────────────────────────────────────

// SubmitTrial submits the trial signup form and runs the full provisioning handoff:
// CAPTCHA verification → rate limiting → validation → eligibility check → RP-1 (Keycloak user) → I-1 (tenant).
//
// @Summary      Submit trial signup form (SB-2)
// @Description  Runs the provisioning handoff. Returns 201 when fully provisioned, 202 when I-1 is still
// @Description  in progress (poll SB-3), or 409 when the slug is already taken (submit SB-4 with a new slug).
// @Description  Requires the session cookie and X-CSRF-Token header from SB-1.
// @Description  Rate-limited: 3 signups per IP per hour; 5 attempts per email per 24 hours.
// @Tags         signup
// @Accept       json
// @Produce      json
// @Param        X-CSRF-Token  header    string              true  "CSRF token from SB-1"
// @Param        body          body      TrialSignupRequest  true  "Signup form payload"
// @Success      201  {object}  TrialSignupResponse  "Signup fully provisioned"
// @Success      202  {object}  TrialSignupResponse  "Keycloak user created; tenant still provisioning — poll SB-3"
// @Failure      400  {object}  ErrorResponse        "Validation failed"
// @Failure      401  {object}  ErrorResponse        "Session expired or missing"
// @Failure      403  {object}  ErrorResponse        "CSRF token invalid or CAPTCHA failed"
// @Failure      409  {object}  SlugTakenResponse    "Slug already taken — submit SB-4 with a new slug"
// @Failure      429  {object}  ErrorResponse        "Rate limit exceeded"
// @Failure      503  {object}  ErrorResponse        "Dependency unavailable (Valkey, Turnstile, RP-1)"
// @Security     SessionCookie
// @Security     CSRFToken
// @Router       /trial [post]
func (h *Handlers) SubmitTrial(c *gin.Context) {
	// Session + CSRF validation is done in the middleware stack; the session
	// and CSRF token are pre-validated and stored in context.
	sess, ok := sessionFromContext(c)
	if !ok {
		return // already handled by middleware
	}

	var req TrialSignupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		handleError(c, domain.NewError(domain.ErrValidationFailed, err.Error()))
		return
	}

	locale := acceptLocale(c)

	result, err := h.signup.Submit(c.Request.Context(), service.SignupInput{
		CompanyName:   req.CompanyName,
		Slug:          req.Slug,
		AdminName:     req.AdminName,
		AdminEmail:    req.AdminEmail,
		Password:      req.Password,
		Plan:          req.Plan,
		AcceptTerms:   req.AcceptTerms,
		TermsVersion:  req.TermsVersion,
		CaptchaToken:  req.CaptchaToken,
		DefaultLocale: locale,
		ClientIP:      middleware.ClientIPFromContext(c),
		Session:       sess,
	})
	if err != nil {
		if de, ok := err.(*domain.DomainError); ok && de.Cause == domain.ErrSlugTaken {
			// 409 slug_taken with suggestions
			var resp SlugTakenResponse
			resp.Error = "slug_taken"
			resp.Status = http.StatusConflict
			resp.RequestID = c.GetString("X-Request-Id")
			resp.Details.SignupID = sess.SignupID
			if result != nil {
				resp.Details.Suggestions = result.SlugSuggestions
			}
			c.JSON(http.StatusConflict, resp)
			return
		}
		handleError(c, err)
		return
	}

	switch result.State { //nolint:exhaustive
	case domain.StateCompleted:
		c.JSON(http.StatusCreated, TrialSignupResponse{
			SignupID:              result.SignupID,
			Status:                "completed",
			EmailVerificationSent: result.EmailVerificationSent,
			Next:                  "check_email",
		})
	case domain.StateTenantPending:
		c.JSON(http.StatusAccepted, TrialSignupResponse{
			SignupID:    result.SignupID,
			Status:      "provisioning",
			PollAfterMs: 2000,
		})
	default:
		c.JSON(http.StatusAccepted, TrialSignupResponse{
			SignupID:    result.SignupID,
			Status:      "provisioning",
			PollAfterMs: 2000,
		})
	}
}

// ── SB-3: GET /api/v1/signup/:signup_id/status ───────────────────────────────

// GetStatus polls the provisioning status after a 202 from SB-2.
// If the handoff is in tenant_pending state, one I-1 resume attempt is made before responding.
// Rate-limited to 60 polls per session per minute.
//
// @Summary      Poll provisioning status (SB-3)
// @Description  Returns the current handoff state. Poll every `poll_after_ms` milliseconds after a 202 from SB-2.
// @Description  When status is "action_required" with action "choose_slug", submit SB-4 with a new slug.
// @Description  After 15 minutes in "provisioning", status becomes "failed".
// @Tags         signup
// @Produce      json
// @Param        signup_id  path      string  true  "Signup ID returned by SB-1"
// @Success      200  {object}  StatusResponse
// @Failure      401  {object}  ErrorResponse  "Session expired or missing"
// @Failure      404  {object}  ErrorResponse  "Signup not found or does not belong to this session"
// @Failure      429  {object}  ErrorResponse  "Too many status polls"
// @Security     SessionCookie
// @Router       /{signup_id}/status [get]
func (h *Handlers) GetStatus(c *gin.Context) {
	sess, ok := sessionFromContext(c)
	if !ok {
		return
	}

	pathID := c.Param("signup_id")
	sid := middleware.SIDFromContext(c)

	result, err := h.status.GetStatus(c.Request.Context(), sess.SignupID, pathID, sid)
	if err != nil {
		handleError(c, err)
		return
	}

	resp := StatusResponse{
		SignupID:    result.SignupID,
		Status:      stateToStatusString(result.State),
		Action:      result.Action,
		PollAfterMs: result.PollAfterMs,
		Message:     result.Message,
	}
	c.JSON(http.StatusOK, resp)
}

// ── SB-4: POST /api/v1/signup/:signup_id/slug ────────────────────────────────

// ChangeSlug picks a new workspace slug after a 409 slug_taken from SB-2 or SB-3.
// The Keycloak user is NOT recreated — only I-1 (tenant creation) is retried with the new slug.
// Maximum 5 slug attempts per signup.
//
// @Summary      Change slug after conflict (SB-4)
// @Description  Retries I-1 tenant creation with a new slug. The Keycloak user from RP-1 is reused.
// @Description  Requires the handoff to be in slug_conflict state (signalled by SB-2 or SB-3 returning 409 slug_taken).
// @Tags         signup
// @Accept       json
// @Produce      json
// @Param        X-CSRF-Token  header    string            true  "CSRF token from SB-1"
// @Param        signup_id     path      string            true  "Signup ID from SB-1"
// @Param        body          body      SlugChangeRequest true  "New slug"
// @Success      201  {object}  TrialSignupResponse  "Signup fully provisioned with new slug"
// @Success      202  {object}  TrialSignupResponse  "I-1 still in progress — poll SB-3"
// @Failure      400  {object}  ErrorResponse        "Slug validation failed"
// @Failure      401  {object}  ErrorResponse        "Session expired or missing"
// @Failure      403  {object}  ErrorResponse        "CSRF token invalid"
// @Failure      404  {object}  ErrorResponse        "Signup not found or does not belong to this session"
// @Failure      409  {object}  SlugTakenResponse    "New slug also taken — try again"
// @Security     SessionCookie
// @Security     CSRFToken
// @Router       /{signup_id}/slug [post]
func (h *Handlers) ChangeSlug(c *gin.Context) {
	sess, ok := sessionFromContext(c)
	if !ok {
		return
	}

	pathID := c.Param("signup_id")

	var req SlugChangeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		handleError(c, domain.NewError(domain.ErrValidationFailed, err.Error()))
		return
	}

	result, err := h.status.ChangeSlug(c.Request.Context(), sess.SignupID, pathID, req.Slug)
	if err != nil {
		if de, ok := err.(*domain.DomainError); ok && de.Cause == domain.ErrSlugTaken {
			var resp SlugTakenResponse
			resp.Error = "slug_taken"
			resp.Status = http.StatusConflict
			resp.RequestID = c.GetString("X-Request-Id")
			resp.Details.SignupID = pathID
			c.JSON(http.StatusConflict, resp)
			return
		}
		handleError(c, err)
		return
	}

	switch result.State { //nolint:exhaustive
	case domain.StateCompleted:
		c.JSON(http.StatusCreated, TrialSignupResponse{
			SignupID:              result.SignupID,
			Status:                "completed",
			EmailVerificationSent: true,
			Next:                  "check_email",
		})
	default:
		c.JSON(http.StatusAccepted, TrialSignupResponse{
			SignupID:    result.SignupID,
			Status:      "provisioning",
			PollAfterMs: 2000,
		})
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func sessionFromContext(c *gin.Context) (*port.Session, bool) {
	raw, exists := c.Get("session")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:  "session_expired",
			Status: http.StatusUnauthorized,
		})
		c.Abort()
		return nil, false
	}
	sess, ok := raw.(*port.Session)
	if !ok {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:  "session_expired",
			Status: http.StatusUnauthorized,
		})
		c.Abort()
		return nil, false
	}
	return sess, true
}

func stateToStatusString(state domain.HandoffState) string {
	switch state { //nolint:exhaustive
	case domain.StateCompleted:
		return "completed"
	case domain.StateTenantPending:
		return "provisioning"
	case domain.StateSlugConflict:
		return "action_required"
	case domain.StateFailed:
		return "failed"
	default:
		return "provisioning"
	}
}

// acceptLocale derives default_locale from Accept-Language header (allow-listed).
func acceptLocale(c *gin.Context) string {
	lang := c.GetHeader("Accept-Language")
	allowed := map[string]bool{
		"en-US": true, "en-GB": true, "ar-AE": true,
		"fr-FR": true, "de-DE": true, "es-ES": true,
	}
	if lang != "" && allowed[lang] {
		return lang
	}
	return "en-US"
}
