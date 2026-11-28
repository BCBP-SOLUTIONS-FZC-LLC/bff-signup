package valkey

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

// SessionStore implements port.SessionStore using Valkey (LLD §4.1).
type SessionStore struct {
	rdb *redis.Client
	ttl time.Duration
}

var _ port.SessionStore = (*SessionStore)(nil)

func NewSessionStore(rdb *redis.Client, ttl time.Duration) *SessionStore {
	return &SessionStore{rdb: rdb, ttl: ttl}
}

func (s *SessionStore) Create(ctx context.Context, sess *port.Session) error {
	data, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	return runOp(ctx, func(opCtx context.Context) error {
		return s.rdb.Set(opCtx, sessKey(sess.SID), data, s.ttl).Err()
	})
}

func (s *SessionStore) Get(ctx context.Context, sid string) (*port.Session, error) {
	var raw []byte
	err := runOp(ctx, func(opCtx context.Context) error {
		var e error
		raw, e = s.rdb.Get(opCtx, sessKey(sid)).Bytes()
		return e
	})
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sess port.Session
	if err := json.Unmarshal(raw, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

// Refresh slides the session TTL. EXPIRE … GT never shortens it, so a session
// held for a 3-D Secure window (Hold) keeps its longer TTL across polls.
func (s *SessionStore) Refresh(ctx context.Context, sid string) error {
	return runOp(ctx, func(opCtx context.Context) error {
		return s.rdb.ExpireGT(opCtx, sessKey(sid), s.ttl).Err()
	})
}

// Hold records HoldUntil on the session and extends its TTL to cover it (at
// least the normal TTL; never shorter than what it already has).
func (s *SessionStore) Hold(ctx context.Context, sid string, until time.Time) error {
	sess, err := s.Get(ctx, sid)
	if err != nil || sess == nil {
		return err
	}
	if u := until.Unix(); u > sess.HoldUntil {
		sess.HoldUntil = u
	}
	ttl := s.ttl
	if d := time.Until(time.Unix(sess.HoldUntil, 0)); d > ttl {
		ttl = d
	}
	data, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	return runOp(ctx, func(opCtx context.Context) error {
		if cur, e := s.rdb.PTTL(opCtx, sessKey(sid)).Result(); e == nil && cur > ttl {
			ttl = cur
		}
		// XX: never re-create a session deleted or expired since the Get.
		err := s.rdb.SetArgs(opCtx, sessKey(sid), data, redis.SetArgs{Mode: "XX", TTL: ttl}).Err()
		if errors.Is(err, redis.Nil) {
			return nil
		}
		return err
	})
}

func (s *SessionStore) Delete(ctx context.Context, sid string) error {
	return runOp(ctx, func(opCtx context.Context) error {
		return s.rdb.Del(opCtx, sessKey(sid)).Err()
	})
}
