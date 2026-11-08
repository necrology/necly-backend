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

var storeIssuedScript = redis.NewScript(`
local old = redis.call("HGET", KEYS[2], "code_key")
if old then
  redis.call("DEL", KEYS[3] .. old)
end
redis.call("SET", KEYS[1], ARGV[1], "PX", ARGV[4])
redis.call("HSET", KEYS[2], "code_key", ARGV[2], "expires_unix", ARGV[3])
redis.call("PEXPIRE", KEYS[2], ARGV[4])
return 1
`)

// StoreIssued atomically invalidates the previous hashed code for the scope,
// writes the new hashed-code payload, and stores only its expiry metadata in the
// scope index. Neither key nor value contains the plaintext access code.
func (s *OTPAccessCodes) StoreIssued(ctx context.Context, codeKey, scopeKey string, value []byte, expiresUnix int64, ttl time.Duration) error {
	if s == nil || s.client == nil {
		return ErrOTPAccessStoreUnavailable
	}
	if ttl <= 0 {
		return errors.New("otp access code ttl must be positive")
	}
	return storeIssuedScript.Run(ctx, s.client,
		[]string{s.prefix + codeKey, s.prefix + scopeKey, s.prefix},
		value, codeKey, expiresUnix, ttl.Milliseconds(),
	).Err()
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

func (s *OTPAccessCodes) LoadStatus(ctx context.Context, scopeKey string) (int64, bool, error) {
	if s == nil || s.client == nil {
		return 0, false, ErrOTPAccessStoreUnavailable
	}
	expiresUnix, err := s.client.HGet(ctx, s.prefix+scopeKey, "expires_unix").Int64()
	if errors.Is(err, redis.Nil) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return expiresUnix, true, nil
}
