package valkey

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

const sessionTTL = 30 * time.Minute

// SessionStore implements port.SessionStore using Valkey (LLD §4.1).
type SessionStore struct {
	rdb *redis.Client
}

var _ port.SessionStore = (*SessionStore)(nil)

func NewSessionStore(rdb *redis.Client) *SessionStore { return &SessionStore{rdb} }

func (s *SessionStore) Create(ctx context.Context, sess *port.Session) error {
	data, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, sessKey(sess.SID), data, sessionTTL).Err()
}

func (s *SessionStore) Get(ctx context.Context, sid string) (*port.Session, error) {
	data, err := s.rdb.Get(ctx, sessKey(sid)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sess port.Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

func (s *SessionStore) Refresh(ctx context.Context, sid string) error {
	return s.rdb.Expire(ctx, sessKey(sid), sessionTTL).Err()
}

func (s *SessionStore) Delete(ctx context.Context, sid string) error {
	return s.rdb.Del(ctx, sessKey(sid)).Err()
}
