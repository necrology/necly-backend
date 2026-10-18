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

// TestW8AdminOrderDetailContractExercisesTheRealStore covers the C19 read path:
// admin gets the stored order and payment, seller is denied before any data is
// read, and unknown/malformed ids are a clean 404 instead of a 500.
func TestW8AdminOrderDetailContractExercisesTheRealStore(t *testing.T) {
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
	sellerID := uuid.NewString()
	customerID := uuid.NewString()
	for _, user := range []*domain.User{
		{ID: adminID, Email: "fixture.admin@example.test", Name: "Fixture Admin", PasswordHash: "x", Role: domain.RoleAdmin, Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: sellerID, Email: "fixture.seller@example.test", Name: "Fixture Seller", PasswordHash: "x", Role: domain.RoleSeller, Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: customerID, Email: "fixture.customer@example.test", Name: "Fixture Customer", PasswordHash: "x", Role: domain.RoleCustomer, Active: true, CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(user).Error; err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}

	categoryID := "0192f0a2-0000-0000-0000-000000000001"
	productID := "0192f0a3-0000-0000-0000-000000000001"
	durationID := "0192f0a1-0000-0000-0000-000000000001"
	priceID := "0192f0a4-0000-0000-0000-000000000001"
	if err := db.Create(&domain.Category{
		ID: categoryID, Name: "Streaming Video", Slug: "video-streaming", Active: true, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed category: %v", err)
	}
	if err := db.Create(&domain.Product{
		ID: productID, CategoryID: categoryID, Name: "Netflix Premium", Slug: "netflix-premium", Active: true, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed product: %v", err)
	}
	if err := db.Create(&domain.ProductDuration{
		ID: durationID, Name: "1 Bulan", Days: 30, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed duration: %v", err)
	}
	if err := db.Create(&domain.ProductPrice{
		ID: priceID, ProductID: productID, DurationID: durationID, Amount: 186000, Currency: "IDR", Active: true, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed price: %v", err)
	}

	orderID := "0192f0b0-0000-0000-0000-000000000001"
	paymentID := "0192f0b1-0000-0000-0000-000000000001"
	itemID := "0192f0b2-0000-0000-0000-000000000001"
	order := &domain.Order{
		ID: orderID, Number: "ORD-1760832000000-0192f0b0", UserID: customerID,
		Status: domain.OrderPaid, Subtotal: 186000, Discount: 18600, Total: 167400,
		Currency: "IDR", PaymentDueAt: now.Add(15 * time.Minute), IdempotencyKey: "fixture-checkout-0001",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(order).Error; err != nil {
		t.Fatalf("seed order: %v", err)
	}
	if err := db.Create(&domain.OrderItem{
		ID: itemID, OrderID: orderID, ProductPriceID: priceID, ProductName: "Netflix Premium",
		DurationName: "1 Bulan", UnitPrice: 186000, Quantity: 1, DeviceCount: 1, LineTotal: 186000,
	}).Error; err != nil {
		t.Fatalf("seed order item: %v", err)
	}
	paidAt := now.Add(5 * time.Minute)
	if err := db.Create(&domain.Payment{
		ID: paymentID, OrderID: orderID, Provider: "xendit", ProviderPaymentID: "ps-fixture-1",
		Status: "paid", Amount: 167400, PaidAt: &paidAt, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed payment: %v", err)
	}

	store := gormrepo.NewStore(db)
	tokens, err := service.NewTokenManager("test-jwt-secret-that-has-at-least-32-bytes", "necly", 15*time.Minute, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	authSvc := service.NewAuthService(store, service.NewPasswordHasher(4), tokens, service.AuthSettings{})
	router := handler.NewRouter(
		authSvc,
		nil, nil, nil, nil,
		service.NewOrderService(store, store, store),
		nil, nil, nil, nil,
		service.NewAdminOrderService(store),
		nil, nil,
		tokens, nil, nil, nil, nil,
	)
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

	do := func(token, target string) (int, []byte) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, target, nil)
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

	status, payload := do(access(adminID, domain.RoleAdmin), "/api/v1/admin/orders/"+orderID)
	if status != fiber.StatusOK {
		t.Fatalf("admin detail = %d %s, want 200", status, payload)
	}
	var detail struct {
		ID       string             `json:"id"`
		Number   string             `json:"number"`
		Total    int64              `json:"total"`
		Items    []domain.OrderItem `json:"items"`
		Payments []domain.Payment   `json:"payments"`
		Customer *domain.User       `json:"customer"`
	}
	if err := json.Unmarshal(payload, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.ID != orderID || detail.Number != order.Number || detail.Total != 167400 {
		t.Fatalf("admin detail identity = %+v, want fixture order", detail)
	}
	if len(detail.Items) != 1 || detail.Items[0].ProductName != "Netflix Premium" {
		t.Fatalf("admin detail items = %+v, want one Netflix item", detail.Items)
	}
	if len(detail.Payments) != 1 || detail.Payments[0].Status != "paid" || detail.Payments[0].ProviderPaymentID != "ps-fixture-1" {
		t.Fatalf("admin detail payments = %+v, want paid xendit fixture", detail.Payments)
	}
	if detail.Customer == nil || detail.Customer.Email != "fixture.customer@example.test" {
		t.Fatalf("admin detail customer = %+v, want fixture customer", detail.Customer)
	}
	if bytes.Contains(payload, []byte("idempotency_key")) {
		t.Fatalf("admin detail leaked idempotency_key: %s", payload)
	}

	status, payload = do(access(sellerID, domain.RoleSeller), "/api/v1/admin/orders/"+orderID)
	if status != fiber.StatusForbidden {
		t.Fatalf("seller detail = %d %s, want 403", status, payload)
	}

	for _, id := range []string{uuid.NewString(), "not-a-uuid"} {
		status, payload = do(access(adminID, domain.RoleAdmin), "/api/v1/admin/orders/"+id)
		if status != fiber.StatusNotFound {
			t.Fatalf("missing detail %q = %d %s, want 404", id, status, payload)
		}
		var notFound struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(payload, &notFound)
		if notFound.Code != "not_found" {
			t.Fatalf("missing detail %q code = %q, want not_found (%s)", id, notFound.Code, payload)
		}
	}

	status, _ = do("", "/api/v1/admin/orders/"+orderID)
	if status != fiber.StatusUnauthorized {
		t.Fatalf("anonymous detail = %d, want 401", status)
	}
}
