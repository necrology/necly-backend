package handler

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrOAuthStateNotFound means the state was absent, already consumed or
// expired. Callers must treat all three cases as a failed login.
var ErrOAuthStateNotFound = errors.New("oauth state not found")

// OAuthStateStore persists the short-lived binding between an OAuth state value
// and the browser nonce that requested it. Consume must be atomic (single-use)
// so two concurrent callbacks cannot both redeem the same state.
type OAuthStateStore interface {
	Save(ctx context.Context, state, nonce string, ttl time.Duration) error
	Consume(ctx context.Context, state string) (nonce string, err error)
}

type redisOAuthStateStore struct {
	client *redis.Client
}

func newRedisOAuthStateStore(client *redis.Client) *redisOAuthStateStore {
	return &redisOAuthStateStore{client: client}
}

func oauthStateKey(state string) string { return "oauth:state:" + state }

func (s *redisOAuthStateStore) Save(ctx context.Context, state, nonce string, ttl time.Duration) error {
	return s.client.Set(ctx, oauthStateKey(state), nonce, ttl).Err()
}

func (s *redisOAuthStateStore) Consume(ctx context.Context, state string) (string, error) {
	nonce, err := s.client.GetDel(ctx, oauthStateKey(state)).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrOAuthStateNotFound
	}
	if err != nil {
		return "", err
	}
	return nonce, nil
}
