package port

import (
	"context"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
)

// HandoffStore manages the provisioning handoff records in Valkey (LLD §4.1, §4.2).
type HandoffStore interface {
	// Create persists a new handoff with a 30-min TTL.
	Create(ctx context.Context, h *domain.Handoff) error
	// Get retrieves the handoff by signupID; returns nil if not found.
	Get(ctx context.Context, signupID string) (*domain.Handoff, error)
	// Update replaces the handoff record and refreshes the TTL.
	// After StateCompleted the TTL is reduced to 10 minutes.
	Update(ctx context.Context, h *domain.Handoff) error
	// Delete removes the handoff immediately (e.g. after rejection).
	Delete(ctx context.Context, signupID string) error

	// AcquireLock attempts a 15-second distributed lock (SET NX PX).
	// Returns the lock token if acquired, or "" if already held.
	AcquireLock(ctx context.Context, signupID string) (token string, acquired bool, err error)
	// ReleaseLock releases the lock only if the token matches (Lua script).
	ReleaseLock(ctx context.Context, signupID, token string) error
}
