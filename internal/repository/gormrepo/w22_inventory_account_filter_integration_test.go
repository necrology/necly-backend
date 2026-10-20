package gormrepo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// resetInventory removes the checkout fixture's default sellable stock so a
// test can control every account status/expiry on its own.
func resetInventory(t *testing.T, fixture *checkoutFixture) {
	t.Helper()
	if err := fixture.db.Exec("DELETE FROM inventory_slots").Error; err != nil {
		t.Fatalf("clear inventory slots: %v", err)
	}
	if err := fixture.db.Exec("DELETE FROM inventory_accounts").Error; err != nil {
		t.Fatalf("clear inventory accounts: %v", err)
	}
}

func availableSlotsForPrice(t *testing.T, product *domain.ProductWithStock, priceID string) int {
	t.Helper()
	for _, price := range product.Prices {
		if price.ID == priceID {
			return price.AvailableSlots
		}
	}
	t.Fatalf("price %s missing from product detail", priceID)
	return 0
}

func availableSlotCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	if err := db.Model(&domain.InventorySlot{}).Where("status = ?", "available").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func TestW22UnsellableAccountsAreNotStockAndCannotBeReserved(t *testing.T) {
	fixture := newCheckoutFixture(t)
	resetInventory(t, fixture)
	ctx := context.Background()

	past := time.Now().UTC().Add(-time.Hour)
	inactive := newInventoryAccount(t, fixture, fixture.product, 1, "inactive", nil)
	expiredByTime := newInventoryAccount(t, fixture, fixture.product, 1, "active", &past)
	expiredStatus := newInventoryAccount(t, fixture, fixture.product, 1, "expired", nil)
	seedInventorySlots(t, fixture, inactive.ID, fixture.price, 1, "available")
	seedInventorySlots(t, fixture, expiredByTime.ID, fixture.price, 1, "available")
	seedInventorySlots(t, fixture, expiredStatus.ID, fixture.price, 1, "available")

	detail, err := fixture.store.GetProductWithStock(ctx, fixture.product)
	if err != nil {
		t.Fatalf("product detail: %v", err)
	}
	if got := availableSlotsForPrice(t, detail, fixture.price); got != 0 {
		t.Fatalf("detail available slots = %d, want 0 for inactive/expired accounts", got)
	}

	active := true
	products, total, err := fixture.store.ListProductsWithStock(ctx, service.ProductFilter{Active: &active, InStock: true}, 1, 20)
	if err != nil {
		t.Fatalf("in-stock listing: %v", err)
	}
	if total != 0 || len(products) != 0 {
		t.Fatalf("in-stock listing returned %d products, want none", total)
	}

	provider := &stubProvider{}
	svc := fixture.service(provider)
	if _, err := svc.Checkout(ctx, fixture.input(fixture.userA, "w22-unsellable-0001", "")); !errors.Is(err, service.ErrOutOfStock) {
		t.Fatalf("checkout error = %v, want ErrOutOfStock", err)
	}
	if provider.calls.Load() != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls.Load())
	}
	assertCheckoutMutationCounts(t, fixture.db, 0, 0, 3)
	if reserved := countReservedSlots(t, fixture.db); reserved != 0 {
		t.Fatalf("reserved slots = %d, want 0", reserved)
	}
}

func TestW22SellableAccountsRemainPurchasable(t *testing.T) {
	fixture := newCheckoutFixture(t)
	resetInventory(t, fixture)
	ctx := context.Background()

	future := time.Now().UTC().Add(24 * time.Hour)
	noExpiry := newInventoryAccount(t, fixture, fixture.product, 1, "active", nil)
	futureExpiry := newInventoryAccount(t, fixture, fixture.product, 1, "active", &future)
	seedInventorySlots(t, fixture, noExpiry.ID, fixture.price, 1, "available")
	seedInventorySlots(t, fixture, futureExpiry.ID, fixture.priceB, 1, "available")

	detail, err := fixture.store.GetProductWithStock(ctx, fixture.product)
	if err != nil {
		t.Fatalf("product detail: %v", err)
	}
	if got := availableSlotsForPrice(t, detail, fixture.price); got != 1 {
		t.Fatalf("no-expiry price slots = %d, want 1", got)
	}
	if got := availableSlotsForPrice(t, detail, fixture.priceB); got != 1 {
		t.Fatalf("future-expiry price slots = %d, want 1", got)
	}

	active := true
	_, total, err := fixture.store.ListProductsWithStock(ctx, service.ProductFilter{Active: &active, InStock: true}, 1, 20)
	if err != nil {
		t.Fatalf("in-stock listing: %v", err)
	}
	if total != 1 {
		t.Fatalf("in-stock products = %d, want 1", total)
	}

	provider := &stubProvider{}
	svc := fixture.service(provider)
	if _, err := svc.Checkout(ctx, fixture.input(fixture.userA, "w22-sellable-0001", "")); err != nil {
		t.Fatalf("checkout no-expiry account: %v", err)
	}
	second := fixture.input(fixture.userB, "w22-sellable-0002", "")
	second.Items = []service.CheckoutItemInput{{ProductPriceID: fixture.priceB, Quantity: 1}}
	if _, err := svc.Checkout(ctx, second); err != nil {
		t.Fatalf("checkout future-expiry account: %v", err)
	}
	assertCheckoutMutationCounts(t, fixture.db, 2, 2, 0)
	if reserved := countReservedSlots(t, fixture.db); reserved != 2 {
		t.Fatalf("reserved slots = %d, want 2", reserved)
	}
}

