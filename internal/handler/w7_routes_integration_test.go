package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/handler"
	"github.com/necly/services/internal/migrate"
	"github.com/necly/services/internal/platform/testdb"
	"github.com/necly/services/internal/repository/gormrepo"
	"github.com/necly/services/internal/service"
)

// TestW7AdminCatalogRoutesExerciseTheRealStore proves the HTTP status contract
// with the real repository: a free slug succeeds, duplicate becomes 409,
// malformed category becomes 400 and a missing product becomes 404.
func TestW7AdminCatalogRoutesExerciseTheRealStore(t *testing.T) {
	if !testdb.Available() {
		t.Skip("DB_HOST/DB_USER not set; skipping PostgreSQL integration test")
	}
	db, cleanup, err := testdb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	migrations, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Run(context.Background(), db, migrate.Options{Dir: migrations}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Now().UTC()
	adminID := uuid.NewString()
	sessionID := uuid.NewString()
	user := &domain.User{
		ID: adminID, Email: "fixture.admin@example.test", Name: "Fixture Admin",
		PasswordHash: "x", Role: domain.RoleAdmin, Active: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	store := gormrepo.NewStore(db)
	if err := store.SaveRefreshToken(context.Background(), &domain.RefreshToken{
		ID: sessionID, UserID: adminID, TokenHash: uuid.NewString(), ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	categoryID := "0192f0a2-0000-0000-0000-000000000001"
	if err := db.Create(&domain.Category{
		ID: categoryID, Name: "Streaming Video", Slug: "video-streaming", Active: true, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed category: %v", err)
	}

	tokens, err := service.NewTokenManager("test-jwt-secret-that-has-at-least-32-bytes", "necly", 15*time.Minute, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	authSvc := service.NewAuthService(store, service.NewPasswordHasher(4), tokens, service.AuthSettings{})
	router := handler.NewRouter(
		authSvc,
		service.NewCatalogService(store),
		nil, nil, nil, nil, nil, nil,
		service.NewAdminProductService(store),
		service.NewAdminCategoryService(store),
		nil, nil, nil,
		tokens, nil, nil, nil, nil,
	)
	app := fiber.New()
	router.Register(app)

	accessToken, err := tokens.IssueAccess(adminID, string(domain.RoleAdmin), sessionID)
	if err != nil {
		t.Fatal(err)
	}

	do := func(method, target string, body any) (int, []byte) {
		t.Helper()
		var reader *bytes.Reader
		if body == nil {
			reader = bytes.NewReader(nil)
		} else {
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			reader = bytes.NewReader(encoded)
		}
		req := httptest.NewRequest(method, target, reader)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+accessToken)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, payload
	}

	status, payload := do(http.MethodPost, "/api/v1/admin/products", map[string]any{
		"category_id": categoryID,
		"name":        "Produk Baru",
		"slug":        "produk-baru",
	})
	if status != fiber.StatusCreated {
		t.Fatalf("create with free slug = %d %s, want 201", status, payload)
	}
	var created domain.Product
	if err := json.Unmarshal(payload, &created); err != nil {
		t.Fatal(err)
	}

	status, payload = do(http.MethodPost, "/api/v1/admin/products", map[string]any{
		"category_id": categoryID,
		"name":        "Produk Lain",
		"slug":        "produk-baru",
	})
	if status != fiber.StatusConflict {
		t.Fatalf("duplicate slug = %d %s, want 409", status, payload)
	}

	status, payload = do(http.MethodPatch, "/api/v1/admin/products/"+created.ID, map[string]any{
		"slug": "slug-baru",
	})
	if status != fiber.StatusOK {
		t.Fatalf("update with free slug = %d %s, want 200", status, payload)
	}
	status, payload = do(http.MethodPatch, "/api/v1/admin/products/"+created.ID, map[string]any{
		"name": "Nama Setelah Edit",
		"slug": "slug-baru",
	})
	if status != fiber.StatusOK {
		t.Fatalf("update with unchanged slug = %d %s, want 200", status, payload)
	}

	status, payload = do(http.MethodPost, "/api/v1/admin/products", map[string]any{
		"category_id": "Streaming",
		"name":        "Kategori Salah",
		"slug":        "kategori-salah",
	})
	if status != fiber.StatusBadRequest {
		t.Fatalf("malformed category = %d %s, want 400", status, payload)
	}
	var invalid struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(payload, &invalid)
	if invalid.Code != "invalid_input" {
		t.Fatalf("malformed category code = %q, want invalid_input (%s)", invalid.Code, payload)
	}

	status, payload = do(http.MethodPatch, "/api/v1/admin/products/"+uuid.NewString(), map[string]any{
		"name": "Tidak Ada",
	})
	if status != fiber.StatusNotFound {
		t.Fatalf("missing product update = %d %s, want 404", status, payload)
	}

	status, payload = do(http.MethodDelete, "/api/v1/admin/products/"+uuid.NewString(), nil)
	if status != fiber.StatusNotFound {
		t.Fatalf("missing product delete = %d %s, want 404", status, payload)
	}

	status, payload = do(http.MethodGet, "/api/v1/admin/products?active=foo", nil)
	if status != fiber.StatusBadRequest {
		t.Fatalf("malformed active filter = %d %s, want 400", status, payload)
	}
	invalid = struct {
		Code string `json:"code"`
	}{}
	_ = json.Unmarshal(payload, &invalid)
	if invalid.Code != "invalid_input" {
		t.Fatalf("malformed active filter code = %q, want invalid_input (%s)", invalid.Code, payload)
	}

	status, payload = do(http.MethodGet, "/api/v1/admin/categories?active=foo", nil)
	if status != fiber.StatusBadRequest {
		t.Fatalf("malformed category active filter = %d %s, want 400", status, payload)
	}

	// NEC-89: coming_soon and image_url must round-trip through the admin DTO,
	// accept an explicit empty image_url as "clear", and reject the
	// contradictory coming-soon + inactive state.
	status, payload = do(http.MethodPost, "/api/v1/admin/products", map[string]any{
		"category_id": categoryID,
		"name":        "Produk Coming Soon",
		"slug":        "produk-coming-soon",
		"image_url":   "https://cdn.example.test/old.jpg",
		"icon_url":    "  /design-assets/logo-netflix.png  ",
		"coming_soon": true,
	})
	if status != fiber.StatusCreated {
		t.Fatalf("create coming soon = %d %s, want 201", status, payload)
	}
	var comingSoon domain.Product
	if err := json.Unmarshal(payload, &comingSoon); err != nil {
		t.Fatal(err)
	}
	if !comingSoon.ComingSoon || comingSoon.ImageURL != "https://cdn.example.test/old.jpg" || comingSoon.IconURL != "/design-assets/logo-netflix.png" {
		t.Fatalf("created coming soon product = %#v", comingSoon)
	}

	status, payload = do(http.MethodPatch, "/api/v1/admin/products/"+comingSoon.ID, map[string]any{
		"image_url": "",
		"icon_url":  "",
	})
	if status != fiber.StatusOK {
		t.Fatalf("clear image = %d %s, want 200", status, payload)
	}
	if err := json.Unmarshal(payload, &comingSoon); err != nil {
		t.Fatal(err)
	}
	if comingSoon.ImageURL != "" || comingSoon.IconURL != "" || !comingSoon.ComingSoon {
		t.Fatalf("cleared coming soon product = %#v", comingSoon)
	}

	status, payload = do(http.MethodPatch, "/api/v1/admin/products/"+comingSoon.ID, map[string]any{
		"icon_url": "javascript:alert(1)",
	})
	if status != fiber.StatusBadRequest {
		t.Fatalf("invalid icon_url = %d %s, want 400", status, payload)
	}

	status, payload = do(http.MethodPatch, "/api/v1/admin/products/"+comingSoon.ID, map[string]any{
		"active": false,
	})
	if status != fiber.StatusBadRequest {
		t.Fatalf("deactivate coming soon = %d %s, want 400", status, payload)
	}
	invalid = struct {
		Code string `json:"code"`
	}{}
	_ = json.Unmarshal(payload, &invalid)
	if invalid.Code != "invalid_input" {
		t.Fatalf("deactivate coming soon code = %q, want invalid_input (%s)", invalid.Code, payload)
	}

	status, payload = do(http.MethodPatch, "/api/v1/admin/products/"+comingSoon.ID, map[string]any{
		"active":      false,
		"coming_soon": false,
	})
	if status != fiber.StatusOK {
		t.Fatalf("deactivate after clearing coming soon = %d %s, want 200", status, payload)
	}

	status, payload = do(http.MethodPatch, "/api/v1/admin/products/"+comingSoon.ID, map[string]any{
		"coming_soon": true,
	})
	if status != fiber.StatusBadRequest {
		t.Fatalf("coming soon on inactive product = %d %s, want 400", status, payload)
	}
	invalid = struct {
		Code string `json:"code"`
	}{}
	_ = json.Unmarshal(payload, &invalid)
	if invalid.Code != "invalid_input" {
		t.Fatalf("coming soon on inactive code = %q, want invalid_input (%s)", invalid.Code, payload)
	}
}
