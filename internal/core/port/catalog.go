package port

import (
	"context"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
)

// Catalog provides the plan list shown to the user in SB-1 (LLD §6, N-2).
// The adapter caches in-process for 5 minutes and falls back to the static
// plansFallback config when the Catalog Service is unreachable.
type Catalog interface {
	Plans(ctx context.Context) ([]domain.PlanInfo, error)
}
