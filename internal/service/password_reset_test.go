package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/necly/services/internal/domain"
)

func newAuthServiceWithResetForTest(t *testing.T, repo *fakeAuthRepository, now time.Time, mail MailSender, frontendURL string) *AuthService {
	t.Helper()
	tokens, err := NewTokenManager("auth-test-secret-that-is-definitely-32-bytes", "necly-test", 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tokens.now = func() time.Time { return now }
	service := NewAuthService(repo, NewPasswordHasher(4), tokens, AuthSettings{
		VerificationTTL:  24 * time.Hour,
		PasswordResetTTL: 45 * time.Minute,
		FrontendBaseURL:  frontendURL,
		Mailer:           mail,
	})
	service.now = func() time.Time { return now }
	return service
}

func seededVerifiedUser(t *testing.T, auth *AuthService, repo *fakeAuthRepository, now time.Time) *domain.User {
	t.Helper()
	hash, err := auth.hasher.Hash("Str0ng-password!")
	if err != nil {
		t.Fatal(err)
	}
	verified := now
	user := &domain.User{ID: "user-1", Email: "alice@example.com", Name: "Alice", PasswordHash: hash, Role: domain.RoleCustomer, Active: true, EmailVerifiedAt: &verified}
	repo.usersByEmail[user.Email], repo.usersByID[user.ID] = user, user
	return user
}

func tokenFromMail(t *testing.T, body string) string {
	t.Helper()
	const prefix = "/reset-password?token="
	index := strings.Index(body, prefix)
	if index < 0 {
		t.Fatalf("no reset link in mail body: %q", body)
	}
	return strings.SplitN(body[index+len(prefix):], "\n", 2)[0]
}

func TestForgotPasswordReportsUnknownEmail(t *testing.T) {
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	mail := &fakeMail{enabled: true}
	auth := newAuthServiceWithResetForTest(t, repo, now, mail, "https://necly-service.my.id")

	if err := auth.ForgotPassword(context.Background(), "nobody@example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ForgotPassword() error = %v, want ErrNotFound", err)
	}
	if len(repo.reset) != 0 {
		t.Fatal("no reset token may be issued for an unknown address")
	}
	if mail.sentCount() != 0 {
		t.Fatal("no email may be sent for an unknown address")
	}
}

func TestForgotPasswordStoresHashedTokenAndMailsTheRawLink(t *testing.T) {
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	mail := &fakeMail{enabled: true}
	// The trailing slash is deliberate: the link must not end up with a double
	// slash when the operator configures the origin that way.
	auth := newAuthServiceWithResetForTest(t, repo, now, mail, "https://necly-service.my.id/")
	seededVerifiedUser(t, auth, repo, now)

	if err := auth.ForgotPassword(context.Background(), " ALICE@Example.com "); err != nil {
		t.Fatalf("ForgotPassword() error = %v", err)
	}
	if len(repo.reset) != 1 {
		t.Fatalf("stored reset tokens = %d, want 1", len(repo.reset))
	}
	if mail.sentCount() != 1 {
		t.Fatalf("sent emails = %d, want 1", mail.sentCount())
	}

	message := mail.sent[0]
	if message.To != "alice@example.com" {
		t.Errorf("email went to %q", message.To)
	}
	if !strings.Contains(message.Body, "https://necly-service.my.id/reset-password?token=") {
		t.Fatalf("reset link is missing or malformed: %q", message.Body)
	}

	// The database holds only the hash, so the token stored under reset[] must
	// be the hash of what the mail carries - never the raw value itself.
	rawToken := tokenFromMail(t, message.Body)
	if rawToken == "" {
		t.Fatal("reset link carried no token")
	}
	if _, ok := repo.reset[rawToken]; ok {
		t.Fatal("the raw reset token was stored instead of its hash")
	}
	if _, ok := repo.reset[HashToken(rawToken)]; !ok {
		t.Fatal("the hashed reset token was not stored")
	}
}

