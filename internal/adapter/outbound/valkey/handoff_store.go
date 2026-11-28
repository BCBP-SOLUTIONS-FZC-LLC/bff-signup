package valkey

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

const (
	handoffTTL   = 30 * time.Minute
	completedTTL = 10 * time.Minute
	lockTTL      = 15 * time.Second
)

// HandoffStore implements port.HandoffStore using Valkey (LLD §4.1, §4.2).
type HandoffStore struct {
	rdb     *redis.Client
	lockTTL time.Duration
}

var _ port.HandoffStore = (*HandoffStore)(nil)

func NewHandoffStore(rdb *redis.Client) *HandoffStore {
	return &HandoffStore{rdb: rdb, lockTTL: lockTTL}
}

// WithLockTTL sets the handoff lock lifetime. It must outlast the longest
// request that holds the lock (config.HandoffLockTTL); non-positive keeps 15 s.
func (s *HandoffStore) WithLockTTL(ttl time.Duration) *HandoffStore {
	if ttl > 0 {
		s.lockTTL = ttl
	}
	return s
}

func (s *HandoffStore) Create(ctx context.Context, h *domain.Handoff) error {
	data, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return runOp(ctx, func(opCtx context.Context) error {
		return s.rdb.Set(opCtx, handoffKey(h.SignupID), data, handoffTTLFor(h)).Err()
	})
}

// handoffTTLFor is the handoff's lifetime: 10 min once completed, else 30 min —
// extended to Billing's 3-D Secure window + 15 min when one was opened, so a late
// re-POST still finds the same tenant_id and charge key (Billing hand-off SB-1).
// The hold applies to a completed handoff too: the session is held as long, and
// a duplicate SB-5 on it must report the state, not allocate a new tenant_id.
func handoffTTLFor(h *domain.Handoff) time.Duration {
	ttl := handoffTTL
	if h.State == domain.StateCompleted {
		ttl = completedTTL
	}
	if until := h.PaymentHoldUntil(); !until.IsZero() {
		if d := time.Until(until); d > ttl {
			ttl = d
		}
	}
	return ttl
}

func (s *HandoffStore) Get(ctx context.Context, signupID string) (*domain.Handoff, error) {
	var raw []byte
	err := runOp(ctx, func(opCtx context.Context) error {
		var e error
		raw, e = s.rdb.Get(opCtx, handoffKey(signupID)).Bytes()
		return e
	})
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var h domain.Handoff
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, err
	}
	return &h, nil
}

func (s *HandoffStore) Update(ctx context.Context, h *domain.Handoff) error {
	ttl := handoffTTLFor(h)
	data, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return runOp(ctx, func(opCtx context.Context) error {
		return s.rdb.Set(opCtx, handoffKey(h.SignupID), data, ttl).Err()
	})
}

func (s *HandoffStore) Delete(ctx context.Context, signupID string) error {
	return runOp(ctx, func(opCtx context.Context) error {
		return s.rdb.Del(opCtx, handoffKey(signupID)).Err()
	})
}

var releaseLockScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
    return redis.call("del", KEYS[1])
else
    return 0
end
`)

func (s *HandoffStore) AcquireLock(ctx context.Context, signupID string) (string, bool, error) {
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", false, err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	var ok bool
	err := runOp(ctx, func(opCtx context.Context) error {
		var e error
		ok, e = s.rdb.SetNX(opCtx, lockKey(signupID), token, s.lockTTL).Result()
		return e
	})
	if err != nil {
		return "", false, err
	}
	return token, ok, nil
}

func (s *HandoffStore) ReleaseLock(ctx context.Context, signupID, token string) error {
	return runOp(ctx, func(opCtx context.Context) error {
		return releaseLockScript.Run(opCtx, s.rdb, []string{lockKey(signupID)}, token).Err()
	})
}

func (s *HandoffStore) Ping(ctx context.Context) error {
	return Ping(ctx, s.rdb)
}
