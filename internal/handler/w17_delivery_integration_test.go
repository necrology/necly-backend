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
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/handler"
	"github.com/necly/services/internal/migrate"
	"github.com/necly/services/internal/platform/mailer"
	"github.com/necly/services/internal/platform/testdb"
	"github.com/necly/services/internal/repository/gormrepo"
	"github.com/necly/services/internal/service"
)

type recordingMailer struct {
	mu   sync.Mutex
	sent []mailer.Message
}

func (m *recordingMailer) Enabled() bool { return true }

func (m *recordingMailer) Send(_ context.Context, msg mailer.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

func (m *recordingMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

func (m *recordingMailer) body(index int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sent[index].Body
}

// TestW17ApprovalDeliversCredentialsOnce exercises the real HTTP route, the real
// PostgreSQL reservation, and the real cipher. Only the mailer is replaced, so
// the test proves the admin action (not a paid webhook) is the trigger and that
// replaying it cannot send a second email.
func TestW17ApprovalDeliversCredentialsOnce(t *testing.T) {
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
	adminID, sellerID, buyerID, unverifiedBuyerID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	verifiedAt := now
	for _, user := range []*domain.User{
		{ID: adminID, Email: "fixture.admin@example.test", Name: "Fixture Admin", PasswordHash: "x", Role: domain.RoleAdmin, Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: sellerID, Email: "fixture.seller@example.test", Name: "Fixture Seller", PasswordHash: "x", Role: domain.RoleSeller, Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: buyerID, Email: "fixture.buyer@example.test", Name: "Verified Buyer", PasswordHash: "x", Role: domain.RoleCustomer, Active: true, EmailVerifiedAt: &verifiedAt, CreatedAt: now, UpdatedAt: now},
		{ID: unverifiedBuyerID, Email: "fixture.unverified@example.test", Name: "Unverified Buyer", PasswordHash: "x", Role: domain.RoleCustomer, Active: true, CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(user).Error; err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}

	categoryID, productID, durationID, priceID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if err := db.Create(&domain.Category{ID: categoryID, Name: "Streaming Video", Slug: "video-" + categoryID[:8], Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.Product{ID: productID, CategoryID: categoryID, Name: "Netflix Premium", Slug: "netflix-" + productID[:8], Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
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
	sealed, err := cipher.Encrypt([]byte("buyer-login@example.test:secret-pass"))
	if err != nil {
		t.Fatal(err)
	}

	seedOrder := func(userID, number, status string) (string, string) {
		t.Helper()
		orderID, itemID := uuid.NewString(), uuid.NewString()
		// Each fixture order gets its own account: NEC-89 Fase 2 deliberately
		// refuses to reuse an account for the same buyer+product while another
		// unused account exists.
		accountID := uuid.NewString()
		if err := db.Create(&domain.InventoryAccount{
			ID: accountID, ProductID: productID, Label: "Akun " + number, EncryptedCredentials: sealed,
			Status: "active", SharingLimit: 5, CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&domain.Order{
			ID: orderID, Number: number, UserID: userID, Status: domain.OrderStatus(status),
			Subtotal: 150000, Total: 150000, Currency: "IDR", PaymentDueAt: now.Add(time.Hour),
			IdempotencyKey: "fixture-" + orderID[:8], CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&domain.OrderItem{
			ID: itemID, OrderID: orderID, ProductPriceID: priceID, ProductName: "Netflix Premium",
			DurationName: "1 Bulan", UnitPrice: 150000, Quantity: 1, DeviceCount: 1, LineTotal: 150000,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&domain.InventorySlot{
			ID: uuid.NewString(), InventoryAccountID: accountID, ProductPriceID: priceID,
			Status: "reserved", ReservedBy: &userID, OrderItemID: &itemID, CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
		return orderID, itemID
	}
	orderID, _ := seedOrder(buyerID, "ORD-W17-SENT", string(domain.OrderPaid))
	unverifiedOrderID, _ := seedOrder(unverifiedBuyerID, "ORD-W17-UNVERIFIED", string(domain.OrderPaid))
	patchOrderID, _ := seedOrder(buyerID, "ORD-W17-PATCH", string(domain.OrderPaid))

	store := gormrepo.NewStore(db)
	tokens, err := service.NewTokenManager("test-jwt-secret-that-has-at-least-32-bytes", "necly", 15*time.Minute, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	authSvc := service.NewAuthService(store, service.NewPasswordHasher(4), tokens, service.AuthSettings{})
	mail := &recordingMailer{}
	adminOrderSvc := service.NewAdminOrderService(store)
	deliverySvc := service.NewOrderDeliveryService(store, store, cipher, mail, store)
	router := handler.NewRouter(
		authSvc,
		nil, nil, nil, nil,
		service.NewOrderService(store, store, store),
		nil, nil, nil, nil,
		adminOrderSvc,
		nil, nil,
		tokens, cipher, nil, nil, nil,
	)
	router.SetDeliveryService(deliverySvc)
	app := fiber.New()
	router.Register(app)

	access := func(userID string, role domain.UserRole) string {
		t.Helper()
		sessionID := uuid.NewString()
		if err := store.SaveRefreshToken(context.Background(), &domain.RefreshToken{
			ID: sessionID, UserID: userID, TokenHash: uuid.NewString(), ExpiresAt: now.Add(time.Hour), CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		token, err := tokens.IssueAccess(userID, string(role), sessionID)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	call := func(method, token, target, body string) (int, []byte) {
		t.Helper()
		req := httptest.NewRequest(method, target, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, payload
	}
	post := func(token, target string) (int, []byte) {
		t.Helper()
		return call(http.MethodPost, token, target, "{}")
	}

	if status, payload := post(access(sellerID, domain.RoleSeller), "/api/v1/admin/orders/"+orderID+"/approve-delivery"); status != fiber.StatusForbidden {
		t.Fatalf("seller approve = %d %s, want 403", status, payload)
	}
	if mail.count() != 0 {
		t.Fatal("non-admin approval sent an email")
	}

	status, payload := post(access(adminID, domain.RoleAdmin), "/api/v1/admin/orders/"+orderID+"/approve-delivery")
	if status != fiber.StatusOK {
		t.Fatalf("admin approve = %d %s", status, payload)
	}
	if strings.Contains(string(payload), "secret-pass") || strings.Contains(string(payload), "buyer-login@example.test") {
		t.Fatalf("approval response leaked credentials: %s", payload)
	}
	var orderResponse struct {
		Status     string `json:"status"`
		Deliveries []struct {
			Status    string `json:"status"`
			ErrorCode string `json:"error_code"`
		} `json:"deliveries"`
	}
	if err := json.Unmarshal(payload, &orderResponse); err != nil {
		t.Fatal(err)
	}
	if orderResponse.Status != string(domain.OrderProcessing) {
		t.Fatalf("order status = %s, want processing", orderResponse.Status)
	}
	if len(orderResponse.Deliveries) != 1 || orderResponse.Deliveries[0].Status != domain.DeliverySent {
		t.Fatalf("deliveries = %#v, want one sent", orderResponse.Deliveries)
	}
	if mail.count() != 1 {
		t.Fatalf("sent emails = %d, want 1", mail.count())
	}
	body := mail.body(0)
	for _, want := range []string{"ORD-W17-SENT", "buyer-login@example.test", "secret-pass", "Cara penggunaan"} {
		if !strings.Contains(body, want) {
			t.Errorf("email missing %q", want)
		}
	}

	// Replaying the same approval action must not produce a second email.
	if status, payload := post(access(adminID, domain.RoleAdmin), "/api/v1/admin/orders/"+orderID+"/approve-delivery"); status != fiber.StatusOK {
		t.Fatalf("replay approve = %d %s", status, payload)
	}
	if mail.count() != 1 {
		t.Fatalf("replay sent %d emails, want still 1", mail.count())
	}

	// The ordinary status transition to processing is the same approval trigger;
	// an admin does not have to use the dedicated endpoint.
	status, payload = call(http.MethodPatch, access(adminID, domain.RoleAdmin), "/api/v1/admin/orders/"+patchOrderID+"/status", `{"status":"processing"}`)
	if status != fiber.StatusOK {
		t.Fatalf("status approval = %d %s", status, payload)
	}
	if err := json.Unmarshal(payload, &orderResponse); err != nil {
		t.Fatal(err)
	}
	if len(orderResponse.Deliveries) != 1 || orderResponse.Deliveries[0].Status != domain.DeliverySent {
		t.Fatalf("status approval deliveries = %#v", orderResponse.Deliveries)
	}
	if mail.count() != 2 {
		t.Fatalf("status approval sent %d emails, want 2 total", mail.count())
	}

	// An unverified buyer must not receive credentials; the failure is visible
	// in the delivery ledger while the order remains in processing.
	status, payload = post(access(adminID, domain.RoleAdmin), "/api/v1/admin/orders/"+unverifiedOrderID+"/approve-delivery")
	if status != fiber.StatusOK {
		t.Fatalf("unverified approve = %d %s", status, payload)
	}
	if err := json.Unmarshal(payload, &orderResponse); err != nil {
		t.Fatal(err)
	}
	if len(orderResponse.Deliveries) != 1 || orderResponse.Deliveries[0].Status != domain.DeliveryFailed || orderResponse.Deliveries[0].ErrorCode != service.DeliveryErrorBuyerUnverified {
		t.Fatalf("unverified deliveries = %#v", orderResponse.Deliveries)
	}
	if mail.count() != 2 {
		t.Fatalf("unverified buyer caused a send; total emails = %d, want 2", mail.count())
	}
}
