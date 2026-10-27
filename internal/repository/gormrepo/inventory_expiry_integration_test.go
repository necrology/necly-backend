package gormrepo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
)

func insertLifecycleSlot(t *testing.T, fixture *checkoutFixture, accountID, priceID, status string, accessExpiresAt *time.Time, reservedBy *string) *domain.InventorySlot {
	t.Helper()
	now := time.Now().UTC()
	slot := &domain.InventorySlot{
		ID: uuid.NewString(), InventoryAccountID: accountID, ProductPriceID: priceID,
		Status: status, AccessExpiresAt: accessExpiresAt, ReservedBy: reservedBy,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.db.Create(slot).Error; err != nil {
		t.Fatalf("insert lifecycle slot: %v", err)
	}
	return slot
}

func TestExpireInventoryIsolatesPerSlotAndPerAccountState(t *testing.T) {
	fixture := newCheckoutFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	future := now.AddDate(0, 0, 10).Truncate(time.Microsecond)
	past := now.AddDate(0, 0, -1).Truncate(time.Microsecond)

	accountA := newInventoryAccount(t, fixture, fixture.product, 5, "active", &future)
	accountB := newInventoryAccount(t, fixture, fixture.product, 5, "active", &future)
	expiredSlotA := insertLifecycleSlot(t, fixture, accountA.ID, fixture.price, "reserved", &past, nil)
	futureSlotA := insertLifecycleSlot(t, fixture, accountA.ID, fixture.price, "reserved", &future, nil)
	expiredSlotB := insertLifecycleSlot(t, fixture, accountB.ID, fixture.price, "reserved", &past, nil)

	result, err := fixture.store.ExpireInventory(ctx, now, 100)
	if err != nil {
		t.Fatalf("ExpireInventory: %v", err)
	}
	if len(result.SlotIDs) != 2 {
		t.Fatalf("expired slot ids = %v, want 2", result.SlotIDs)
	}

	loadSlot := func(id string) domain.InventorySlot {
		var slot domain.InventorySlot
		if err := fixture.db.Where("id = ?", id).First(&slot).Error; err != nil {
			t.Fatalf("load slot %s: %v", id, err)
		}
		return slot
	}
	if got := loadSlot(expiredSlotA.ID); got.Status != "expired" {
		t.Fatalf("expired slot A status = %s", got.Status)
	}
	if got := loadSlot(expiredSlotB.ID); got.Status != "expired" {
		t.Fatalf("expired slot B status = %s", got.Status)
	}
	if got := loadSlot(futureSlotA.ID); got.Status != "reserved" || got.AccessExpiresAt == nil || !got.AccessExpiresAt.Equal(future) {
		t.Fatalf("future slot changed: %+v", got)
	}

	// Idempotent second run: the status predicate leaves nothing to do.
	again, err := fixture.store.ExpireInventory(ctx, now, 100)
	if err != nil {
		t.Fatalf("second ExpireInventory: %v", err)
	}
	if len(again.SlotIDs) != 0 || len(again.AccountIDs) != 0 {
		t.Fatalf("second run changed rows: %+v", again)
	}

	// An expired account is marked expired, but the other user's slot on that
	// same account keeps its own access window untouched.
	if err := fixture.db.Model(&domain.InventoryAccount{}).Where("id = ?", accountA.ID).
		Update("expires_at", past).Error; err != nil {
		t.Fatalf("age account: %v", err)
	}
	accountResult, err := fixture.store.ExpireInventory(ctx, now, 100)
	if err != nil {
		t.Fatalf("expire account: %v", err)
	}
	if len(accountResult.AccountIDs) != 1 || accountResult.AccountIDs[0] != accountA.ID {
		t.Fatalf("expired accounts = %v, want [%s]", accountResult.AccountIDs, accountA.ID)
	}
	var storedAccount domain.InventoryAccount
	if err := fixture.db.Where("id = ?", accountA.ID).First(&storedAccount).Error; err != nil {
		t.Fatal(err)
	}
	if storedAccount.Status != service.InventoryStatusExpired {
		t.Fatalf("account status = %s, want expired", storedAccount.Status)
	}
	if got := loadSlot(futureSlotA.ID); got.Status != "reserved" || got.AccessExpiresAt == nil || !got.AccessExpiresAt.Equal(future) {
		t.Fatalf("slot of expired account was rewritten: %+v", got)
	}
	if accountResult.SlotIDs != nil && len(accountResult.SlotIDs) != 0 {
		t.Fatalf("account expiry changed slot rows: %v", accountResult.SlotIDs)
	}
}

func TestExpiredAccountStopsStockAndCheckout(t *testing.T) {
	fixture := newCheckoutFixture(t)
	now := time.Now().UTC()
	past := now.AddDate(0, 0, -1)
	future := now.AddDate(0, 0, 60)

	var account domain.InventoryAccount
	if err := fixture.db.Where("product_id = ?", fixture.product).First(&account).Error; err != nil {
		t.Fatalf("load fixture account: %v", err)
	}
	if err := fixture.db.Model(&domain.InventoryAccount{}).Where("id = ?", account.ID).
		Update("expires_at", past).Error; err != nil {
		t.Fatalf("expire account: %v", err)
	}
	product, err := fixture.store.GetProductWithStock(context.Background(), fixture.product)
	if err != nil {
		t.Fatalf("GetProductWithStock: %v", err)
	}
	for _, price := range product.Prices {
		if price.ID == fixture.price && price.AvailableSlots != 0 {
			t.Fatalf("available slots = %d for an expired account, want 0", price.AvailableSlots)
		}
	}
	if _, err := fixture.checkout(t, fixture.userA, "expired-account-stock", ""); !errors.Is(err, service.ErrOutOfStock) {
		t.Fatalf("checkout error = %v, want ErrOutOfStock", err)
	}

	// Same account with enough remaining time sells normally again.
	if err := fixture.db.Model(&domain.InventoryAccount{}).Where("id = ?", account.ID).
		Update("expires_at", future).Error; err != nil {
		t.Fatalf("renew account: %v", err)
	}
	product, err = fixture.store.GetProductWithStock(context.Background(), fixture.product)
	if err != nil {
		t.Fatalf("GetProductWithStock after renew: %v", err)
	}
	for _, price := range product.Prices {
		if price.ID == fixture.price && price.AvailableSlots < 1 {
			t.Fatalf("available slots = %d after renew", price.AvailableSlots)
		}
	}
}

func TestExtendSlotAccessRespectsAccountExpiry(t *testing.T) {
	fixture := newCheckoutFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	accountExpiry := now.AddDate(0, 0, 10)
	oldExpiry := now.AddDate(0, 0, 2).Truncate(time.Microsecond)
	account := newInventoryAccount(t, fixture, fixture.product, 5, "active", &accountExpiry)
	slot := insertLifecycleSlot(t, fixture, account.ID, fixture.price, "expired", &oldExpiry, nil)

	if _, err := fixture.store.ExtendSlotAccess(ctx, slot.ID, now, 30); !errors.Is(err, service.ErrSlotExceedsAccountExpiry) {
		t.Fatalf("overrun error = %v, want ErrSlotExceedsAccountExpiry", err)
	}
	extended, err := fixture.store.ExtendSlotAccess(ctx, slot.ID, now, 5)
	if err != nil {
		t.Fatalf("ExtendSlotAccess: %v", err)
	}
	want := oldExpiry.AddDate(0, 0, 5)
	if extended.AccessExpiresAt == nil || !extended.AccessExpiresAt.Equal(want) {
		t.Fatalf("new expiry = %v, want %s", extended.AccessExpiresAt, want)
	}
	if extended.Status != "available" {
		t.Fatalf("status = %s, want available after extending an unattached expired slot", extended.Status)
	}
}

func TestCreateNotificationsDeduplicatesReminderKeys(t *testing.T) {
	fixture := newCheckoutFixture(t)
	now := time.Now().UTC()
	key := "inventory-slot:test:h7:20261027"
	notifications := []domain.Notification{{
		UserID: fixture.userA, Type: "inventory_slot_expiry",
		Title: "Hampir berakhir", Body: "Uji", DedupeKey: &key, CreatedAt: now,
	}}
	created, err := fixture.store.CreateNotifications(context.Background(), notifications)
	if err != nil || created != 1 {
		t.Fatalf("first insert = %d, %v", created, err)
	}
	created, err = fixture.store.CreateNotifications(context.Background(), notifications)
	if err != nil || created != 0 {
		t.Fatalf("duplicate insert = %d, %v; want 0", created, err)
	}
	var count int64
	if err := fixture.db.Model(&domain.Notification{}).Where("dedupe_key = ?", key).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("stored notifications = %d, want 1", count)
	}
}
