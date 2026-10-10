// Package rediscache holds small Redis-backed helpers that are not tied to any
// single domain.
package rediscache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// Cooldown is a "one action per subject per window" gate backed by Redis keys.
//
// It is nil-safe: a nil *Cooldown, or one built without a client, reports no
// cooldown and silently accepts Block. That keeps local development and any
// deployment without Redis working, with the caller's database fallback taking
// over the job of rejecting repeats.
type Cooldown struct {
	client *redis.Client
	prefix string
	ttl    time.Duration
}

// NewCooldown builds a gate whose entries expire after ttl. Pass a nil client to
// get a disabled gate.
func NewCooldown(client *redis.Client, prefix string, ttl time.Duration) *Cooldown {
	return &Cooldown{client: client, prefix: prefix, ttl: ttl}
}

// BlockedFor returns how long the subject must still wait before acting again,
// or zero when it may act now.
func (c *Cooldown) BlockedFor(ctx context.Context, subject string) (time.Duration, error) {
	if c == nil || c.client == nil {
		return 0, nil
	}
	ttl, err := c.client.TTL(ctx, c.key(subject)).Result()
	if err != nil {
		// A Redis outage must not block a customer from reporting a problem.
		return 0, nil
	}
	// TTL returns -2 for a missing key and -1 for a key without expiry.
	if ttl <= 0 {
		return 0, nil
	}
	return ttl, nil
}

// Block starts (or restarts) the cooldown window for subject.
func (c *Cooldown) Block(ctx context.Context, subject string) error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Set(ctx, c.key(subject), "1", c.ttl).Err()
}

func (c *Cooldown) key(subject string) string { return c.prefix + subject }
