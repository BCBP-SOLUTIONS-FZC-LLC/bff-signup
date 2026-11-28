package port

import (
	"context"
	"time"
)

// Session is the data held in Valkey for a browser signup session (LLD §4.1).
type Session struct {
	SID       string // 256-bit random session id
	CSRFHash  string // SHA-256 of the CSRF token
	SignupID  string // ULID bound to this session
	CreatedAt int64  // Unix timestamp
	// HoldUntil (Unix seconds, 0 = none): the session must live at least until
	// then — set for SB-5's 3-D Secure window (Billing hand-off SB-1).
	HoldUntil int64
}

// SessionStore manages ephemeral signup sessions in Valkey.
type SessionStore interface {
	// Create persists a new session with a 30-min sliding TTL.
	Create(ctx context.Context, s *Session) error
	// Get retrieves the session; returns nil if not found or expired.
	Get(ctx context.Context, sid string) (*Session, error)
	// Refresh slides the TTL to the configured session TTL, never shortening it
	// (a held session keeps its longer TTL).
	Refresh(ctx context.Context, sid string) error
	// Hold keeps the session alive until at least until: it records HoldUntil
	// and extends the TTL to cover it. It never shortens an existing TTL.
	Hold(ctx context.Context, sid string, until time.Time) error
	// Delete removes the session immediately.
	Delete(ctx context.Context, sid string) error
}
