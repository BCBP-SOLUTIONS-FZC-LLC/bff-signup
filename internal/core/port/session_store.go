package port

import "context"

// Session is the data held in Valkey for a browser signup session (LLD §4.1).
type Session struct {
	SID       string // 256-bit random session id
	CSRFHash  string // SHA-256 of the CSRF token
	SignupID  string // ULID bound to this session
	CreatedAt int64  // Unix timestamp
}

// SessionStore manages ephemeral signup sessions in Valkey.
type SessionStore interface {
	// Create persists a new session with a 30-min sliding TTL.
	Create(ctx context.Context, s *Session) error
	// Get retrieves the session; returns nil if not found or expired.
	Get(ctx context.Context, sid string) (*Session, error)
	// Refresh resets the 30-min sliding TTL on an existing session.
	Refresh(ctx context.Context, sid string) error
	// Delete removes the session immediately.
	Delete(ctx context.Context, sid string) error
}
