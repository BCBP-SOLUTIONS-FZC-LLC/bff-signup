package service

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/metrics"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

// StatusService handles SB-3 and SB-4 (LLD §5.5).
type StatusService struct {
	handoffs       port.HandoffStore
	limiter        port.RateLimiter
	orchestrator   *HandoffOrchestrator
	clock          port.Clock
	reserved       map[string]bool
	pollsPerMinute int
}

func NewStatusService(
	handoffs port.HandoffStore,
	limiter port.RateLimiter,
	orchestrator *HandoffOrchestrator,
	clock port.Clock,
	reserved map[string]bool,
	pollsPerMinute int,
) *StatusService {
	return &StatusService{handoffs: handoffs, limiter: limiter, orchestrator: orchestrator, clock: clock, reserved: reserved, pollsPerMinute: pollsPerMinute}
}

// StatusResult is the SB-3 response payload.
type StatusResult struct {
	SignupID        string
	State           domain.HandoffState
	Action          string   // "choose_slug" when State == action_required
	PollAfterMs     int      // set when State == provisioning
	Message         string   // "workspace_setting_up" when tenant_pending
	SlugSuggestions []string // non-empty when ChangeSlug hit a conflict (SB-4, A-3)
}

// GetStatus handles SB-3 (LLD §5.5, §8.2).
// Rate-limited to 60 polls per minute per session.
func (s *StatusService) GetStatus(ctx context.Context, sessionSignupID, pathSignupID, sid string) (*StatusResult, error) {
	// Session must match the path signup_id (no hint if it exists for another session)
	if sessionSignupID != pathSignupID {
		return nil, domain.NewError(domain.ErrSignupNotFound, "signup not found")
	}

	// Rate limit polls
	count, resetAt, err := s.limiter.IncrPoll(ctx, sid)
	if err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "rate limiter unavailable")
	}
	if count > int64(s.pollsPerMinute) {
		metrics.IncRateLimited("poll") // LLD §11.2
		return nil, domain.NewError(domain.ErrRateLimited, "too many status polls").
			WithDetails(map[string]any{"retry_after_seconds": retryAfterSecs(resetAt), "scope": "poll"})
	}

	h, err := s.handoffs.Get(ctx, pathSignupID)
	if err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "handoff store unavailable")
	}
	if h == nil {
		return nil, domain.NewError(domain.ErrSignupNotFound, "signup not found")
	}

	// If tenant_pending: acquire lock and attempt one I-1 resume
	if h.State == domain.StateTenantPending {
		lockToken, acquired, _ := s.handoffs.AcquireLock(ctx, h.SignupID)
		if acquired {
			defer s.handoffs.ReleaseLock(ctx, h.SignupID, lockToken) //nolint:errcheck
			metrics.IncHandoffResumed("poll")                        // LLD §11.2
			result, err := s.orchestrator.ResumeI1(ctx, h)
			if err == nil && result != nil {
				h.State = result.State
				if result.State == domain.StateCompleted {
					metrics.IncProvisioningSucceeded("trial", "resume") // LLD §11.2
				}
			}
		}
	}

	// Business attributes on the request span (LLD §11.3, O-03). IDs and state only.
	trace.SpanFromContext(ctx).SetAttributes(
		attribute.String("signup.id", h.SignupID),
		attribute.String("tenant.id", h.TenantID),
		attribute.String("handoff.state", string(h.State)),
	)
	return stateToStatus(h), nil
}

// ChangeSlug handles SB-4 (LLD §5.5, §8.3).
func (s *StatusService) ChangeSlug(ctx context.Context, sessionSignupID, pathSignupID, newSlug string) (*StatusResult, error) {
	if sessionSignupID != pathSignupID {
		return nil, domain.NewError(domain.ErrSignupNotFound, "signup not found")
	}

	// Validate new slug
	if err := domain.ValidateSlug(newSlug, s.reserved); err != nil {
		return nil, err
	}

	h, err := s.handoffs.Get(ctx, pathSignupID)
	if err != nil {
		return nil, domain.NewError(domain.ErrDependencyUnavailable, "handoff store unavailable")
	}
	if h == nil {
		return nil, domain.NewError(domain.ErrSignupNotFound, "signup not found")
	}
	if !h.CanChangeSlug() {
		return nil, domain.NewError(domain.ErrInvalidState, "handoff is not awaiting a new slug")
	}

	lockToken, acquired, _ := s.handoffs.AcquireLock(ctx, h.SignupID)
	if !acquired {
		return nil, domain.NewError(domain.ErrInvalidState, "another request is processing this signup").
			WithDetails(map[string]any{"retry_after_ms": 500})
	}
	defer s.handoffs.ReleaseLock(ctx, h.SignupID, lockToken) //nolint:errcheck

	result, err := s.orchestrator.ChangeSlug(ctx, h, newSlug)
	if err != nil && !isDomainCause(err, domain.ErrSlugTaken) {
		return nil, err
	}

	h.State = result.State
	if result.State == domain.StateCompleted {
		metrics.IncProvisioningSucceeded("trial", "slug_change") // LLD §11.2
	}
	sr := stateToStatus(h)
	sr.SlugSuggestions = result.SlugSuggestions
	// Propagate ErrSlugTaken so the handler can build a proper 409 with suggestions (A-3).
	return sr, err
}

func stateToStatus(h *domain.Handoff) *StatusResult {
	r := &StatusResult{SignupID: h.SignupID, State: h.State}
	switch h.State { //nolint:exhaustive
	case domain.StateTenantPending:
		r.PollAfterMs = 2000
		r.Message = "workspace_setting_up"
	case domain.StateSlugConflict:
		r.Action = "choose_slug"
	}
	return r
}
