package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/necly/services/internal/domain"
)

func tokenFromVerificationMail(t *testing.T, body string) string {
	t.Helper()
	const prefix = "/verify-email?token="
	index := strings.Index(body, prefix)
	if index < 0 {
		t.Fatalf("no activation link in mail body: %q", body)
	}
	return strings.SplitN(body[index+len(prefix):], "\n", 2)[0]
}

func newUnverifiedUser(t *testing.T, auth *AuthService, repo *fakeAuthRepository, now time.Time) *domain.User {
	t.Helper()
	hash, err := auth.hasher.Hash("Str0ng-password!")
	if err != nil {
		t.Fatal(err)
	}
	user := &domain.User{ID: "user-2", Email: "bob@example.com", Name: "Bob", PasswordHash: hash, Role: domain.RoleCustomer, Active: true}
	repo.usersByEmail[user.Email], repo.usersByID[user.ID] = user, user
	return user
}

// Registration used to hand the activation token back to the browser without
// ever mailing it, so a closed tab meant a permanently unusable account: Login
// refuses anyone whose EmailVerifiedAt is still nil.
func TestRegisterMailsTheActivationLink(t *testing.T) {
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	mail := &fakeMail{enabled: true}
	auth := newAuthServiceWithResetForTest(t, repo, now, mail, "https://necly-service.my.id")

	result, err := auth.Register(context.Background(), RegisterInput{Name: "Bob", Email: "BOB@Example.com ", Password: "Str0ng-password!"})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if mail.sentCount() != 1 {
		t.Fatalf("sent emails = %d, want 1", mail.sentCount())
	}

	message := mail.sent[0]
	if message.To != "bob@example.com" {
		t.Errorf("activation email went to %q", message.To)
	}
	if !strings.Contains(message.Body, "https://necly-service.my.id/verify-email?token=") {
		t.Fatalf("activation link is missing or malformed: %q", message.Body)
	}

	if result.VerificationToken != "" {
		t.Fatal("the activation token must not be handed back to the caller while mail delivery is on")
	}
	rawToken := tokenFromVerificationMail(t, message.Body)
	if _, ok := repo.verification[rawToken]; ok {
		t.Fatal("the raw activation token was stored instead of its hash")
	}
	if _, ok := repo.verification[HashToken(rawToken)]; !ok {
		t.Fatal("the hashed activation token was not stored")
	}

	// And the mailed link actually activates the account.
	if _, err := auth.VerifyEmail(context.Background(), rawToken); err != nil {
		t.Fatalf("VerifyEmail() error = %v", err)
	}
	if _, err := auth.Login(context.Background(), LoginInput{Email: "bob@example.com", Password: "Str0ng-password!"}, ClientInfo{}); err != nil {
		t.Fatalf("login after activation error = %v", err)
	}
}

func TestResendVerificationRetiresThePreviousLink(t *testing.T) {
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	mail := &fakeMail{enabled: true}
	auth := newAuthServiceWithResetForTest(t, repo, now, mail, "https://necly-service.my.id")
	result, err := auth.Register(context.Background(), RegisterInput{Name: "Bob", Email: "bob@example.com", Password: "Str0ng-password!"})
	if err != nil {
		t.Fatal(err)
	}
	firstRaw := result.VerificationToken

	later := now.Add(5 * time.Minute)
	resending := newAuthServiceWithResetForTest(t, repo, later, mail, "https://necly-service.my.id")
	if err := resending.ResendVerification(context.Background(), " bob@example.com "); err != nil {
		t.Fatalf("ResendVerification() error = %v", err)
	}
	if mail.sentCount() != 2 {
		t.Fatalf("sent emails = %d, want 2", mail.sentCount())
	}
	secondRaw := tokenFromVerificationMail(t, mail.sent[1].Body)
	if secondRaw == firstRaw {
		t.Fatal("the resend issued the same token again")
	}

	// Only one link may work at a time, and it must be the newest one.
	if _, err := resending.VerifyEmail(context.Background(), firstRaw); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("the superseded link error = %v, want unauthorized", err)
	}
	if _, err := resending.VerifyEmail(context.Background(), secondRaw); err != nil {
		t.Fatalf("the newest link error = %v, want nil", err)
	}
}