func TestResetPasswordAppliesNewPasswordAndRevokesSessions(t *testing.T) {
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	mail := &fakeMail{enabled: true}
	auth := newAuthServiceWithResetForTest(t, repo, now, mail, "https://necly-service.my.id")
	seededVerifiedUser(t, auth, repo, now)

	first, err := auth.Login(context.Background(), LoginInput{Email: "alice@example.com", Password: "Str0ng-password!"}, ClientInfo{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := auth.Login(context.Background(), LoginInput{Email: "alice@example.com", Password: "Str0ng-password!"}, ClientInfo{})
	if err != nil {
		t.Fatal(err)
	}

	if err := auth.ForgotPassword(context.Background(), "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	rawToken := tokenFromMail(t, mail.sent[0].Body)

	updated, err := auth.ResetPassword(context.Background(), rawToken, "N3w-strong-password!")
	if err != nil {
		t.Fatalf("ResetPassword() error = %v", err)
	}
	if !auth.hasher.Verify(updated.PasswordHash, "N3w-strong-password!") {
		t.Fatal("the new password was not stored")
	}
	if _, err := auth.Login(context.Background(), LoginInput{Email: "alice@example.com", Password: "Str0ng-password!"}, ClientInfo{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("login with the old password error = %v, want unauthorized", err)
	}
	if _, err := auth.Login(context.Background(), LoginInput{Email: "alice@example.com", Password: "N3w-strong-password!"}, ClientInfo{}); err != nil {
		t.Fatalf("login with the new password error = %v", err)
	}

	// A reset is a "someone may have had my password" event, so every session
	// that existed before it must be dead.
	for label, session := range map[string]string{"first": first.RefreshToken, "second": second.RefreshToken} {
		if _, err := auth.Refresh(context.Background(), session, ClientInfo{}); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("%s session survived the reset: error = %v, want unauthorized", label, err)
		}
	}
}

func TestResetPasswordRejectsReusedExpiredAndShortCredentials(t *testing.T) {
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	mail := &fakeMail{enabled: true}
	auth := newAuthServiceWithResetForTest(t, repo, now, mail, "https://necly-service.my.id")
	seededVerifiedUser(t, auth, repo, now)

	if err := auth.ForgotPassword(context.Background(), "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	rawToken := tokenFromMail(t, mail.sent[0].Body)

	if _, err := auth.ResetPassword(context.Background(), rawToken, "short"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("short password error = %v, want invalid input", err)
	}
	// The rejected attempt must not have burned the token.
	if _, err := auth.ResetPassword(context.Background(), rawToken, "N3w-strong-password!"); err != nil {
		t.Fatalf("ResetPassword() error = %v", err)
	}
	if _, err := auth.ResetPassword(context.Background(), rawToken, "An0ther-strong-password!"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("reused token error = %v, want unauthorized", err)
	}

	later := now.Add(2 * time.Hour)
	expiring := newAuthServiceWithResetForTest(t, repo, later, mail, "https://necly-service.my.id")
	if err := expiring.ForgotPassword(context.Background(), "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	freshToken := tokenFromMail(t, mail.sent[1].Body)

	// 45-minute TTL, so two hours later the link is dead.
	afterExpiry := newAuthServiceWithResetForTest(t, repo, later.Add(2*time.Hour), mail, "https://necly-service.my.id")
	if _, err := afterExpiry.ResetPassword(context.Background(), freshToken, "N3w-strong-password!"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired token error = %v, want unauthorized", err)
	}
}

// Without a configured mailer the flow must still issue a token, so the
// operator can recover the link from the log-only sender.
func TestForgotPasswordWorksWithoutAConfiguredMailer(t *testing.T) {
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	auth := newAuthServiceWithResetForTest(t, repo, now, nil, "https://necly-service.my.id")
	seededVerifiedUser(t, auth, repo, now)

	if err := auth.ForgotPassword(context.Background(), "alice@example.com"); err != nil {
		t.Fatalf("ForgotPassword() error = %v, want nil", err)
	}
	if len(repo.reset) != 1 {
		t.Fatalf("stored reset tokens = %d, want 1", len(repo.reset))
	}
}
