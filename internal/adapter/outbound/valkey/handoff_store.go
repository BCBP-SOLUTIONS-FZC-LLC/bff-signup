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
	rdb *redis.Client
}

var _ port.HandoffStore = (*HandoffStore)(nil)

func NewHandoffStore(rdb *redis.Client) *HandoffStore { return &HandoffStore{rdb} }

func (s *HandoffStore) Create(ctx context.Context, h *domain.Handoff) error {
	data, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return runOp(ctx, func(opCtx context.Context) error {
		return s.rdb.Set(opCtx, handoffKey(h.SignupID), data, handoffTTL).Err()
	})
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
	ttl := handoffTTL
	if h.State == domain.StateCompleted {
		ttl = completedTTL
	}
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
		ok, e = s.rdb.SetNX(opCtx, lockKey(signupID), token, lockTTL).Result()
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
