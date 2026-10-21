package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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
	"gorm.io/gorm"
)

func newNEC96HandlerFixture(t *testing.T) (*gorm.DB, *gormrepo.Store, *fiber.App, string, string) {
	t.Helper()
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
		ID: adminID, Email: "nec96.admin@example.test", Name: "NEC96 Admin",
		PasswordHash: "x", Role: domain.RoleAdmin, Active: true, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	categoryID := uuid.NewString()
	productID := uuid.NewString()
	durationID := uuid.NewString()
	priceID := uuid.NewString()
	if err := db.Create(&domain.Category{ID: categoryID, Name: "Streaming Video", Slug: "nec96-" + categoryID[:8], Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed category: %v", err)
	}
	if err := db.Create(&domain.Product{ID: productID, CategoryID: categoryID, Name: "Netflix Premium", Slug: "nec96-netflix-" + productID[:8], Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed product: %v", err)
	}
	if err := db.Create(&domain.ProductDuration{ID: durationID, Name: "1 Bulan", Days: 30, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed duration: %v", err)
	}
	if err := db.Create(&domain.ProductPrice{ID: priceID, ProductID: productID, DurationID: durationID, Amount: 186000, Currency: "IDR", Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed price: %v", err)
	}

	store := gormrepo.NewStore(db)
	cipher, err := service.NewCredentialCipher("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	tokens, err := service.NewTokenManager("test-jwt-secret-that-has-at-least-32-bytes", "necly", 15*time.Minute, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	authSvc := service.NewAuthService(store, service.NewPasswordHasher(4), tokens, service.AuthSettings{})
	router := handler.NewRouter(
		authSvc,
		nil, nil, nil, nil,
		nil,
		nil, nil, nil, nil,
		nil,
		nil, service.NewAdminInventoryService(store, cipher),
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
	return db, store, app, access, productID
}

func nec96Request(t *testing.T, app *fiber.App, access, method, path, body string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, payload
}

func TestNEC96InventoryOTPAndCRUDContract(t *testing.T) {
	_, store, app, access, productID := newNEC96HandlerFixture(t)
	secret := "JBSWY3DPEHPK3PXP"

	status, payload := nec96Request(t, app, access, http.MethodPost, "/api/v1/admin/inventory",
		`{"product_id":"`+productID+`","credentials":"user:pass","label":"Akun OTP","sharing_limit":5,"otp_secret":"`+secret+`"}`)
	if status != fiber.StatusCreated {
		t.Fatalf("create account = %d %s", status, payload)
	}
	if bytes.Contains(payload, []byte("otp_secret")) || bytes.Contains(payload, []byte(secret)) {
		t.Fatalf("create response leaked OTP material: %s", payload)
	}
	var created struct {
		ID            string `json:"id"`
		SharingLimit  int    `json:"sharing_limit"`
		OTPConfigured bool   `json:"otp_configured"`
		NeedsReview   bool   `json:"needs_review"`
		AssignedUsers int    `json:"assigned_users"`
	}
	if err := json.Unmarshal(payload, &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.SharingLimit != 5 || !created.OTPConfigured || created.NeedsReview {
		t.Fatalf("created account = %+v", created)
	}

	status, payload = nec96Request(t, app, access, http.MethodPost, "/api/v1/admin/inventory/"+created.ID+"/otp", "")
	if status != fiber.StatusOK {
		t.Fatalf("generate otp = %d %s", status, payload)
	}
	var otp struct {
		Code      string `json:"code"`
		ExpiresIn int    `json:"expires_in"`
		Period    int    `json:"period"`
	}
	if err := json.Unmarshal(payload, &otp); err != nil {
		t.Fatal(err)
	}
	if len(otp.Code) != 6 || otp.Period != 30 || otp.ExpiresIn < 1 || otp.ExpiresIn > 30 {
		t.Fatalf("otp = %+v, want six digits with 30s period", otp)
	}
	if bytes.Contains(payload, []byte(secret)) || bytes.Contains(payload, []byte("secret")) {
		t.Fatalf("otp response leaked secret: %s", payload)
	}

	// Empty string is the explicit delete contract.
	status, payload = nec96Request(t, app, access, http.MethodPatch, "/api/v1/admin/inventory/"+created.ID, `{"otp_secret":""}`)
	if status != fiber.StatusOK {
		t.Fatalf("clear otp = %d %s", status, payload)
	}
	status, payload = nec96Request(t, app, access, http.MethodPost, "/api/v1/admin/inventory/"+created.ID+"/otp", "")
	if status != fiber.StatusConflict {
		t.Fatalf("otp after clear = %d %s, want 409", status, payload)
	}
	var conflict struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(payload, &conflict); err != nil {
		t.Fatal(err)
	}
	if conflict.Code != "otp_not_configured" {
		t.Fatalf("code = %q, want otp_not_configured", conflict.Code)
	}

	status, payload = nec96Request(t, app, access, http.MethodPatch, "/api/v1/admin/inventory/"+created.ID, `{"sharing_limit":6}`)
	if status != fiber.StatusBadRequest {
		t.Fatalf("limit 6 = %d %s, want 400", status, payload)
	}
	status, payload = nec96Request(t, app, access, http.MethodPost, "/api/v1/admin/inventory",
		`{"product_id":"`+productID+`","credentials":"user:pass","sharing_limit":6}`)
	if status != fiber.StatusBadRequest {
		t.Fatalf("create limit 6 = %d %s, want 400", status, payload)
	}

	// A slot-less account is safe to delete.
	status, payload = nec96Request(t, app, access, http.MethodDelete, "/api/v1/admin/inventory/"+created.ID, "")
	if status != fiber.StatusOK {
		t.Fatalf("delete account = %d %s", status, payload)
	}
	if _, err := store.GetAccount(context.Background(), created.ID); err == nil {
		t.Fatal("deleted account is still readable")
	}
}

func TestNEC96SlotDetailExposesAccessWindowWithoutCredentials(t *testing.T) {
	db, store, app, access, productID := newNEC96HandlerFixture(t)
	now := time.Now().UTC()

	status, payload := nec96Request(t, app, access, http.MethodPost, "/api/v1/admin/inventory",
		`{"product_id":"`+productID+`","credentials":"user:pass","label":"Akun slot","sharing_limit":2}`)
	if status != fiber.StatusCreated {
		t.Fatalf("create account = %d %s", status, payload)
	}
	var account struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &account); err != nil {
		t.Fatal(err)
	}

	var price domain.ProductPrice
	if err := db.Where("product_id = ?", productID).First(&price).Error; err != nil {
		t.Fatalf("load price: %v", err)
	}
	status, payload = nec96Request(t, app, access, http.MethodPost, "/api/v1/admin/inventory/slots",
		`{"product_price_id":"`+price.ID+`","inventory_account_id":"`+account.ID+`","quantity":1}`)
	if status != fiber.StatusCreated {
		t.Fatalf("create slot = %d %s", status, payload)
	}

	userID := uuid.NewString()
	orderID := uuid.NewString()
	itemID := uuid.NewString()
	if err := db.Create(&domain.User{ID: userID, Email: "nec96.buyer@example.test", Name: "Buyer", PasswordHash: "x", Role: domain.RoleCustomer, Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed buyer: %v", err)
	}
	if err := db.Create(&domain.Order{
		ID: orderID, Number: "NEC96-SLOT", UserID: userID, Status: domain.OrderProcessing,
		Subtotal: 186000, Total: 186000, Currency: "IDR", PaymentDueAt: now,
		IdempotencyKey: "nec96-slot-" + uuid.NewString(), CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed order: %v", err)
	}
	if err := db.Create(&domain.OrderItem{
		ID: itemID, OrderID: orderID, ProductPriceID: price.ID,
		ProductName: "Netflix Premium", DurationName: "1 Bulan",
		UnitPrice: 186000, Quantity: 1, DeviceCount: 1, LineTotal: 186000,
	}).Error; err != nil {
		t.Fatalf("seed order item: %v", err)
	}
	if err := db.Create(&domain.Payment{
		ID: uuid.NewString(), OrderID: orderID, Provider: "manual",
		ProviderPaymentID: "nec96-slot-" + uuid.NewString(), Status: "paid",
		Amount: 186000, PaidAt: &now, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed payment: %v", err)
	}
	activated := now.Add(-2 * 24 * time.Hour)
	accessExpires := now.Add(4 * 24 * time.Hour)
	if err := db.Model(&domain.InventorySlot{}).
		Where("inventory_account_id = ?", account.ID).
		Updates(map[string]any{
			"status": "reserved", "reserved_by": userID, "order_item_id": itemID,
			"activated_at": activated, "access_expires_at": accessExpires, "updated_at": now,
		}).Error; err != nil {
		t.Fatalf("assign slot: %v", err)
	}

	status, payload = nec96Request(t, app, access, http.MethodGet, "/api/v1/admin/inventory/"+account.ID+"/slots", "")
	if status != fiber.StatusOK {
		t.Fatalf("list slots = %d %s", status, payload)
	}
	if bytes.Contains(payload, []byte("encrypted_credentials")) || bytes.Contains(payload, []byte("user:pass")) {
		t.Fatalf("slot response leaked credentials: %s", payload)
	}
	var listed struct {
		Slots []struct {
			OrderNumber     *string `json:"order_number"`
			PaymentStatus   *string `json:"payment_status"`
			ActivatedAt     *string `json:"activated_at"`
			AccessExpiresAt *string `json:"access_expires_at"`
			DaysRemaining   *int    `json:"days_remaining"`
			ReservedEmail   *string `json:"reserved_email"`
		} `json:"slots"`
	}
	if err := json.Unmarshal(payload, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Slots) != 1 {
		t.Fatalf("slots = %d, want 1 (%s)", len(listed.Slots), payload)
	}
	slot := listed.Slots[0]
	if slot.OrderNumber == nil || *slot.OrderNumber != "NEC96-SLOT" {
		t.Fatalf("order_number = %v", slot.OrderNumber)
	}
	if slot.PaymentStatus == nil || *slot.PaymentStatus != "paid" {
		t.Fatalf("payment_status = %v", slot.PaymentStatus)
	}
	if slot.ActivatedAt == nil || slot.AccessExpiresAt == nil {
		t.Fatal("access window missing from slot response")
	}
	if slot.DaysRemaining == nil || *slot.DaysRemaining != 4 {
		t.Fatalf("days_remaining = %v, want 4", slot.DaysRemaining)
	}
	if slot.ReservedEmail == nil || *slot.ReservedEmail != "nec96.buyer@example.test" {
		t.Fatalf("reserved_email = %v", slot.ReservedEmail)
	}
	if !strings.Contains(string(payload), `"access_expires_at"`) {
		t.Fatalf("raw slot response missing access_expires_at: %s", payload)
	}
	_ = store
}
