package gormrepo

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/migrate"
	"github.com/necly/services/internal/platform/testdb"
	"github.com/necly/services/internal/service"
)

// TestCheckoutSecondOrderForSameProductRejected_baris133 verifies that a user
// who already has a sent delivery for a product cannot checkout that product again.
func TestCheckoutSecondOrderForSameProductRejected_baris133(t *testing.T) {
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
		ID: userID, Email: "test@example.com", Name: "Test User",
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
		ID: productID, CategoryID: categoryID, Name: "Test Product", Slug: "test-product",
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
		UserID: userID, IdempotencyKey: "first-checkout",
		Items: []service.CheckoutItemInput{{ProductPriceID: priceID, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("first checkout: %v", err)
	}
	// Mark the delivery as sent (simulating completed order with delivery)
	orderID := ""
	var order domain.Order
	if err := db.Where("user_id = ?", userID).First(&order).Error; err != nil {
		t.Fatalf("load order: %v", err)
	}
	orderID = order.ID
	var item domain.OrderItem
	if err := db.Where("order_id = ?", orderID).First(&item).Error; err != nil {
		t.Fatalf("load order item: %v", err)
	}
	delivery := domain.OrderDelivery{
		ID: uuid.NewString(), OrderID: orderID, OrderItemID: item.ID,
		Status: domain.DeliverySent, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&delivery).Error; err != nil {
		t.Fatalf("create delivery: %v", err)
	}
	// Second checkout for same product - should be rejected with service.ErrProductAlreadySubscribed
	_, err = svc.Checkout(context.Background(), service.CheckoutInput{
		UserID: userID, IdempotencyKey: "second-checkout",
		Items: []service.CheckoutItemInput{{ProductPriceID: priceID, Quantity: 1}},
	})
	if !errors.Is(err, service.ErrProductAlreadySubscribed) {
		t.Fatalf("second checkout error = %v, want service.ErrProductAlreadySubscribed", err)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1 (second checkout should not call provider)", provider.calls.Load())
	}
}

// TestCheckoutDifferentProductSucceeds_baris133 verifies that a user can
// checkout a different product even if they already have a sent delivery for another product.
func TestCheckoutDifferentProductSucceeds_baris133(t *testing.T) {
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
	productID1 := uuid.NewString()
	productID2 := uuid.NewString()
	durationID := uuid.NewString()
	priceID1 := uuid.NewString()
	priceID2 := uuid.NewString()
	accountID1 := uuid.NewString()
	accountID2 := uuid.NewString()
	// Seed user
	if err := db.Create(&domain.User{
		ID: userID, Email: "test2@example.com", Name: "Test User 2",
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
	// Seed product 1
	if err := db.Create(&domain.Product{
		ID: productID1, CategoryID: categoryID, Name: "Product 1", Slug: "product-1",
		Active: true, ComingSoon: false, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed product 1: %v", err)
	}
	// Seed product 2
	if err := db.Create(&domain.Product{
		ID: productID2, CategoryID: categoryID, Name: "Product 2", Slug: "product-2",
		Active: true, ComingSoon: false, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed product 2: %v", err)
	}
	// Seed duration
	if err := db.Create(&domain.ProductDuration{
		ID: durationID, Name: "1 Month", Days: 30, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed duration: %v", err)
	}
	// Seed prices
	if err := db.Create(&domain.ProductPrice{
		ID: priceID1, ProductID: productID1, DurationID: durationID, Amount: 100000,
		Currency: "IDR", Active: true, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed price 1: %v", err)
	}
	if err := db.Create(&domain.ProductPrice{
		ID: priceID2, ProductID: productID2, DurationID: durationID, Amount: 100000,
		Currency: "IDR", Active: true, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed price 2: %v", err)
	}
	// Seed inventory accounts
	if err := db.Create(&domain.InventoryAccount{
		ID: accountID1, ProductID: productID1, EncryptedCredentials: []byte("creds1"),
		Status: "active", SharingLimit: 5, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed account 1: %v", err)
	}
	if err := db.Create(&domain.InventoryAccount{
		ID: accountID2, ProductID: productID2, EncryptedCredentials: []byte("creds2"),
		Status: "active", SharingLimit: 5, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed account 2: %v", err)
	}
	// Seed slots for both products
	for i := 0; i < 2; i++ {
		if err := db.Create(&domain.InventorySlot{
			ID: uuid.NewString(), InventoryAccountID: accountID1, ProductPriceID: priceID1,
			Status: "available", CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatalf("seed slot product 1: %v", err)
		}
		if err := db.Create(&domain.InventorySlot{
			ID: uuid.NewString(), InventoryAccountID: accountID2, ProductPriceID: priceID2,
			Status: "available", CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatalf("seed slot product 2: %v", err)
		}
	}
	// First checkout for product 1 - should succeed
	_, err = svc.Checkout(context.Background(), service.CheckoutInput{
		UserID: userID, IdempotencyKey: "checkout-product-1",
		Items: []service.CheckoutItemInput{{ProductPriceID: priceID1, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("first checkout product 1: %v", err)
	}
	// Mark delivery as sent for product 1
	var order1 domain.Order
	if err := db.Where("user_id = ?", userID).First(&order1).Error; err != nil {
		t.Fatalf("load order 1: %v", err)
	}
	var item1 domain.OrderItem
	if err := db.Where("order_id = ?", order1.ID).First(&item1).Error; err != nil {
		t.Fatalf("load item 1: %v", err)
	}
	if err := db.Create(&domain.OrderDelivery{
		ID: uuid.NewString(), OrderID: order1.ID, OrderItemID: item1.ID,
		Status: domain.DeliverySent, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create delivery 1: %v", err)
	}
	// Second checkout for product 2 - should succeed
	_, err = svc.Checkout(context.Background(), service.CheckoutInput{
		UserID: userID, IdempotencyKey: "checkout-product-2",
		Items: []service.CheckoutItemInput{{ProductPriceID: priceID2, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("second checkout product 2: %v", err)
	}
	if provider.calls.Load() != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls.Load())
	}
}

// TestCheckoutPendingDeliveryNotCountedAsSubscribed_baris133 verifies that
// an order with a delivery that is NOT yet sent does not block a new checkout.
func TestCheckoutPendingDeliveryNotCountedAsSubscribed_baris133(t *testing.T) {
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
		ID: userID, Email: "test3@example.com", Name: "Test User 3",
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
		ID: productID, CategoryID: categoryID, Name: "Test Product", Slug: "test-product",
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
		UserID: userID, IdempotencyKey: "checkout-pending-1",
		Items: []service.CheckoutItemInput{{ProductPriceID: priceID, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("first checkout: %v", err)
	}
	// Create a delivery with status 'pending' (not 'sent')
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
		Status: domain.DeliveryPending, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create pending delivery: %v", err)
	}
	// Second checkout for same product - should succeed because delivery is not 'sent'
	_, err = svc.Checkout(context.Background(), service.CheckoutInput{
		UserID: userID, IdempotencyKey: "checkout-pending-2",
		Items: []service.CheckoutItemInput{{ProductPriceID: priceID, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("second checkout with pending delivery: %v", err)
	}
	if provider.calls.Load() != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls.Load())
	}
}

// TestCheckoutIdempotencyReplayNotBlockedBySubscriptionCheck_baris133 verifies
// that replaying the same idempotency key returns the stored order without
// triggering the subscription check again.
func TestCheckoutIdempotencyReplayNotBlockedBySubscriptionCheck_baris133(t *testing.T) {
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
		ID: userID, Email: "test4@example.com", Name: "Test User 4",
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
		ID: productID, CategoryID: categoryID, Name: "Test Product", Slug: "test-product",
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
	// First checkout
	key := "replay-test-key"
	first, err := svc.Checkout(context.Background(), service.CheckoutInput{
		UserID: userID, IdempotencyKey: key,
		Items: []service.CheckoutItemInput{{ProductPriceID: priceID, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("first checkout: %v", err)
	}
	// Manually mark delivery as sent
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
	// Replay with same idempotency key - should return stored order (200), not error
	second, err := svc.Checkout(context.Background(), service.CheckoutInput{
		UserID: userID, IdempotencyKey: key,
		Items: []service.CheckoutItemInput{{ProductPriceID: priceID, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("replay checkout: %v", err)
	}
	if !second.Replayed {
		t.Fatal("replay was not marked as replay")
	}
	if second.Order.ID != first.Order.ID {
		t.Fatalf("replay returned different order: %s != %s", second.Order.ID, first.Order.ID)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1 (replay should not call provider)", provider.calls.Load())
	}
}
