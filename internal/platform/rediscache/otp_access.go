package rediscache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// OTPAccessCodes stores short-lived email-OTP access codes. The service passes
// an already-hashed key, so Redis never holds the plaintext code. A missing
// Redis client makes every operation fail closed instead of pretending the code
// was stored.
type OTPAccessCodes struct {
	client *redis.Client
	prefix string
}

// NewOTPAccessCodes builds the store. Pass a nil client to represent an
// unavailable cache; Store and Load then return ErrOTPAccessStoreUnavailable.
func NewOTPAccessCodes(client *redis.Client, prefix string) *OTPAccessCodes {
	if prefix == "" {
		prefix = "necly:otp-access:"
	}
	return &OTPAccessCodes{client: client, prefix: prefix}
}

// ErrOTPAccessStoreUnavailable is returned when Redis is not configured.
var ErrOTPAccessStoreUnavailable = errors.New("otp access code store is unavailable")

func (s *OTPAccessCodes) Store(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if s == nil || s.client == nil {
		return ErrOTPAccessStoreUnavailable
	}
	return s.client.Set(ctx, s.prefix+key, value, ttl).Err()
}

func (s *OTPAccessCodes) Load(ctx context.Context, key string) ([]byte, bool, error) {
	if s == nil || s.client == nil {
		return nil, false, ErrOTPAccessStoreUnavailable
	}
	value, err := s.client.Get(ctx, s.prefix+key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}
