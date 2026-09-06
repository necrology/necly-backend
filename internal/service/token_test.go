package service

import (
	"testing"
	"time"
)

func TestTokenManagerIssuesShortLivedAccessAndOpaqueRefreshTokens(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	manager, err := NewTokenManager("a-development-secret-that-is-at-least-32-bytes", "necly", 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	manager.now = func() time.Time { return now }

	access, err := manager.IssueAccess("user-1", "admin")
	if err != nil {
		t.Fatalf("IssueAccess() error = %v", err)
	}
	claims, err := manager.ParseAccess(access)
	if err != nil {
		t.Fatalf("ParseAccess() error = %v", err)
	}
	if claims.Subject != "user-1" || claims.Role != "admin" {
		t.Fatalf("claims = %#v", claims)
	}
	if got := claims.ExpiresAt.Time.Sub(now); got != 15*time.Minute {
		t.Fatalf("access TTL = %v, want 15m", got)
	}

	raw, hash, expiresAt, err := manager.IssueRefresh()
	if err != nil {
		t.Fatalf("IssueRefresh() error = %v", err)
	}
	if raw == "" || hash == "" || raw == hash {
		t.Fatalf("refresh token must be opaque and stored only as a hash")
	}
	if HashToken(raw) != hash {
		t.Fatal("refresh hash cannot be reproduced for lookup")
	}
	if expiresAt.Sub(now) != 7*24*time.Hour {
		t.Fatalf("refresh TTL = %v", expiresAt.Sub(now))
	}
}

func TestTokenManagerRejectsWrongSecretAndWrongType(t *testing.T) {
	first, _ := NewTokenManager("first-secret-at-least-thirty-two-bytes-long", "necly", time.Minute, time.Hour)
	second, _ := NewTokenManager("second-secret-at-least-thirty-two-bytes", "necly", time.Minute, time.Hour)
	access, _ := first.IssueAccess("user-1", "customer")
	if _, err := second.ParseAccess(access); err == nil {
		t.Fatal("ParseAccess accepted a token signed with another secret")
	}
}
