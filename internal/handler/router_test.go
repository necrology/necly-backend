package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/handler"
	"github.com/necly/services/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type dummyAuthRepo struct{ mock.Mock }

func (m *dummyAuthRepo) CreateUser(ctx context.Context, user *domain.User, verification *domain.EmailVerificationToken) error {
	return m.Called(ctx, user, verification).Error(0)
}
func (m *dummyAuthRepo) UserByEmail(ctx context.Context, email string) (*domain.User, error) {
	args := m.Called(ctx, email)
	if u := args.Get(0); u != nil {
		return u.(*domain.User), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *dummyAuthRepo) UserByID(ctx context.Context, id string) (*domain.User, error) {
	args := m.Called(ctx, id)
	if u := args.Get(0); u != nil {
		return u.(*domain.User), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *dummyAuthRepo) SaveRefreshToken(ctx context.Context, token *domain.RefreshToken) error {
	return m.Called(ctx, token).Error(0)
}
func (m *dummyAuthRepo) RefreshTokenByHash(ctx context.Context, hash string) (*domain.RefreshToken, error) {
	args := m.Called(ctx, hash)
	if r := args.Get(0); r != nil {
		return r.(*domain.RefreshToken), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *dummyAuthRepo) RotateRefreshToken(ctx context.Context, oldHash string, replacement *domain.RefreshToken, now time.Time) error {
	return m.Called(ctx, oldHash, replacement, now).Error(0)
}
func (m *dummyAuthRepo) RevokeRefreshToken(ctx context.Context, hash string, now time.Time) error {
	return m.Called(ctx, hash, now).Error(0)
}
func (m *dummyAuthRepo) ConsumeVerificationToken(ctx context.Context, hash string, now time.Time) (*domain.User, error) {
	args := m.Called(ctx, hash, now)
	if u := args.Get(0); u != nil {
		return u.(*domain.User), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *dummyAuthRepo) UpsertGoogleUser(ctx context.Context, email, name, subject string, now time.Time) (*domain.User, error) {
	args := m.Called(ctx, email, name, subject, now)
	if u := args.Get(0); u != nil {
		return u.(*domain.User), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *dummyAuthRepo) CreatePasswordResetToken(ctx context.Context, token *domain.PasswordResetToken) error {
	return m.Called(ctx, token).Error(0)
}
func (m *dummyAuthRepo) ConsumePasswordResetToken(ctx context.Context, hash, passwordHash string, now time.Time) (*domain.User, error) {
	args := m.Called(ctx, hash, passwordHash, now)
	if u := args.Get(0); u != nil {
		return u.(*domain.User), args.Error(1)
	}
	return nil, args.Error(1)
}

func setupTestApp(t *testing.T) (*fiber.App, *service.TokenManager, *dummyAuthRepo) {
	app := fiber.New()
	tokens, err := service.NewTokenManager("test-jwt-secret-that-has-at-least-32-bytes", "necly", 15*time.Minute, 24*time.Hour)
	assert.NoError(t, err)
	hasher := service.NewPasswordHasher(4)
	authRepo := new(dummyAuthRepo)
	authSvc := service.NewAuthService(authRepo, hasher, tokens, service.AuthSettings{VerificationTTL: 24 * time.Hour})

	router := handler.NewRouter(
		authSvc,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		tokens,
		nil,
		nil,
		nil,
		nil,
	)
	router.Register(app)
	return app, tokens, authRepo
}

func TestAuthMiddleware_LocalizedMissingHeader(t *testing.T) {
	app, _, _ := setupTestApp(t)

	req := httptest.NewRequest("GET", "/api/v1/me", nil)
	resp, err := app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)

	var payload map[string]string
	err = json.Unmarshal(body, &payload)
	assert.NoError(t, err)
	assert.Equal(t, "Header otorisasi tidak ditemukan", payload["error"])
}

func TestAuthMiddleware_LocalizedInvalidFormat(t *testing.T) {
	app, _, _ := setupTestApp(t)

	req := httptest.NewRequest("GET", "/api/v1/me", nil)
	req.Header.Set("Authorization", "Token abc")
	resp, err := app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	var payload map[string]string
	_ = json.Unmarshal(body, &payload)
	assert.Equal(t, "Format otorisasi tidak valid", payload["error"])
}

func TestAuthMiddleware_LocalizedExpiredToken(t *testing.T) {
	app, _, _ := setupTestApp(t)

	req := httptest.NewRequest("GET", "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	resp, err := app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	var payload map[string]string
	_ = json.Unmarshal(body, &payload)
	assert.Equal(t, "Token tidak valid atau telah kedaluwarsa", payload["error"])
}

func TestRequireRole_LocalizedInsufficientPermissions(t *testing.T) {
	app, tokens, _ := setupTestApp(t)
	token, err := tokens.IssueAccess("user-1", string(domain.RoleCustomer))
	assert.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/v1/admin/dashboard", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusForbidden, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	var payload map[string]string
	_ = json.Unmarshal(body, &payload)
	assert.Equal(t, "Akses ditolak: Anda tidak memiliki izin untuk tindakan ini", payload["error"])
}

func TestAuthHandlers_LocalizedRegisterAndLoginErrors(t *testing.T) {
	app, _, authRepo := setupTestApp(t)

	// 1. Invalid body on register
	req := httptest.NewRequest("POST", "/api/v1/auth/register", bytes.NewReader([]byte("{invalid-json")))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusBadRequest, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	var payload map[string]string
	_ = json.Unmarshal(body, &payload)
	assert.Equal(t, "Format data permintaan tidak valid", payload["error"])

	// 2. Duplicate email on register
	authRepo.On("CreateUser", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("duplicate key error: email conflict")).Once()
	regBody, _ := json.Marshal(map[string]string{
		"name":     "Budi",
		"email":    "budi@example.com",
		"password": "Password123!",
	})
	req = httptest.NewRequest("POST", "/api/v1/auth/register", bytes.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusConflict, resp.StatusCode)
	body, _ = io.ReadAll(resp.Body)
	_ = json.Unmarshal(body, &payload)
	assert.Equal(t, "Email sudah terdaftar", payload["error"])

	// 3. Missing refresh token
	req = httptest.NewRequest("POST", "/api/v1/auth/refresh", nil)
	resp, err = app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)
	body, _ = io.ReadAll(resp.Body)
	_ = json.Unmarshal(body, &payload)
	assert.Equal(t, "Token penyegar tidak ditemukan", payload["error"])

	// 4. Logout success message
	req = httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	resp, err = app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
	body, _ = io.ReadAll(resp.Body)
	_ = json.Unmarshal(body, &payload)
	assert.Equal(t, "Berhasil keluar", payload["message"])
}
