package service

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	// TOTPPeriod is the RFC 6238 time step in seconds. The product contract
	// fixes six digits and 30 seconds, so both are part of the API response.
	TOTPPeriod = 30
	totpDigits = 6

	// The endpoint is cheap but still reveals whether a secret exists, so each
	// account/agent pair gets a small budget. This is deliberately in-memory:
	// the limit is a guardrail, not a cross-instance security boundary.
	otpRateLimit  = 10
	otpRateWindow = time.Minute
)

// OTPCode is the only OTP material the API returns. The secret never leaves
// the service and is never included in an error or audit entry.
type OTPCode struct {
	Code      string `json:"code"`
	ExpiresIn int    `json:"expires_in"`
	Period    int    `json:"period"`
}

// GenerateTOTP derives a six-digit RFC 6238 code (HMAC-SHA1) for the given
// base32 secret. It is exported so tests can pin the standard vectors without
// reaching through the repository.
func GenerateTOTP(secret string, at time.Time) (OTPCode, error) {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return OTPCode{}, err
	}
	counter := uint64(at.Unix() / TOTPPeriod)
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], counter)

	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(message[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	code := value % 1_000_000
	remaining := TOTPPeriod - int(at.Unix()%TOTPPeriod)
	if remaining == 0 || remaining > TOTPPeriod {
		remaining = TOTPPeriod
	}
	return OTPCode{Code: fmt.Sprintf("%0*d", totpDigits, code), ExpiresIn: remaining, Period: TOTPPeriod}, nil
}

// normalizeOTPSecret validates a base32 TOTP seed and returns its canonical
// form. Spaces, lower case and optional '=' padding are accepted because
// authenticator apps display seeds that way; invalid characters fail closed.
func normalizeOTPSecret(secret string) (string, error) {
	canonical := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	canonical = strings.TrimRight(canonical, "=")
	if canonical == "" {
		return "", ErrOTPSecretInvalid
	}
	data, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(canonical)
	if err != nil || len(data) == 0 {
		return "", ErrOTPSecretInvalid
	}
	return canonical, nil
}

func decodeTOTPSecret(secret string) ([]byte, error) {
	canonical, err := normalizeOTPSecret(secret)
	if err != nil {
		return nil, err
	}
	data, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(canonical)
	if err != nil || len(data) == 0 {
		return nil, ErrOTPSecretInvalid
	}
	return data, nil
}

// otpRateLimiter is a tiny fixed-window counter keyed by account and actor.
// Expired keys are pruned on every call and the map is capped so a long-lived
// process cannot grow it without bound.
type otpRateLimiter struct {
	mu       sync.Mutex
	attempts map[string]otpAttempt
	now      func() time.Time
	max      int
	window   time.Duration
}

type otpAttempt struct {
	count   int
	resetAt time.Time
}

func newOTPRateLimiter(now func() time.Time) *otpRateLimiter {
	return &otpRateLimiter{
		attempts: make(map[string]otpAttempt),
		now:      now,
		max:      otpRateLimit,
		window:   otpRateWindow,
	}
}

func (l *otpRateLimiter) allow(key string) bool {
	if l == nil {
		return true
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.attempts) > 1024 {
		for candidate, attempt := range l.attempts {
			if !attempt.resetAt.After(now) {
				delete(l.attempts, candidate)
			}
		}
	}
	attempt, ok := l.attempts[key]
	if !ok || !attempt.resetAt.After(now) {
		l.attempts[key] = otpAttempt{count: 1, resetAt: now.Add(l.window)}
		return true
	}
	if attempt.count >= l.max {
		return false
	}
	attempt.count++
	l.attempts[key] = attempt
	return true
}

// ErrOTPSecretInvalid is intentionally opaque: the stored or submitted secret
// must never appear in an error message, log line, or audit metadata.
var ErrOTPSecretInvalid = errors.New("Secret OTP tidak valid")