func TestResendVerificationRejectsVerifiedUnknownAndInvalidAddresses(t *testing.T) {
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	mail := &fakeMail{enabled: true}
	auth := newAuthServiceWithResetForTest(t, repo, now, mail, "https://necly-service.my.id")
	seededVerifiedUser(t, auth, repo, now)

	if err := auth.ResendVerification(context.Background(), "alice@example.com"); !errors.Is(err, ErrAlreadyVerified) {
		t.Fatalf("verified address error = %v, want ErrAlreadyVerified", err)
	}
	if err := auth.ResendVerification(context.Background(), "nobody@example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown address error = %v, want ErrNotFound", err)
	}
	if err := auth.ResendVerification(context.Background(), "bukan-email"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("malformed address error = %v, want ErrInvalidInput", err)
	}
	if mail.sentCount() != 0 {
		t.Fatalf("sent emails = %d, want 0", mail.sentCount())
	}
}

// Without a configured mailer the link has to be recoverable from the log-only
// sender, so the token must still be minted.
func TestResendVerificationWorksWithoutAConfiguredMailer(t *testing.T) {
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	auth := newAuthServiceWithResetForTest(t, repo, now, nil, "https://necly-service.my.id")
	newUnverifiedUser(t, auth, repo, now)

	if err := auth.ResendVerification(context.Background(), "bob@example.com"); err != nil {
		t.Fatalf("ResendVerification() error = %v, want nil", err)
	}
	if len(repo.verification) != 1 {
		t.Fatalf("stored activation tokens = %d, want 1", len(repo.verification))
	}
}

// A wrong mailbox must not be verifiable by the person who typed it: with mail
// delivery off (local or log-only setups) the token is still returned so the
// operator can activate the account by hand.
func TestRegisterReturnsTheTokenOnlyWithoutMailDelivery(t *testing.T) {
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	auth := newAuthServiceWithResetForTest(t, repo, now, nil, "https://necly-service.my.id")

	result, err := auth.Register(context.Background(), RegisterInput{Name: "Bob", Email: "bob@example.com", Password: "Str0ng-password!"})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if result.VerificationToken == "" {
		t.Fatal("without a mailer the token has to be returned, otherwise the account cannot be activated")
	}
	if _, err := auth.VerifyEmail(context.Background(), result.VerificationToken); err != nil {
		t.Fatalf("VerifyEmail() error = %v", err)
	}
}

// Login and Refresh used to ignore the account flags entirely, so a customer an
// admin had deactivated or deleted kept signing in and kept renewing the session.
func TestDeactivatedAndDeletedUsersCannotAuthenticate(t *testing.T) {
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	auth := newAuthServiceWithResetForTest(t, repo, now, nil, "https://necly-service.my.id")
	user := seededVerifiedUser(t, auth, repo, now)

	session, err := auth.Login(context.Background(), LoginInput{Email: user.Email, Password: "Str0ng-password!"}, ClientInfo{})
	if err != nil {
		t.Fatalf("baseline login error = %v", err)
	}

	user.Active = false
	if _, err := auth.Login(context.Background(), LoginInput{Email: user.Email, Password: "Str0ng-password!"}, ClientInfo{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("deactivated login error = %v, want unauthorized", err)
	}
	if _, err := auth.Refresh(context.Background(), session.RefreshToken, ClientInfo{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("deactivated refresh error = %v, want unauthorized", err)
	}

	user.Active = true
	deleted := now
	user.DeletedAt = &deleted
	if _, err := auth.Login(context.Background(), LoginInput{Email: user.Email, Password: "Str0ng-password!"}, ClientInfo{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("deleted login error = %v, want unauthorized", err)
	}
}

// Hashes written with the old bcrypt cost have to be upgraded on the next
// successful sign-in, otherwise the cost increase only ever protects new accounts.
func TestLoginUpgradesAWeakPasswordHash(t *testing.T) {
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	weak := NewPasswordHasher(4)
	strong := NewPasswordHasher(BcryptCost)
	weakHash, err := weak.Hash("Str0ng-password!")
	if err != nil {
		t.Fatal(err)
	}
	verified := now
	user := &domain.User{ID: "user-3", Email: "carol@example.com", Name: "Carol", PasswordHash: weakHash, Role: domain.RoleCustomer, Active: true, EmailVerifiedAt: &verified}
	repo.usersByEmail[user.Email], repo.usersByID[user.ID] = user, user

	auth := newAuthServiceWithResetForTest(t, repo, now, nil, "https://necly-service.my.id")
	auth.hasher = strong

	if _, err := auth.Login(context.Background(), LoginInput{Email: "carol@example.com", Password: "Str0ng-password!"}, ClientInfo{}); err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	stored := repo.usersByID["user-3"].PasswordHash
	if weak.NeedsRehash(stored) {
		t.Fatalf("the stored hash still uses the old cost: %q", stored)
	}
	if !strong.Verify(stored, "Str0ng-password!") {
		t.Fatal("the upgraded hash does not verify against the original password")
	}
}
