package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/necly/services/internal/domain"
)

type fakeAuthRepository struct {
	usersByEmail map[string]*domain.User
	usersByID    map[string]*domain.User
	refresh      map[string]*domain.RefreshToken
	verification map[string]*domain.EmailVerificationToken
}

func newFakeAuthRepository() *fakeAuthRepository {
	return &fakeAuthRepository{
		usersByEmail: map[string]*domain.User{}, usersByID: map[string]*domain.User{},
		refresh: map[string]*domain.RefreshToken{}, verification: map[string]*domain.EmailVerificationToken{},
	}
}

func (r *fakeAuthRepository) CreateUser(_ context.Context, user *domain.User, token *domain.EmailVerificationToken) error {
	if _, exists := r.usersByEmail[user.Email]; exists {
		return ErrConflict
	}
	copyUser := *user
	copyToken := *token
	r.usersByEmail[user.Email] = &copyUser
	r.usersByID[user.ID] = &copyUser
	r.verification[token.TokenHash] = &copyToken
	return nil
}
func (r *fakeAuthRepository) UserByEmail(_ context.Context, email string) (*domain.User, error) {
	user, ok := r.usersByEmail[email]
	if !ok {
		return nil, ErrNotFound
	}
	copyUser := *user
	return &copyUser, nil
}
func (r *fakeAuthRepository) UserByID(_ context.Context, id string) (*domain.User, error) {
	user, ok := r.usersByID[id]
	if !ok {
		return nil, ErrNotFound
	}
	copyUser := *user
	return &copyUser, nil
}
func (r *fakeAuthRepository) SaveRefreshToken(_ context.Context, token *domain.RefreshToken) error {
	copyToken := *token
	r.refresh[token.TokenHash] = &copyToken
	return nil
}
func (r *fakeAuthRepository) RefreshTokenByHash(_ context.Context, hash string) (*domain.RefreshToken, error) {
	token, ok := r.refresh[hash]
	if !ok {
		return nil, ErrNotFound
	}
	copyToken := *token
	return &copyToken, nil
}
func (r *fakeAuthRepository) RotateRefreshToken(_ context.Context, oldHash string, replacement *domain.RefreshToken, now time.Time) error {
	old, ok := r.refresh[oldHash]
	if !ok || old.RevokedAt != nil || !old.ExpiresAt.After(now) {
		return ErrUnauthorized
	}
	old.RevokedAt = &now
	copyReplacement := *replacement
	r.refresh[replacement.TokenHash] = &copyReplacement
	return nil
}
func (r *fakeAuthRepository) RevokeRefreshToken(_ context.Context, hash string, now time.Time) error {
	token, ok := r.refresh[hash]
	if !ok {
		return nil
	}
	token.RevokedAt = &now
	return nil
}
func (r *fakeAuthRepository) ConsumeVerificationToken(_ context.Context, hash string, now time.Time) (*domain.User, error) {
	token, ok := r.verification[hash]
	if !ok || token.UsedAt != nil || !token.ExpiresAt.After(now) {
		return nil, ErrUnauthorized
	}
	token.UsedAt = &now
	user := r.usersByID[token.UserID]
	user.EmailVerifiedAt = &now
	copyUser := *user
	return &copyUser, nil
}
func (r *fakeAuthRepository) UpsertGoogleUser(_ context.Context, email, name, subject string, now time.Time) (*domain.User, error) {
	if user, ok := r.usersByEmail[email]; ok {
		user.GoogleSubject = &subject
		user.EmailVerifiedAt = &now
		copyUser := *user
		return &copyUser, nil
	}
	user := &domain.User{ID: "google-user", Email: email, Name: name, Role: domain.RoleCustomer, GoogleSubject: &subject, EmailVerifiedAt: &now}
	r.usersByEmail[email], r.usersByID[user.ID] = user, user
	copyUser := *user
	return &copyUser, nil
}

func newAuthServiceForTest(t *testing.T, repo *fakeAuthRepository, now time.Time) *AuthService {
	t.Helper()
	tokens, err := NewTokenManager("auth-test-secret-that-is-definitely-32-bytes", "necly-test", 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tokens.now = func() time.Time { return now }
	service := NewAuthService(repo, NewPasswordHasher(4), tokens, 24*time.Hour, nil)
	service.now = func() time.Time { return now }
	return service
}

func TestAuthRegisterNormalizesEmailHashesPasswordAndVerificationToken(t *testing.T) {
	now := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	repo := newFakeAuthRepository()
	auth := newAuthServiceForTest(t, repo, now)
	result, err := auth.Register(context.Background(), RegisterInput{Name: " Alice ", Email: " ALICE@Example.COM ", Password: "Str0ng-password!"})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	stored := repo.usersByEmail["alice@example.com"]
	if stored == nil || stored.PasswordHash == "Str0ng-password!" || !auth.hasher.Verify(stored.PasswordHash, "Str0ng-password!") {
		t.Fatal("password was not safely hashed")
	}
	if result.VerificationToken == "" || repo.verification[HashToken(result.VerificationToken)] == nil {
		t.Fatal("verification token was not returned raw and persisted hashed")
	}
	if stored.Name != "Alice" || stored.Role != domain.RoleCustomer {
		t.Fatalf("stored user = %#v", stored)
	}
}

func TestAuthLoginRequiresVerifiedEmailAndPersistsHashedRefresh(t *testing.T) {
	now := time.Now().UTC()
	repo := newFakeAuthRepository()
	auth := newAuthServiceForTest(t, repo, now)
	_, _ = auth.Register(context.Background(), RegisterInput{Name: "Alice", Email: "alice@example.com", Password: "Str0ng-password!"})
	if _, err := auth.Login(context.Background(), LoginInput{Email: "alice@example.com", Password: "Str0ng-password!"}, ClientInfo{}); !errors.Is(err, ErrEmailNotVerified) {
		t.Fatalf("Login() error = %v, want ErrEmailNotVerified", err)
	}
	verified := now
	repo.usersByEmail["alice@example.com"].EmailVerifiedAt = &verified
	session, err := auth.Login(context.Background(), LoginInput{Email: "alice@example.com", Password: "Str0ng-password!"}, ClientInfo{IPAddress: "127.0.0.1"})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if session.AccessToken == "" || session.RefreshToken == "" {
		t.Fatal("tokens were not issued")
	}
	if _, plaintextStored := repo.refresh[session.RefreshToken]; plaintextStored {
		t.Fatal("refresh token persisted in plaintext")
	}
	if repo.refresh[HashToken(session.RefreshToken)] == nil {
		t.Fatal("hashed refresh token was not persisted")
	}
}

func TestAuthRefreshRotatesWhitelistTokenAndRejectsReplay(t *testing.T) {
	now := time.Now().UTC()
	repo := newFakeAuthRepository()
	auth := newAuthServiceForTest(t, repo, now)
	hash, _ := auth.hasher.Hash("Str0ng-password!")
	verified := now
	user := &domain.User{ID: "user-1", Email: "alice@example.com", Name: "Alice", PasswordHash: hash, Role: domain.RoleCustomer, EmailVerifiedAt: &verified}
	repo.usersByEmail[user.Email], repo.usersByID[user.ID] = user, user
	initial, err := auth.Login(context.Background(), LoginInput{Email: user.Email, Password: "Str0ng-password!"}, ClientInfo{})
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := auth.Refresh(context.Background(), initial.RefreshToken, ClientInfo{})
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if replacement.RefreshToken == initial.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}
	if _, err := auth.Refresh(context.Background(), initial.RefreshToken, ClientInfo{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("replayed refresh error = %v, want unauthorized", err)
	}
}
