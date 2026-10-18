package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/necly/services/internal/domain"
)

func TestAuthorizeSessionUsesCurrentRoleAccountAndSessionState(t *testing.T) {
	now := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	auth := newAuthServiceWithResetForTest(t, repo, now, nil, "https://necly-service.my.id")
	user := seededVerifiedUser(t, auth, repo, now)

	session, err := auth.Login(context.Background(), LoginInput{Email: user.Email, Password: "Str0ng-password!"}, ClientInfo{})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := auth.tokens.ParseAccess(session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if claims.SessionID == "" {
		t.Fatal("access token is not bound to a refresh session")
	}

	current, err := auth.AuthorizeSession(context.Background(), user.ID, claims.SessionID)
	if err != nil {
		t.Fatalf("AuthorizeSession() error = %v", err)
	}
	if current.Role != domain.RoleCustomer {
		t.Fatalf("role = %q, want customer", current.Role)
	}

	// A role change must apply on the next request even though the JWT still
	// carries the old claim.
	repo.usersByID[user.ID].Role = domain.RoleAdmin
	current, err = auth.AuthorizeSession(context.Background(), user.ID, claims.SessionID)
	if err != nil {
		t.Fatalf("AuthorizeSession() after promotion error = %v", err)
	}
	if current.Role != domain.RoleAdmin {
		t.Fatalf("role = %q, want the current database role admin", current.Role)
	}

	repo.usersByID[user.ID].Active = false
	if _, err := auth.AuthorizeSession(context.Background(), user.ID, claims.SessionID); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("disabled account error = %v, want unauthorized", err)
	}
	repo.usersByID[user.ID].Active = true
	deleted := now
	repo.usersByID[user.ID].DeletedAt = &deleted
	if _, err := auth.AuthorizeSession(context.Background(), user.ID, claims.SessionID); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("deleted account error = %v, want unauthorized", err)
	}
	repo.usersByID[user.ID].DeletedAt = nil

	// Revoking the refresh row is the session revocation primitive used by
	// logout and the session list.
	if err := repo.RevokeRefreshToken(context.Background(), HashToken(session.RefreshToken), now); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AuthorizeSession(context.Background(), user.ID, claims.SessionID); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked session error = %v, want unauthorized", err)
	}
	if _, err := auth.AuthorizeSession(context.Background(), user.ID, ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("missing session id error = %v, want unauthorized", err)
	}
}

func TestRefreshBindsNewAccessTokenToTheRotatedSession(t *testing.T) {
	now := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	auth := newAuthServiceWithResetForTest(t, repo, now, nil, "https://necly-service.my.id")
	user := seededVerifiedUser(t, auth, repo, now)

	login, err := auth.Login(context.Background(), LoginInput{Email: user.Email, Password: "Str0ng-password!"}, ClientInfo{})
	if err != nil {
		t.Fatal(err)
	}
	oldClaims, _ := auth.tokens.ParseAccess(login.AccessToken)

	refreshed, err := auth.Refresh(context.Background(), login.RefreshToken, ClientInfo{})
	if err != nil {
		t.Fatal(err)
	}
	newClaims, err := auth.tokens.ParseAccess(refreshed.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := repo.RefreshTokenByHash(context.Background(), HashToken(refreshed.RefreshToken))
	if err != nil {
		t.Fatal(err)
	}
	if newClaims.SessionID != replacement.ID {
		t.Fatalf("access sid = %q, want replacement session %q", newClaims.SessionID, replacement.ID)
	}
	if _, err := auth.AuthorizeSession(context.Background(), user.ID, newClaims.SessionID); err != nil {
		t.Fatalf("refreshed session is not valid: %v", err)
	}
	if _, err := auth.AuthorizeSession(context.Background(), user.ID, oldClaims.SessionID); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("the rotated-out session error = %v, want unauthorized", err)
	}
}

