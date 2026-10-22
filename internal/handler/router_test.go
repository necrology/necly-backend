package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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
func (m *dummyAuthRepo) RefreshTokenByID(ctx context.Context, id string) (*domain.RefreshToken, error) {
	args := m.Called(ctx, id)
	if r := args.Get(0); r != nil {
		return r.(*domain.RefreshToken), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *dummyAuthRepo) UpdatePasswordHashAndRevokeSessions(ctx context.Context, userID, hash, exceptSessionID string, now time.Time) error {
	return m.Called(ctx, userID, hash, exceptSessionID, now).Error(0)
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

// TestAccountOTPLookupIsPublicAtTheDocumentedPath guards the contract used by
// the /otp page: POST /api/v1/account-otp/lookup. Registering the handler under
// the /public group made the browser call hit authMiddleware (401) and left the
// per-route rate limiter pointing at a route that did not exist.
func TestAccountOTPLookupIsPublicAtTheDocumentedPath(t *testing.T) {
	app, _, _ := setupTestApp(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/account-otp/lookup", strings.NewReader(`{"email":"akun@example.test","code":"ABCD234567"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	assert.NoError(t, err)
	// A nil service is the "mailbox not configured" state. A 503 proves the
	// request reached the handler instead of being stopped by authMiddleware.
	assert.Equal(t, fiber.StatusServiceUnavailable, resp.StatusCode)
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

func expectActiveSession(repo *dummyAuthRepo, userID, sessionID string, role domain.UserRole) {
	repo.On("UserByID", mock.Anything, userID).Return(&domain.User{ID: userID, Role: role, Active: true, EmailVerifiedAt: ptrTime(time.Now())}, nil)
	repo.On("RefreshTokenByID", mock.Anything, sessionID).Return(&domain.RefreshToken{ID: sessionID, UserID: userID, ExpiresAt: time.Now().Add(time.Hour)}, nil)
}

func ptrTime(value time.Time) *time.Time { return &value }

func TestRequireRole_LocalizedInsufficientPermissions(t *testing.T) {
	app, tokens, authRepo := setupTestApp(t)
	expectActiveSession(authRepo, "user-1", "session-1", domain.RoleCustomer)
	token, err := tokens.IssueAccess("user-1", string(domain.RoleCustomer), "session-1")
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
	app, tokens, authRepo := setupTestApp(t)
	expectActiveSession(authRepo, "admin-1", "session-admin-1", domain.RoleAdmin)
	token, err := tokens.IssueAccess("admin-1", string(domain.RoleAdmin), "session-admin-1")
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
	app, tokens, authRepo := setupTestApp(t)
	expectActiveSession(authRepo, "user-1", "session-1", domain.RoleCustomer)
	token, err := tokens.IssueAccess("user-1", string(domain.RoleCustomer), "session-1")
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

// stubCatalogRepo hanya mengisi method yang dipakai tes di bawah; sisanya
// dibiarkan nil lewat embed, karena CatalogRepository punya banyak method.
type stubCatalogRepo struct {
	service.CatalogRepository
	slugProduct  *domain.ProductWithStock
	slugErr      error
	prices       []domain.ProductPrice
	listFilter   func(service.ProductFilter)
	listProducts []domain.ProductWithStock
	listTotal    int64
	listErr      error
}

func (s stubCatalogRepo) GetProductWithStock(context.Context, string) (*domain.ProductWithStock, error) {
	// Setara perilaku store setelah penjagaan bentuk uuid: id yang bukan uuid
	// (yaitu slug) tidak pernah sampai ke Postgres dan langsung not found.
	return nil, errors.New("not found")
}

func (s stubCatalogRepo) GetProductBySlugWithStock(context.Context, string) (*domain.ProductWithStock, error) {
	return s.slugProduct, s.slugErr
}

func (s stubCatalogRepo) ListProductPrices(context.Context, string, bool) ([]domain.ProductPrice, error) {
	return s.prices, nil
}

func (s stubCatalogRepo) ListProductsWithStock(_ context.Context, filter service.ProductFilter, _, _ int) ([]domain.ProductWithStock, int64, error) {
	if s.listFilter != nil {
		s.listFilter(filter)
	}
	return s.listProducts, s.listTotal, s.listErr
}

func setupCatalogApp(t *testing.T, repo service.CatalogRepository) *fiber.App {
	t.Helper()
	app := fiber.New()
	app.Use(recover.New())
	tokens, err := service.NewTokenManager("test-jwt-secret-that-has-at-least-32-bytes", "necly", 15*time.Minute, 24*time.Hour)
	assert.NoError(t, err)
	router := handler.NewRouter(nil, service.NewCatalogService(repo), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, tokens, nil, nil, nil, nil)
	router.Register(app)
	return app
}

// product_id bertipe uuid: meneruskan slug apa adanya ke query harga dulu
// membuat Postgres melempar SQLSTATE 22P02 dan endpoint publik ini menjawab 500
// padahal produknya sekadar tidak ada.
func TestListProductPrices_UnknownSlugIsNotFoundNotServerError(t *testing.T) {
	app := setupCatalogApp(t, stubCatalogRepo{slugErr: errors.New("not found")})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/public/products/bukan-produk/prices", nil))
	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusNotFound, resp.StatusCode)
}

func TestListProductPrices_ResolvesSlugBeforeQueryingPrices(t *testing.T) {
	app := setupCatalogApp(t, stubCatalogRepo{
		slugProduct: &domain.ProductWithStock{Product: domain.Product{ID: "0192f0a4-0000-0000-0000-000000000001"}},
		prices:      []domain.ProductPrice{{ID: "price-1", Amount: 186000}},
	})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/public/products/netflix-premium/prices", nil))
	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
}

func TestGetProduct_UnknownSlugStaysNotFound(t *testing.T) {
	app := setupCatalogApp(t, stubCatalogRepo{slugErr: errors.New("not found")})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/public/products/bukan-produk", nil))
	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusNotFound, resp.StatusCode)
}

func TestListProductsRejectsMalformedFilters(t *testing.T) {
	app := setupCatalogApp(t, stubCatalogRepo{})

	cases := []string{
		"/api/v1/public/products?category=Streaming",
		"/api/v1/public/products?min_price=abc",
		"/api/v1/public/products?min_price=-1",
		"/api/v1/public/products?max_price=abc",
		"/api/v1/public/products?in_stock=maybe",
		"/api/v1/public/products?min_price=200000&max_price=100000",
	}
	for _, target := range cases {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, target, nil))
		assert.NoError(t, err)
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", target, resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		var payload map[string]string
		_ = json.Unmarshal(body, &payload)
		assert.Equal(t, "invalid_input", payload["code"], target)
	}
}

func TestListProductsPassesDocumentedFiltersToTheRepository(t *testing.T) {
	var captured service.ProductFilter
	app := setupCatalogApp(t, stubCatalogRepo{
		listFilter: func(filter service.ProductFilter) { captured = filter },
		listTotal:  1,
	})

	resp, err := app.Test(httptest.NewRequest(
		http.MethodGet,
		"/api/v1/public/products?category=0192f0a2-0000-0000-0000-000000000001&min_price=150000&max_price=200000&in_stock=true&search=netflix",
		nil,
	))
	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
	if captured.Active == nil || !*captured.Active {
		t.Fatal("public list must force active=true")
	}
	if captured.CategoryID != "0192f0a2-0000-0000-0000-000000000001" || captured.Search != "netflix" {
		t.Fatalf("captured filter = %#v", captured)
	}
	if captured.MinPrice == nil || *captured.MinPrice != 150000 || captured.MaxPrice == nil || *captured.MaxPrice != 200000 {
		t.Fatalf("price bounds = %#v", captured)
	}
	if !captured.InStock {
		t.Fatal("in_stock=true was not passed to the repository")
	}
}
