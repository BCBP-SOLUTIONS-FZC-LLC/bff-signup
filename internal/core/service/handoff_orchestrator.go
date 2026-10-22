package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

const (
	maxI1InlineRetries  = 2
	resumeWindowSeconds = 900 // 15 min
	maxSlugAttempts     = 5
)

// OrchestratorInput carries all the data needed for the eligibility → RP-1 → I-1 handoff.
type OrchestratorInput struct {
	Handoff            *domain.Handoff
	AdminEmail         string
	AdminName          string
	HashedPasswordSeed string // JSON-serialised; zeroed after RP-1 call
}

// HandoffOrchestrator runs the provisioning sequence (LLD §8.1, §9.1).
type HandoffOrchestrator struct {
	handoffs port.HandoffStore
	rp       port.RealmProvisioner
	om       port.OrgMembership
	clock    port.Clock
}

func NewHandoffOrchestrator(
	handoffs port.HandoffStore,
	rp port.RealmProvisioner,
	om port.OrgMembership,
	clock port.Clock,
) *HandoffOrchestrator {
	return &HandoffOrchestrator{handoffs, rp, om, clock}
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
		if err := o.om.CheckEligibility(ctx, h.AdminEmailHMAC); err != nil {
			_ = o.handoffs.Delete(ctx, h.SignupID)
			return nil, err // ErrTrialNotEligible or ErrDependencyUnavailable
		}
	}

	// ── Step 1: RP-1 provision-trial ─────────────────────────────────────────
	if h.State == domain.StateAllocated {
		idempotencyKey := fmt.Sprintf("signup:%s:rp1", h.TenantID)
		rpReq := port.ProvisionTrialRequest{
			TenantID:           h.TenantID,
			TenantName:         h.CompanyName,
			TenantSlug:         h.Slug,
			AdminEmail:         in.AdminEmail,
			AdminName:          in.AdminName,
			HashedPasswordSeed: in.HashedPasswordSeed,
			Plan:               string(h.Plan),
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
	if o.clock.Now().Sub(h.CreatedAt).Seconds() > resumeWindowSeconds {
		h.State = domain.StateFailed
		h.UpdatedAt = o.clock.Now()
		_ = o.handoffs.Update(ctx, h)
		return &OrchestratorResult{State: domain.StateFailed}, nil
	}

	return o.callI1Once(ctx, h)
}

// ChangeSlug retries I-1 with a new slug (SB-4) after a slug conflict.
func (o *HandoffOrchestrator) ChangeSlug(ctx context.Context, h *domain.Handoff, newSlug string) (*OrchestratorResult, error) {
	if !h.CanChangeSlug() {
		return nil, domain.NewError(domain.ErrInvalidState, "handoff is not in slug_conflict state")
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
	return o.callI1Once(ctx, h)
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
		lastErr = err
		if attempt < maxI1InlineRetries {
			// Short backoff: 100ms, 250ms
			backoff := []time.Duration{100 * time.Millisecond, 250 * time.Millisecond}
			select {
			case <-ctx.Done():
				goto exhaust
			case <-time.After(backoff[attempt]):
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

	_ = resp
	h.State = domain.StateCompleted
	h.UpdatedAt = o.clock.Now()
	_ = o.handoffs.Update(ctx, h)
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
	if de, ok := err.(*domain.DomainError); ok {
		return de.Cause == cause
	}
	return strings.Contains(err.Error(), cause.Error())
}
