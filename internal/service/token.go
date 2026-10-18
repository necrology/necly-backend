package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type AccessClaims struct {
	Role string `json:"role"`
	Type string `json:"type"`
	// SessionID is the refresh-token row that backs this access token. The
	// middleware re-reads it on every request, so ending that sign-in or
	// deactivating the account invalidates the access token immediately
	// instead of waiting for its short TTL to expire.
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}

type TokenManager struct {
	secret     []byte
	issuer     string
	accessTTL  time.Duration
	refreshTTL time.Duration
	now        func() time.Time
}

func NewTokenManager(secret, issuer string, accessTTL, refreshTTL time.Duration) (*TokenManager, error) {
	if len(secret) < 32 {
		return nil, errors.New("JWT secret must be at least 32 bytes")
	}
	if issuer == "" || accessTTL <= 0 || refreshTTL <= 0 {
		return nil, errors.New("issuer and positive token TTLs are required")
	}
	return &TokenManager{secret: []byte(secret), issuer: issuer, accessTTL: accessTTL, refreshTTL: refreshTTL, now: time.Now}, nil
}

func (m *TokenManager) IssueAccess(userID, role, sessionID string) (string, error) {
	if sessionID == "" {
		return "", errors.New("session id is required")
	}
	now := m.now().UTC()
	jti, err := randomToken(16)
	if err != nil {
		return "", err
	}
	claims := AccessClaims{
		Role:      role,
		Type:      "access",
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   userID,
			ID:        jti,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.accessTTL)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
}

func (m *TokenManager) ParseAccess(raw string) (*AccessClaims, error) {
	claims := &AccessClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		return m.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer(m.issuer), jwt.WithTimeFunc(m.now))
	if err != nil || !token.Valid || claims.Type != "access" || claims.Subject == "" {
		if err == nil {
			err = errors.New("invalid access token")
		}
		return nil, err
	}
	return claims, nil
}

func (m *TokenManager) IssueRefresh() (raw, hash string, expiresAt time.Time, err error) {
	raw, err = randomToken(32)
	if err != nil {
		return "", "", time.Time{}, err
	}
	return raw, HashToken(raw), m.now().UTC().Add(m.refreshTTL), nil
}

func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func randomToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
