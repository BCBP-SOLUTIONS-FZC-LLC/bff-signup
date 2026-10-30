package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/metrics"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

const (
	maxI1InlineRetries = 2
	resumeWindow       = 15 * time.Minute
	maxSlugAttempts    = 5
)

// OrchestratorInput carries all the data needed for the eligibility → RP-1 → I-1 handoff.
type OrchestratorInput struct {
	Handoff            *domain.Handoff
	AdminEmail         string
	AdminName          string
	HashedPasswordSeed string               // JSON-serialised; Go string, not zeroable — never logged, stored in Valkey, or retained after RP-1 (SB-INV-2)
	TermsAcceptance    port.TermsAcceptance // forwarded to RP-1 for Audit Log record (A-4)
}

// HandoffOrchestrator runs the provisioning sequence (LLD §8.1, §9.1).
type HandoffOrchestrator struct {
	handoffs           port.HandoffStore
	rp                 port.RealmProvisioner
	om                 port.OrgMembership
	clock              port.Clock
	eligibilityTimeout time.Duration
}

func NewHandoffOrchestrator(
	handoffs port.HandoffStore,
	rp port.RealmProvisioner,
	om port.OrgMembership,
	clock port.Clock,
	eligibilityTimeout time.Duration,
) *HandoffOrchestrator {
	return &HandoffOrchestrator{handoffs: handoffs, rp: rp, om: om, clock: clock, eligibilityTimeout: eligibilityTimeout}
}

// OrchestratorResult reports the outcome of a full or partial run.
type OrchestratorResult struct {
	State                 domain.HandoffState
	KeycloakUserID        string
	EmailVerificationSent bool
	SlugSuggestions       []string // populated when State == slug_conflict
}

// Run executes eligibility check → RP-1 → I-1, updating handoff state at each step.
// It is called from SignupService (SB-2) and must complete within the request deadline.
func (o *HandoffOrchestrator) Run(ctx context.Context, in OrchestratorInput) (*OrchestratorResult, error) {
	h := in.Handoff

	// ── Step E: trial eligibility (CR-4) ─────────────────────────────────────
	if h.State == domain.StateAllocated {
		eligCtx, eligCancel := context.WithTimeout(ctx, o.eligibilityTimeout)
		err := o.om.CheckEligibility(eligCtx, h.AdminEmailHMAC)
		eligCancel()
		if err != nil {
			// Only delete when email is definitively ineligible (LLD §9.5).
			// On ErrDependencyUnavailable keep the handoff so the user can retry.
			if isDomainCause(err, domain.ErrTrialNotEligible) {
				_ = o.handoffs.Delete(ctx, h.SignupID)
			}
			return nil, err
		}
	}

	// ── Step 1: RP-1 provision-trial ─────────────────────────────────────────
	if h.State == domain.StateAllocated {
		// PUB-1 slug pre-check — server-side before RP-1 to prevent orphaned Keycloak
		// users when the slug is already taken (A-3, LLD §7). Fail-open: 5xx/network
		// errors return nil so the signup proceeds and relies on I-1's unique constraint.
		if err := o.om.CheckSlugFree(ctx, h.Slug); err != nil {
			suggestions := domain.SuggestSlugs(h.Slug, h.CompanyName, nil)
			h.State = domain.StateSlugConflict
			h.UpdatedAt = o.clock.Now()
			_ = o.handoffs.Update(ctx, h)
			return &OrchestratorResult{
				State:           domain.StateSlugConflict,
				SlugSuggestions: suggestions,
			}, err
		}

		idempotencyKey := fmt.Sprintf("signup:%s:rp1", h.TenantID)
		rpReq := port.ProvisionTrialRequest{
			TenantID:           h.TenantID,
			TenantName:         h.CompanyName,
			TenantSlug:         h.Slug,
			AdminEmail:         in.AdminEmail,
			AdminName:          in.AdminName,
			HashedPasswordSeed: in.HashedPasswordSeed,
			Plan:               string(h.Plan),
			TermsAcceptance:    in.TermsAcceptance,
		}
		rpResp, err := o.rp.ProvisionTrial(ctx, rpReq, idempotencyKey)
		if err != nil {
			// ErrEmailUnavailable → delete handoff and surface to user
			if isDomainCause(err, domain.ErrEmailUnavailable) {
				_ = o.handoffs.Delete(ctx, h.SignupID)
			}
			return nil, err
		}

		h.KeycloakUserID = rpResp.KeycloakUserID
		h.State = domain.StateIdentityCreated
		h.UpdatedAt = o.clock.Now()
		if err := o.handoffs.Update(ctx, h); err != nil {
			return nil, domain.NewError(domain.ErrDependencyUnavailable, "handoff store unavailable")
		}

		// ── Step 2: I-1 provision-tenant (inline with retries) ─────────────────
		result, err := o.runI1(ctx, h)
		// Thread EmailVerificationSent from RP-1 into the final result so
		// SB-2 can return it in the 201 response (LLD §5.4).
		if result != nil {
			result.EmailVerificationSent = rpResp.EmailVerificationSent
		}
		return result, err
	}

	// Already past allocated (resume path) — run I-1 directly.
	result, err := o.runI1(ctx, h)
	return result, err
}

