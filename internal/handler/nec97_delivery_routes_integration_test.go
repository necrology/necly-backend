package handler_test

import (
	"bytes"
	"context"
	"encoding/base64"
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
)

// TestNEC97DeliveryRoutesAssignAndReplace exercises the admin HTTP contract for
// account selection and replacement. Credentials are encrypted and sent only to
// the mailer; every API response is checked for leaks.
func TestNEC97DeliveryRoutesAssignAndReplace(t *testing.T) {
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
	adminID, buyerID := uuid.NewString(), uuid.NewString()
	verified := now
	for _, user := range []*domain.User{
		{ID: adminID, Email: "nec97.admin@example.test", Name: "Admin", PasswordHash: "x", Role: domain.RoleAdmin, Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: buyerID, Email: "nec97.buyer@example.test", Name: "Buyer", PasswordHash: "x", Role: domain.RoleCustomer, Active: true, EmailVerifiedAt: &verified, CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	categoryID, productID, durationID, priceID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if err := db.Create(&domain.Category{ID: categoryID, Name: "Streaming", Slug: "nec97-" + categoryID[:8], Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.Product{ID: productID, CategoryID: categoryID, Name: "Netflix Premium", Slug: "nec97-netflix-" + productID[:8], Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.ProductDuration{ID: durationID, Name: "1 Bulan", Days: 30, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.ProductPrice{ID: priceID, ProductID: productID, DurationID: durationID, Amount: 150000, Currency: "IDR", Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}

	cipher, err := service.NewCredentialCipher(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	if err != nil {
		t.Fatal(err)
	}
	accountA, accountB := uuid.NewString(), uuid.NewString()
	for index, item := range []struct {
		id       string
		label    string
		password string
	}{{accountA, "Akun A", "secret-account-a"}, {accountB, "Akun B", "secret-account-b"}} {
		// Keep a simple username:password credential for the delivery parser.
		sealed, err := cipher.Encrypt([]byte("nec97-" + string(rune('a'+index)) + "@example.test:" + item.password))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&domain.InventoryAccount{
			ID: item.id, ProductID: productID, Label: item.label, EncryptedCredentials: sealed,
			Status: "active", SharingLimit: 5, CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	orderID, itemID := uuid.NewString(), uuid.NewString()
	order := &domain.Order{
		ID: orderID, Number: "NEC97-ROUTE", UserID: buyerID, Status: domain.OrderProcessing,
		Subtotal: 150000, Total: 150000, Currency: "IDR", PaymentDueAt: now.Add(time.Hour),
		IdempotencyKey: "nec97-route", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(order).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.OrderItem{
		ID: itemID, OrderID: orderID, ProductPriceID: priceID, ProductName: "Netflix Premium",
		DurationName: "1 Bulan", UnitPrice: 150000, Quantity: 1, DeviceCount: 1, LineTotal: 150000,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.InventorySlot{
		ID: uuid.NewString(), InventoryAccountID: accountA, ProductPriceID: priceID,
		Status: "reserved", ReservedBy: &buyerID, OrderItemID: &itemID, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.Payment{
		ID: uuid.NewString(), OrderID: orderID, Provider: "manual", ProviderPaymentID: "nec97-paid-" + orderID[:8],
		Status: "paid", Amount: 150000, PaidAt: &now, CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	store := gormrepo.NewStore(db)
	tokens, err := service.NewTokenManager("test-jwt-secret-that-has-at-least-32-bytes", "necly", 15*time.Minute, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	authSvc := service.NewAuthService(store, service.NewPasswordHasher(4), tokens, service.AuthSettings{})
	mail := &recordingMailer{}
	deliverySvc := service.NewOrderDeliveryService(store, store, cipher, mail, store)
	router := handler.NewRouter(
		authSvc, nil, nil, nil, nil, service.NewOrderService(store, store, store),
		nil, nil, nil, nil, service.NewAdminOrderService(store), nil, nil,
		tokens, cipher, nil, nil, nil,
	)
	router.SetDeliveryService(deliverySvc)
	app := fiber.New()
	router.Register(app)

	sessionID := uuid.NewString()
	if err := store.SaveRefreshToken(context.Background(), &domain.RefreshToken{
		ID: sessionID, UserID: adminID, TokenHash: uuid.NewString(), ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	token, err := tokens.IssueAccess(adminID, string(domain.RoleAdmin), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, target, body string) (int, []byte) {
		t.Helper()
		req := httptest.NewRequest(method, target, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := io.ReadAll(resp.Body)
		if strings.Contains(string(payload), "secret-account-") {
			t.Fatalf("%s %s leaked credentials: %s", method, target, payload)
		}
		return resp.StatusCode, payload
	}

	status, payload := call(http.MethodGet, "/api/v1/admin/orders/"+orderID+"/delivery-options", "")
	if status != fiber.StatusOK {
		t.Fatalf("delivery-options = %d %s", status, payload)
	}
	var options struct {
		Items []struct {
			DeliveryID string `json:"delivery_id"`
			Accounts   []struct {
				InventoryAccountID string `json:"inventory_account_id"`
				CanAssign          bool   `json:"can_assign"`
			} `json:"accounts"`
		} `json:"items"`
	}
	if err := json.Unmarshal(payload, &options); err != nil {
		t.Fatal(err)
	}
	if len(options.Items) != 1 || len(options.Items[0].Accounts) != 2 {
		t.Fatalf("delivery options = %s", payload)
	}
	deliveryID := options.Items[0].DeliveryID

	status, payload = call(http.MethodPost, "/api/v1/admin/orders/"+orderID+"/deliveries/"+deliveryID+"/assign", `{"inventory_account_id":"`+accountB+`"}`)
	if status != fiber.StatusOK {
		t.Fatalf("assign = %d %s", status, payload)
	}

	status, payload = call(http.MethodPost, "/api/v1/admin/orders/"+orderID+"/approve-delivery", "{}")
	if status != fiber.StatusOK {
		t.Fatalf("approve = %d %s", status, payload)
	}
	if mail.count() != 1 {
		t.Fatalf("delivery emails = %d, want 1", mail.count())
	}
	if !strings.Contains(mail.body(0), "secret-account-b") {
		t.Fatalf("delivery email did not use the selected account:\n%s", mail.body(0))
	}

	// Let the buyer's access window expire, then replace the account through the
	// HTTP API. A replacement must email the new credentials exactly once.
	expired := now.Add(-time.Hour)
	if err := db.Model(&domain.InventorySlot{}).Where("order_item_id = ?", itemID).
		Updates(map[string]any{"activated_at": expired.Add(-30 * 24 * time.Hour), "access_expires_at": expired}).Error; err != nil {
		t.Fatal(err)
	}
	status, payload = call(http.MethodPost, "/api/v1/admin/orders/"+orderID+"/deliveries/"+deliveryID+"/replace",
		`{"inventory_account_id":"`+accountA+`","reason":"akun lama expired"}`)
	if status != fiber.StatusOK {
		t.Fatalf("replace = %d %s", status, payload)
	}
	if mail.count() != 2 {
		t.Fatalf("replacement emails = %d, want 2 total", mail.count())
	}
	if !strings.Contains(mail.body(1), "secret-account-a") || !strings.Contains(mail.body(1), "Masa akses") {
		t.Fatalf("replacement email did not contain the new account:\n%s", mail.body(1))
	}
	var replaced struct {
		Delivery struct {
			Status       string `json:"status"`
			Replacements []struct {
				Status string `json:"status"`
			} `json:"replacements"`
			AssignedAccounts []struct {
				InventoryAccountID string `json:"inventory_account_id"`
			} `json:"assigned_accounts"`
		} `json:"delivery"`
	}
	if err := json.Unmarshal(payload, &replaced); err != nil {
		t.Fatal(err)
	}
	if replaced.Delivery.Status != domain.DeliverySent || len(replaced.Delivery.Replacements) != 1 ||
		replaced.Delivery.Replacements[0].Status != "sent" ||
		len(replaced.Delivery.AssignedAccounts) != 1 ||
		replaced.Delivery.AssignedAccounts[0].InventoryAccountID != accountA {
		t.Fatalf("replace response = %s", payload)
	}

	// The detail view carries the latest payment status and the same delivery
	// history without any credential fields.
	status, payload = call(http.MethodGet, "/api/v1/admin/orders/"+orderID, "")
	if status != fiber.StatusOK {
		t.Fatalf("order detail = %d %s", status, payload)
	}
	var detail struct {
		PaymentStatus string `json:"payment_status"`
		Deliveries    []struct {
			Replacements []struct {
				Status string `json:"status"`
			} `json:"replacements"`
		} `json:"deliveries"`
	}
	if err := json.Unmarshal(payload, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.PaymentStatus != "paid" || len(detail.Deliveries) != 1 || len(detail.Deliveries[0].Replacements) != 1 {
		t.Fatalf("order detail = %s", payload)
	}
}