func waitForCheckoutAccountLock(t *testing.T, db *gorm.DB) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int64
		if err := db.Raw(`SELECT COUNT(*) FROM pg_stat_activity
			WHERE datname = current_database()
			  AND wait_event_type = 'Lock'
			  AND query LIKE '%inventory_accounts%'`).Scan(&waiting).Error; err != nil {
			t.Fatalf("inspect lock waits: %v", err)
		}
		if waiting > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("checkout never waited for the inventory account lock")
}

func TestW22CheckoutSerializesWithAccountLifecycleChange(t *testing.T) {
	tests := []struct {
		name  string
		apply func(*gorm.DB, string) error
	}{
		{
			name: "deactivated",
			apply: func(tx *gorm.DB, accountID string) error {
				return tx.Model(&domain.InventoryAccount{}).Where("id = ?", accountID).Update("status", "inactive").Error
			},
		},
		{
			name: "expired",
			apply: func(tx *gorm.DB, accountID string) error {
				past := time.Now().UTC().Add(-time.Minute)
				return tx.Model(&domain.InventoryAccount{}).Where("id = ?", accountID).Update("expires_at", &past).Error
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCheckoutFixture(t)
			resetInventory(t, fixture)
			account := newInventoryAccount(t, fixture, fixture.product, 1, "active", nil)
			seedInventorySlots(t, fixture, account.ID, fixture.price, 1, "available")

			// Simulate an admin lifecycle change that has locked the account but
			// not committed yet. Checkout must wait, then re-check the account
			// row after the commit instead of reserving a now-unsellable slot.
			tx := fixture.db.Begin()
			if tx.Error != nil {
				t.Fatalf("begin lifecycle change: %v", tx.Error)
			}
			defer tx.Rollback()
			var locked domain.InventoryAccount
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ?", account.ID).
				First(&locked).Error; err != nil {
				t.Fatalf("lock account: %v", err)
			}
			if err := test.apply(tx, account.ID); err != nil {
				t.Fatalf("apply lifecycle change: %v", err)
			}

			provider := &stubProvider{}
			svc := fixture.service(provider)
			outcome := make(chan error, 1)
			started := make(chan struct{})
			go func() {
				close(started)
				_, err := svc.Checkout(context.Background(), fixture.input(fixture.userA, "w22-race-"+test.name, ""))
				outcome <- err
			}()

			<-started
			waitForCheckoutAccountLock(t, fixture.db)
			if err := tx.Commit().Error; err != nil {
				t.Fatalf("commit lifecycle change: %v", err)
			}

			select {
			case err := <-outcome:
				if !errors.Is(err, service.ErrOutOfStock) {
					t.Fatalf("checkout error = %v, want ErrOutOfStock", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("checkout did not finish after the account change committed")
			}

			if provider.calls.Load() != 0 {
				t.Fatalf("provider calls = %d, want 0", provider.calls.Load())
			}
			assertCheckoutMutationCounts(t, fixture.db, 0, 0, 1)
			if reserved := countReservedSlots(t, fixture.db); reserved != 0 {
				t.Fatalf("reserved slots = %d, want 0", reserved)
			}
		})
	}
}

func TestW22DeactivatedAccountKeepsReservedSlotProtectedFromDelete(t *testing.T) {
	fixture := newCheckoutFixture(t)
	resetInventory(t, fixture)
	ctx := context.Background()
	account := newInventoryAccount(t, fixture, fixture.product, 1, "active", nil)
	seedInventorySlots(t, fixture, account.ID, fixture.price, 1, "available")

	provider := &stubProvider{}
	svc := fixture.service(provider)
	result, err := svc.Checkout(ctx, fixture.input(fixture.userA, "w22-protected-0001", ""))
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if err := fixture.db.Model(&domain.InventoryAccount{}).Where("id = ?", account.ID).Update("status", "inactive").Error; err != nil {
		t.Fatalf("deactivate account: %v", err)
	}

	if err := fixture.store.DeleteAccount(ctx, account.ID); !errors.Is(err, service.ErrInventoryAccountInUse) {
		t.Fatalf("delete error = %v, want ErrInventoryAccountInUse", err)
	}
	if available := availableSlotCount(t, fixture.db); available != 0 {
		t.Fatalf("available slots after deactivation = %d, want 0", available)
	}
	if reserved := countReservedSlots(t, fixture.db); reserved != 1 {
		t.Fatalf("reserved slots = %d, want 1", reserved)
	}
	var stored domain.Order
	if err := fixture.db.Where("id = ?", result.Order.ID).First(&stored).Error; err != nil {
		t.Fatalf("reserved order disappeared: %v", err)
	}
}
