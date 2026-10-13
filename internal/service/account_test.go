package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeAudit struct {
	mu      sync.Mutex
	entries []AuditEntry
}

func (a *fakeAudit) Record(_ context.Context, entry AuditEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, entry)
}

func (a *fakeAudit) actions() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, 0, len(a.entries))
	for _, entry := range a.entries {
		out = append(out, entry.Action)
	}
	return out
}

func (a *fakeAudit) has(action string) bool {
	for _, seen := range a.actions() {
		if seen == action {
			return true
		}
	}
	return false
}

func TestChangePasswordRotatesThePasswordAndEndsOtherSessions(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	mail := &fakeMail{enabled: true}
	auth := newAuthServiceWithResetForTest(t, repo, now, mail, "https://necly-service.my.id")
	user := seededVerifiedUser(t, auth, repo, now)
	audit := &fakeAudit{}
	auth.audit = audit

	phone, err := auth.Login(context.Background(), LoginInput{Email: user.Email, Password: "Str0ng-password!"}, ClientInfo{})
	if err != nil {
		t.Fatal(err)
	}
	laptop, err := auth.Login(context.Background(), LoginInput{Email: user.Email, Password: "Str0ng-password!"}, ClientInfo{})
	if err != nil {
		t.Fatal(err)
	}

	if err := auth.ChangePassword(context.Background(), user.ID, "Str0ng-password!", "N3w-strong-password!", laptop.RefreshToken, ClientInfo{}); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}

	if _, err := auth.Login(context.Background(), LoginInput{Email: user.Email, Password: "Str0ng-password!"}, ClientInfo{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("login with the old password error = %v, want unauthorized", err)
	}
	if _, err := auth.Login(context.Background(), LoginInput{Email: user.Email, Password: "N3w-strong-password!"}, ClientInfo{}); err != nil {
		t.Fatalf("login with the new password error = %v", err)
	}

	// The device that made the change stays signed in; the other one does not.
	if _, err := auth.Refresh(context.Background(), laptop.RefreshToken, ClientInfo{}); err != nil {
		t.Fatalf("the session that changed the password was ended: %v", err)
	}
	if _, err := auth.Refresh(context.Background(), phone.RefreshToken, ClientInfo{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("the other session survived the change: error = %v, want unauthorized", err)
	}
	if !audit.has(AuditPasswordChanged) {
		t.Fatal("the password change was not audited")
	}
}

func TestChangePasswordRejectsWrongShortAndRepeatedPasswords(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	auth := newAuthServiceWithResetForTest(t, repo, now, nil, "https://necly-service.my.id")
	user := seededVerifiedUser(t, auth, repo, now)

	cases := map[string]struct{ current, next string }{
		"kata sandi saat ini salah": {"Bukan-password-ku!", "N3w-strong-password!"},
		"kata sandi baru pendek":    {"Str0ng-password!", "pendek"},
		"kata sandi tidak berubah":  {"Str0ng-password!", "Str0ng-password!"},
	}
	for name, input := range cases {
		if err := auth.ChangePassword(context.Background(), user.ID, input.current, input.next, "", ClientInfo{}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s: error = %v, want ErrInvalidInput", name, err)
		}
	}
	// None of the rejected attempts may have touched the stored hash.
	if _, err := auth.Login(context.Background(), LoginInput{Email: user.Email, Password: "Str0ng-password!"}, ClientInfo{}); err != nil {
		t.Fatalf("the original password stopped working: %v", err)
	}
}

func TestListAndRevokeSessions(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	auth := newAuthServiceWithResetForTest(t, repo, now, nil, "https://necly-service.my.id")
	user := seededVerifiedUser(t, auth, repo, now)

	first, err := auth.Login(context.Background(), LoginInput{Email: user.Email, Password: "Str0ng-password!"}, ClientInfo{IPAddress: "10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Login(context.Background(), LoginInput{Email: user.Email, Password: "Str0ng-password!"}, ClientInfo{IPAddress: "10.0.0.2"}); err != nil {
		t.Fatal(err)
	}

	sessions, err := auth.ListSessions(context.Background(), user.ID, first.RefreshToken)
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("active sessions = %d, want 2", len(sessions))
	}
	current := 0
	for _, session := range sessions {
		if session.Current {
			current++
		}
	}
	if current != 1 {
		t.Fatalf("sessions flagged as current = %d, want exactly 1", current)
	}

	// The session making the request cannot be ended through the list.
	var currentID, otherID string
	for _, session := range sessions {
		if session.Current {
			currentID = session.ID
		} else {
			otherID = session.ID
		}
	}
	if err := auth.RevokeSession(context.Background(), user.ID, currentID, first.RefreshToken, ClientInfo{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("revoking the current session error = %v, want ErrInvalidInput", err)
	}
	// Ending one named device works and leaves the caller signed in.
	if err := auth.RevokeSession(context.Background(), user.ID, otherID, first.RefreshToken, ClientInfo{}); err != nil {
		t.Fatalf("RevokeSession() error = %v", err)
	}

	// The other device is already gone, so a second sweep ends nothing.
	ended, err := auth.RevokeOtherSessions(context.Background(), user.ID, first.RefreshToken, ClientInfo{})
	if err != nil {
		t.Fatalf("RevokeOtherSessions() error = %v", err)
	}
	if ended != 0 {
		t.Fatalf("ended sessions = %d, want 0", ended)
	}
	remaining, err := auth.ListSessions(context.Background(), user.ID, first.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || !remaining[0].Current {
		t.Fatalf("remaining sessions = %+v, want only the current one", remaining)
	}
}

func TestRevokeSessionRejectsAnotherAccountsID(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	auth := newAuthServiceWithResetForTest(t, repo, now, nil, "https://necly-service.my.id")
	alice := seededVerifiedUser(t, auth, repo, now)
	session, err := auth.Login(context.Background(), LoginInput{Email: alice.Email, Password: "Str0ng-password!"}, ClientInfo{})
	if err != nil {
		t.Fatal(err)
	}

	sessions, err := auth.ListSessions(context.Background(), alice.ID, session.RefreshToken)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("ListSessions() = %v, %v", sessions, err)
	}
	if err := auth.RevokeSession(context.Background(), "user-lain", sessions[0].ID, "", ClientInfo{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoking another account's session error = %v, want ErrNotFound", err)
	}
	if _, err := auth.Refresh(context.Background(), session.RefreshToken, ClientInfo{}); err != nil {
		t.Fatalf("the session was ended by a foreign request: %v", err)
	}
}

// The audit log had a viewer but no writer, so it was permanently empty.
func TestAuthEventsAreAudited(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	mail := &fakeMail{enabled: true}
	auth := newAuthServiceWithResetForTest(t, repo, now, mail, "https://necly-service.my.id")
	audit := &fakeAudit{}
	auth.audit = audit
	user := seededVerifiedUser(t, auth, repo, now)

	// A failed sign-in must leave a trace even though no account is involved.
	if _, err := auth.Login(context.Background(), LoginInput{Email: "hantu@example.com", Password: "apa-saja-12345"}, ClientInfo{IPAddress: "203.0.113.9"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("sign-in with an unknown address error = %v, want unauthorized", err)
	}
	if !audit.has(AuditLoginFailed) {
		t.Fatal("a failed sign-in was not audited")
	}

	if _, err := auth.Login(context.Background(), LoginInput{Email: user.Email, Password: "Str0ng-password!"}, ClientInfo{}); err != nil {
		t.Fatal(err)
	}
	if !audit.has(AuditLoginSucceeded) {
		t.Fatal("a successful sign-in was not audited")
	}

	deactivated := user
	deactivated.Active = false
	if _, err := auth.Login(context.Background(), LoginInput{Email: user.Email, Password: "Str0ng-password!"}, ClientInfo{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("deactivated sign-in error = %v, want unauthorized", err)
	}
	if !audit.has(AuditLoginBlocked) {
		t.Fatal("a blocked sign-in was not audited")
	}
}
