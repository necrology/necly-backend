package gormrepo

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/migrate"
	"github.com/necly/services/internal/platform/testdb"
	"github.com/necly/services/internal/service"
	"path/filepath"
)

// TestCheckoutPreventsDuplicateOrders_baris134 verifies that the checkout
// rejection (baris 133) prevents duplicate orders from being stored.
// The dashboard "Pesanan" page should only see one order per user per product.
func TestCheckoutPreventsDuplicateOrders_baris134(t *testing.T) {
	if !testdb.Available() {
		t.Skip("DB_HOST/DB_USER not set; skipping PostgreSQL integration test")
	}
	db, cleanup, err := testdb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	migrations, err := filepath.Abs(filepath.Join("..", "..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Run(context.Background(), db, migrate.Options{Dir: migrations}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	store := NewStore(db)
	provider := &stubProvider{}
	svc := service.NewCheckoutService(store, provider, 15*time.Minute)

	now := time.Now().UTC()
	userID := uuid.NewString()
	categoryID := uuid.NewString()
	productID := uuid.NewString()
	durationID := uuid.NewString()
	priceID := uuid.NewString()
	accountID := uuid.NewString()

	// Seed user
	if err := db.Create(&domain.User{
		ID: userID, Email: "baris134@example.com", Name: "Baris 134 User",
		PasswordHash: "x", Role: domain.RoleCustomer, Active: true, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// Seed category
	if err := db.Create(&domain.Category{
		ID: categoryID, Name: "Test", Slug: "test", Active: true, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed category: %v", err)
	}

	// Seed product
	if err := db.Create(&domain.Product{
		ID: productID, CategoryID: categoryID, Name: "Baris 134 Product", Slug: "baris-134-product",
		Active: true, ComingSoon: false, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed product: %v", err)
	}

	// Seed duration
	if err := db.Create(&domain.ProductDuration{
		ID: durationID, Name: "1 Month", Days: 30, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed duration: %v", err)
	}

	// Seed price
	if err := db.Create(&domain.ProductPrice{
		ID: priceID, ProductID: productID, DurationID: durationID, Amount: 100000,
		Currency: "IDR", Active: true, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed price: %v", err)
	}

	// Seed inventory account
	if err := db.Create(&domain.InventoryAccount{
		ID: accountID, ProductID: productID, EncryptedCredentials: []byte("creds"),
		Status: "active", SharingLimit: 5, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed account: %v", err)
	}

	// Seed slots
	for i := 0; i < 2; i++ {
		if err := db.Create(&domain.InventorySlot{
			ID: uuid.NewString(), InventoryAccountID: accountID, ProductPriceID: priceID,
			Status: "available", CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatalf("seed slot %d: %v", i, err)
		}
	}

	// First checkout - should succeed
	_, err = svc.Checkout(context.Background(), service.CheckoutInput{
		UserID: userID, IdempotencyKey: "baris134-first",
		Items: []service.CheckoutItemInput{{ProductPriceID: priceID, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("first checkout: %v", err)
	}

	// Mark delivery as sent
	var order domain.Order
	if err := db.Where("user_id = ?", userID).First(&order).Error; err != nil {
		t.Fatalf("load order: %v", err)
	}
	var item domain.OrderItem
	if err := db.Where("order_id = ?", order.ID).First(&item).Error; err != nil {
		t.Fatalf("load item: %v", err)
	}
	if err := db.Create(&domain.OrderDelivery{
		ID: uuid.NewString(), OrderID: order.ID, OrderItemID: item.ID,
		Status: domain.DeliverySent, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create sent delivery: %v", err)
	}

	// Second checkout for same product - should be rejected
	_, err = svc.Checkout(context.Background(), service.CheckoutInput{
		UserID: userID, IdempotencyKey: "baris134-second",
		Items: []service.CheckoutItemInput{{ProductPriceID: priceID, Quantity: 1}},
	})
	if err == nil {
		t.Fatal("second checkout should have been rejected")
	}

	// Verify only ONE order exists for this user
	var orders []domain.Order
	if err := db.Where("user_id = ?", userID).Find(&orders).Error; err != nil {
		t.Fatalf("load orders: %v", err)
	}
	if len(orders) != 1 {
		t.Fatalf("orders count = %d, want 1 (duplicate order should not exist)", len(orders))
	}

	// Verify only ONE checkout attempt with state 'reserved' or 'completed' exists
	var attempts []domain.CheckoutAttempt
	if err := db.Where("user_id = ?", userID).Find(&attempts).Error; err != nil {
		t.Fatalf("load attempts: %v", err)
	}
	completedOrReserved := 0
	for _, a := range attempts {
		if a.State == "reserved" || a.State == "completed" {
			completedOrReserved++
		}
	}
	if completedOrReserved != 1 {
		t.Fatalf("completed/reserved attempts = %d, want 1", completedOrReserved)
	}
}
