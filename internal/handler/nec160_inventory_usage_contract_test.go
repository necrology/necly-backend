package handler_test

import (
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

func TestNEC160InventoryResponseSeparatesUsedUsersFromUsedSlots(t *testing.T) {
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
	if err := db.Create(&domain.User{
		ID: adminID, Email: "nec160.admin@example.test", Name: "NEC160 Admin",
		PasswordHash: "x", Role: domain.RoleAdmin, Active: true, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	categoryID, productID := uuid.NewString(), uuid.NewString()
	durationID, priceID := uuid.NewString(), uuid.NewString()
	if err := db.Create(&domain.Category{ID: categoryID, Name: "Streaming Video", Slug: "nec160-" + categoryID[:8], Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.Product{ID: productID, CategoryID: categoryID, Name: "Netflix Premium", Slug: "nec160-netflix-" + productID[:8], Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.ProductDuration{ID: durationID, Name: "1 Bulan", Days: 30, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.ProductPrice{ID: priceID, ProductID: productID, DurationID: durationID, Amount: 186000, Currency: "IDR", Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}

	accountID := uuid.NewString()
	if err := db.Create(&domain.InventoryAccount{
		ID: accountID, ProductID: productID, Label: "Akun sharing", EncryptedCredentials: []byte("cipher"),
		Status: "active", SharingLimit: 5, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	buyerID := uuid.NewString()
	if err := db.Create(&domain.User{
		ID: buyerID, Email: "nec160.buyer@example.test", Name: "NEC160 Buyer",
		PasswordHash: "x", Role: domain.RoleCustomer, Active: true, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed buyer: %v", err)
	}
	for i := 0; i < 3; i++ {
		slot := domain.InventorySlot{
			ID: uuid.NewString(), InventoryAccountID: accountID, ProductPriceID: priceID,
			Status: "available", CreatedAt: now, UpdatedAt: now,
		}
		if i < 2 {
			reservedBy := buyerID
			slot.Status = "reserved"
			slot.ReservedBy = &reservedBy
		}
		if err := db.Create(&slot).Error; err != nil {
			t.Fatalf("seed slot: %v", err)
		}
	}

	store := gormrepo.NewStore(db)
	cipher, err := service.NewCredentialCipher("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := service.NewTokenManager("test-jwt-secret-that-has-at-least-32-bytes", "necly", 15*time.Minute, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	authSvc := service.NewAuthService(store, service.NewPasswordHasher(4), tokens, service.AuthSettings{})
	router := handler.NewRouter(
		authSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		service.NewAdminInventoryService(store, cipher),
		tokens, cipher, nil, nil, nil,
	)
	app := fiber.New()
	router.Register(app)

	sessionID := uuid.NewString()
	if err := store.SaveRefreshToken(context.Background(), &domain.RefreshToken{
		ID: sessionID, UserID: adminID, TokenHash: uuid.NewString(), ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	access, err := tokens.IssueAccess(adminID, string(domain.RoleAdmin), sessionID)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/inventory?product_id="+productID, nil)
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("list inventory = %d %s, want 200", resp.StatusCode, payload)
	}
	var body struct {
		Accounts []struct {
			ID            string `json:"id"`
			TotalSlots    int    `json:"total_slots"`
			Available     int    `json:"available_slots"`
			UsedSlots     int    `json:"used_slots"`
			UsedUsers     int    `json:"used_users"`
			AssignedUsers int    `json:"assigned_users"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Accounts) != 1 {
		t.Fatalf("accounts = %d, want 1 (%s)", len(body.Accounts), payload)
	}
	account := body.Accounts[0]
	if account.ID != accountID {
		t.Fatalf("account id = %s, want %s", account.ID, accountID)
	}
	if account.UsedSlots != 2 {
		t.Fatalf("used_slots = %d, want 2 occupied rows", account.UsedSlots)
	}
	if account.UsedUsers != 1 || account.AssignedUsers != 1 {
		t.Fatalf("used_users = %d, assigned_users = %d, want 1/1", account.UsedUsers, account.AssignedUsers)
	}
	if account.TotalSlots != 3 || account.Available != 1 {
		t.Fatalf("capacity = total %d available %d, want 3/1", account.TotalSlots, account.Available)
	}
}
