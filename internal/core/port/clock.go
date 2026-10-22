package port

import "time"

// Clock abstracts time.Now() for testability.
type Clock interface {
	Now() time.Time
}

// RealClock returns the system time.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now().UTC() }

// IDGenerator generates unique identifiers.
type IDGenerator interface {
	// NewULID returns a new ULID string (used for signup_id).
	NewULID() string
	// NewUUIDv7 returns a new UUIDv7 string (used for tenant_id).
	NewUUIDv7() string
}
