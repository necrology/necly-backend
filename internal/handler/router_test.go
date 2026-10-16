package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
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
func (m *dummyAuthRepo) CreateVerificationToken(ctx context.Context, token *domain.EmailVerificationToken, now time.Time) error {
	return m.Called(ctx, token, now).Error(0)
}
func (m *dummyAuthRepo) UpdatePasswordHash(ctx context.Context, userID, hash string, now time.Time) error {
	return m.Called(ctx, userID, hash, now).Error(0)
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
	// Without this a handler reached with a nil service would panic the test
	// process instead of answering, which hides whether the role gate let it past.
	app.Use(recover.New())
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

	// 2. Duplicate email on register. The store translates the pq unique violation
	// into service.ErrConflict, so the handler must match on the sentinel.
	authRepo.On("CreateUser", mock.Anything, mock.Anything, mock.Anything).Return(service.ErrConflict).Once()
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

// The role gate compared an `any` holding a string against a domain.UserRole,
// which never matches, so every /admin route answered 403 — including for a
// genuine admin. The existing test only covered the deny case, which is why the
// bug survived.
func TestRequireRole_AdmitsAnAdminToken(t *testing.T) {
	app, tokens, _ := setupTestApp(t)
	token, err := tokens.IssueAccess("admin-1", string(domain.RoleAdmin))
	assert.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/v1/admin/dashboard", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := app.Test(req)
	assert.NoError(t, err)
	// The dashboard service is nil in this harness, so anything past the gate is
	// a 5xx; what matters is that it is no longer rejected on the role.
	assert.NotEqual(t, fiber.StatusForbidden, resp.StatusCode, "an admin token must get past requireRole")
}

func TestRequireRole_StillDeniesAnonymous(t *testing.T) {
	app, _, _ := setupTestApp(t)

	req := httptest.NewRequest("GET", "/api/v1/admin/dashboard", nil)
	resp, err := app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)
}

func (m *dummyAuthRepo) ListRefreshTokens(ctx context.Context, userID string, now time.Time) ([]domain.RefreshToken, error) {
	args := m.Called(ctx, userID, now)
	if v := args.Get(0); v != nil {
		return v.([]domain.RefreshToken), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *dummyAuthRepo) RevokeRefreshTokenByID(ctx context.Context, userID, tokenID string, now time.Time) error {
	return m.Called(ctx, userID, tokenID, now).Error(0)
}
func (m *dummyAuthRepo) RevokeUserRefreshTokens(ctx context.Context, userID, exceptHash string, now time.Time) error {
	return m.Called(ctx, userID, exceptHash, now).Error(0)
}

// The storefront sends the idempotency key as an Idempotency-Key header, and no
// code ever read it into the service input. The field stayed empty, so the
// service's first validation rejected every checkout with a 400 — checkout had
// never worked, since the first commit.
func TestCheckoutReadsTheIdempotencyKeyFromTheHeader(t *testing.T) {
	app, tokens, _ := setupTestApp(t)
	token, err := tokens.IssueAccess("user-1", string(domain.RoleCustomer))
	assert.NoError(t, err)
	body := `{"items":[{"product_price_id":"price-1","quantity":1,"device_count":1}]}`

	post := func(headers map[string]string) int {
		req := httptest.NewRequest("POST", "/api/v1/checkout", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		resp, err := app.Test(req)
		assert.NoError(t, err)
		return resp.StatusCode
	}

	// Without a key the request is refused on the input validation.
	assert.Equal(t, fiber.StatusBadRequest, post(nil))

	// With the header it must get past that validation. The checkout service is
	// nil in this harness, so anything past the gate is a 5xx — what matters is
	// that it is no longer rejected as invalid input.
	assert.NotEqual(t, fiber.StatusBadRequest, post(map[string]string{"Idempotency-Key": "idem-uji-1"}),
		"the Idempotency-Key header must satisfy the input validation")
}