// ResumeI1 retries I-1 from an existing identity_created or tenant_pending handoff.
// Called by StatusService (SB-3) during polling.
func (o *HandoffOrchestrator) ResumeI1(ctx context.Context, h *domain.Handoff) (*OrchestratorResult, error) {
	if !h.CanResume() {
		return nil, domain.NewError(domain.ErrInvalidState, "handoff is not in tenant_pending state")
	}

	// Check 15-minute resume window
	if o.clock.Now().Sub(h.CreatedAt) > resumeWindow {
		h.State = domain.StateFailed
		h.UpdatedAt = o.clock.Now()
		_ = o.handoffs.Update(ctx, h)
		metrics.IncProvisioningFailed("trial", "org_membership", "exhausted") // LLD §11.2
		return &OrchestratorResult{State: domain.StateFailed}, nil
	}

	return o.callI1Once(ctx, h)
}

// ChangeSlug retries I-1 with a new slug (SB-4) after a slug conflict.
func (o *HandoffOrchestrator) ChangeSlug(ctx context.Context, h *domain.Handoff, newSlug string) (*OrchestratorResult, error) {
	if !h.CanChangeSlug() {
		return nil, domain.NewError(domain.ErrInvalidState, "handoff is not in slug_conflict state")
	}
	// PUB-1 pre-check path: RP-1 never ran, so no Keycloak identity exists.
	// SB-4 can only retry I-1 — it cannot re-run RP-1 (password seed is gone).
	// The frontend must resubmit SB-2 with the new slug.
	if h.KeycloakUserID == "" {
		return nil, domain.NewError(domain.ErrInvalidState, "identity not yet created; resubmit the signup form with the new slug")
	}
	if h.SlugAttempts >= maxSlugAttempts {
		return nil, domain.NewError(domain.ErrInvalidState, "maximum slug attempts reached")
	}
	h.Slug = newSlug
	h.State = domain.StateIdentityCreated
	h.SlugAttempts++
	h.UpdatedAt = o.clock.Now()
	if err := o.handoffs.Update(ctx, h); err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "handoff store unavailable")
	}
	result, err := o.callI1Once(ctx, h)
	if err == nil {
		return result, nil
	}
	if isDomainCause(err, domain.ErrSlugTaken) {
		// New slug is also taken: surface the suggestions and a 409 (A-3).
		return result, err
	}
	if isDomainCause(err, domain.ErrInternal) {
		// Contract error (LLD §9.5): fail fast, exactly as runI1 does.
		metrics.IncProvisioningFailed("trial", "org_membership", "contract_error") // LLD §11.2
		return nil, err
	}
	if !isDomainCause(err, domain.ErrDependencyUnavailable) {
		// Unknown error: pass it through unchanged, as before.
		return nil, err
	}
	// I-1 5xx or timeout (LLD §9.5): the signup must not be left stuck in
	// identity_created. Park it in tenant_pending so SB-3 polling resumes I-1,
	// and answer 202 provisioning (OBS-31).
	h.State = domain.StateTenantPending
	h.I1Attempts++
	h.LastError = err.Error()
	h.UpdatedAt = o.clock.Now()
	_ = o.handoffs.Update(ctx, h)
	return &OrchestratorResult{State: domain.StateTenantPending}, nil
}