func TestChangePasswordWithoutCookieRevokesEverySession(t *testing.T) {
	now := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	auth := newAuthServiceWithResetForTest(t, repo, now, nil, "https://necly-service.my.id")
	user := seededVerifiedUser(t, auth, repo, now)

	phone, err := auth.Login(context.Background(), LoginInput{Email: user.Email, Password: "Str0ng-password!"}, ClientInfo{})
	if err != nil {
		t.Fatal(err)
	}
	laptop, err := auth.Login(context.Background(), LoginInput{Email: user.Email, Password: "Str0ng-password!"}, ClientInfo{})
	if err != nil {
		t.Fatal(err)
	}

	// No refresh cookie and no session id: every live sign-in must die.
	if err := auth.ChangePassword(context.Background(), user.ID, "Str0ng-password!", "N3w-strong-password!", "", "", ClientInfo{}); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}
	for name, raw := range map[string]string{"phone": phone.RefreshToken, "laptop": laptop.RefreshToken} {
		if _, err := auth.Refresh(context.Background(), raw, ClientInfo{}); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("%s session survived a cookie-less password change: error = %v", name, err)
		}
	}
	if _, err := auth.Login(context.Background(), LoginInput{Email: user.Email, Password: "N3w-strong-password!"}, ClientInfo{}); err != nil {
		t.Fatalf("login with the new password error = %v", err)
	}
}

func TestGoogleLoginAppliesAccountEligibilityBeforeLinking(t *testing.T) {
	now := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	auth := newAuthServiceWithResetForTest(t, repo, now, nil, "https://necly-service.my.id")
	user := seededVerifiedUser(t, auth, repo, now)

	user.Active = false
	if _, err := auth.LoginGoogleIdentity(context.Background(), user.Email, user.Name, "google-sub-1", ClientInfo{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("disabled Google account error = %v, want unauthorized", err)
	}
	if user.GoogleSubject != nil {
		t.Fatal("a disabled account must not be linked to a Google subject")
	}

	user.Active = true
	deleted := now
	user.DeletedAt = &deleted
	if _, err := auth.LoginGoogleIdentity(context.Background(), user.Email, user.Name, "google-sub-1", ClientInfo{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("deleted Google account error = %v, want unauthorized", err)
	}
	if user.GoogleSubject != nil {
		t.Fatal("a deleted account must not be linked to a Google subject")
	}

	user.DeletedAt = nil
	session, err := auth.LoginGoogleIdentity(context.Background(), user.Email, user.Name, "google-sub-1", ClientInfo{})
	if err != nil {
		t.Fatalf("eligible Google account error = %v", err)
	}
	if session.AccessToken == "" || session.RefreshToken == "" {
		t.Fatal("eligible Google login did not issue a session")
	}
	if user.GoogleSubject == nil || *user.GoogleSubject != "google-sub-1" {
		t.Fatal("eligible Google login did not link the subject")
	}
}

func TestProductionMailFailClosedOnRegistrationAndReset(t *testing.T) {
	now := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	tokens, err := NewTokenManager("auth-test-secret-that-is-definitely-32-bytes", "necly-test", 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	auth := NewAuthService(repo, NewPasswordHasher(4), tokens, AuthSettings{Production: true})
	auth.now = func() time.Time { return now }

	result, err := auth.Register(context.Background(), RegisterInput{Name: "Alice", Email: "alice@example.com", Password: "Str0ng-password!"})
	if !errors.Is(err, ErrMailUnavailable) {
		t.Fatalf("Register() error = %v, want ErrMailUnavailable", err)
	}
	if result != nil {
		t.Fatalf("Register() returned %#v, want no result and no token", result)
	}
	if len(repo.usersByEmail) != 0 {
		t.Fatal("a production registration without mail must not create an account")
	}

	user := &domain.User{ID: "user-1", Email: "alice@example.com", Name: "Alice", Role: domain.RoleCustomer, Active: true}
	repo.usersByEmail[user.Email], repo.usersByID[user.ID] = user, user
	if err := auth.ForgotPassword(context.Background(), user.Email); !errors.Is(err, ErrMailUnavailable) {
		t.Fatalf("ForgotPassword() error = %v, want ErrMailUnavailable", err)
	}
	if len(repo.reset) != 0 {
		t.Fatal("a production reset without mail must not mint a token")
	}
	if err := auth.ResendVerification(context.Background(), user.Email); !errors.Is(err, ErrMailUnavailable) {
		t.Fatalf("ResendVerification() error = %v, want ErrMailUnavailable", err)
	}
	if len(repo.verification) != 0 {
		t.Fatal("a production resend without mail must not mint a token")
	}

	// With working mail the same flows are allowed, and the raw verification
	// token is still never returned in production.
	auth.mail = &fakeMail{enabled: true}
	result, err = auth.Register(context.Background(), RegisterInput{Name: "Budi", Email: "budi@example.com", Password: "Str0ng-password!"})
	if err != nil {
		t.Fatalf("Register() with mail error = %v", err)
	}
	if result.VerificationToken != "" {
		t.Fatal("production registration returned the raw verification token")
	}
}