// runI1 calls I-1 inline with up to maxI1InlineRetries retries.
func (o *HandoffOrchestrator) runI1(ctx context.Context, h *domain.Handoff) (*OrchestratorResult, error) {
	var lastErr error
	for attempt := 0; attempt <= maxI1InlineRetries; attempt++ {
		// Stop if the request context deadline is close
		if ctx.Err() != nil {
			break
		}
		result, err := o.callI1Once(ctx, h)
		if err == nil {
			return result, nil
		}
		if isDomainCause(err, domain.ErrSlugTaken) {
			// Slug conflict is terminal for inline retries — propagate both the
			// result (with suggestions) and the error so the caller can surface
			// a 409 with the suggestions list.
			return result, err
		}
		if isDomainCause(err, domain.ErrInternal) {
			// Contract error (LLD §9.5): an unexpected 4xx, an unexpected 409 code
			// or a malformed reply from I-1 will not fix itself, and retrying would
			// hide it behind a 202. Fail fast: the handoff stays identity_created,
			// the caller returns 500 internal_error, and the paged contract_error
			// metric fires. 5xx and timeouts (ErrDependencyUnavailable) still retry.
			metrics.IncProvisioningFailed("trial", "org_membership", "contract_error") // LLD §11.2
			return nil, err
		}
		lastErr = err
		if attempt < maxI1InlineRetries {
			// Short backoff with ±20% jitter to avoid thundering-herd on retry.
			base := []time.Duration{100 * time.Millisecond, 250 * time.Millisecond}[attempt]
			jitter := time.Duration(rand.Int63n(int64(base / 5)))
			select {
			case <-ctx.Done():
				goto exhaust
			case <-time.After(base + jitter):
			}
		}
	}

exhaust:
	// I-1 exhausted inline retries → 202, browser polls SB-3
	h.State = domain.StateTenantPending
	h.I1Attempts += maxI1InlineRetries + 1
	if lastErr != nil {
		h.LastError = lastErr.Error()
	}
	h.UpdatedAt = o.clock.Now()
	_ = o.handoffs.Update(ctx, h)
	return &OrchestratorResult{State: domain.StateTenantPending}, nil
}

func (o *HandoffOrchestrator) callI1Once(ctx context.Context, h *domain.Handoff) (*OrchestratorResult, error) {
	req := port.ProvisionTenantRequest{
		TenantID:      h.TenantID,
		Slug:          h.Slug,
		Name:          h.CompanyName,
		Plan:          string(h.Plan),
		OwnerUserID:   h.KeycloakUserID,
		DefaultLocale: h.DefaultLocale,
		LicensedSeats: h.LicensedSeats,
		// AdminEmailHMAC carries the HMAC-SHA256 hex of lower(email). O&M writes it
		// atomically into trial_signup_ledger inside the I-1 tx (CR-4). The backstop
		// consumer omits it legitimately — the omitempty tag handles the empty case.
		EmailHash: h.AdminEmailHMAC,
	}
	resp, err := o.om.ProvisionTenant(ctx, req)
	if err != nil {
		if isDomainCause(err, domain.ErrSlugTaken) {
			// Slug conflict — ask user to pick another
			suggestions := domain.SuggestSlugs(h.Slug, h.CompanyName, nil)
			h.State = domain.StateSlugConflict
			h.UpdatedAt = o.clock.Now()
			_ = o.handoffs.Update(ctx, h)
			return &OrchestratorResult{
				State:           domain.StateSlugConflict,
				SlugSuggestions: suggestions,
			}, err
		}
		return nil, err // 5xx / timeout → caller decides retry
	}

	if resp.TenantID != "" && resp.TenantID != h.TenantID {
		return nil, domain.NewError(domain.ErrInternal, "I-1 returned unexpected tenant ID")
	}
	h.State = domain.StateCompleted
	h.UpdatedAt = o.clock.Now()
	_ = o.handoffs.Update(ctx, h)
	metrics.IncProvisioningSucceeded("trial", "inline") // LLD §11.2 — strategy=inline (I-1 completed within SB-2 request)
	return &OrchestratorResult{
		State:          domain.StateCompleted,
		KeycloakUserID: h.KeycloakUserID,
	}, nil
}

// seedJSON serialises a HashedPasswordSeed to its JSON wire form.
func seedJSON(seed *port.HashedPasswordSeed) (string, error) {
	b, err := json.Marshal(seed)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func isDomainCause(err error, cause error) bool {
	var de *domain.DomainError
	if errors.As(err, &de) {
		return errors.Is(de.Cause, cause)
	}
	return false
}
