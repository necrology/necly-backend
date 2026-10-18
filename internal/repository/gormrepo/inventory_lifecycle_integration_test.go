package gormrepo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
	"gorm.io/gorm/clause"
)

func makeOverdue(t *testing.T, fixture *checkoutFixture, orderID string) {
	t.Helper()
	if err := fixture.db.Model(&domain.Order{}).Where("id = ?", orderID).
		Update("payment_due_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatalf("make overdue: %v", err)
	}
}

func countAvailableSlots(t *testing.T, fixture *checkoutFixture) int64 {
	t.Helper()
	var count int64
	if err := fixture.db.Model(&domain.InventorySlot{}).Where("status = ?", "available").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func TestExpireOverdueOrdersReleasesReservationExactlyOnce(t *testing.T) {
	fixture := newCheckoutFixture(t)
	result, err := fixture.checkout(t, fixture.userA, "fixture-expiry-once", "")
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	makeOverdue(t, fixture, result.Order.ID)
	if reserved := countReservedSlots(t, fixture.db); reserved != 1 {
		t.Fatalf("reserved slots before expiry = %d, want 1", reserved)
	}

	expired, err := fixture.store.ExpireOverdueOrders(context.Background(), time.Now().UTC(), 100)
	if err != nil {
		t.Fatalf("expire overdue: %v", err)
	}
	if expired != 1 {
		t.Fatalf("expired = %d, want 1", expired)
	}
	order := loadOrder(t, fixture, result.Order.ID)
	payment := loadPayment(t, fixture, result.Order.ID)
	if order.Status != domain.OrderExpired || payment.Status != "expired" {
		t.Fatalf("order/payment = %s/%s, want expired/expired", order.Status, payment.Status)
	}
	if reserved := countReservedSlots(t, fixture.db); reserved != 0 {
		t.Fatalf("reserved slots after expiry = %d, want 0", reserved)
	}
	if available := countAvailableSlots(t, fixture); available != 3 {
		t.Fatalf("available slots after expiry = %d, want 3", available)
	}

	expiredAgain, err := fixture.store.ExpireOverdueOrders(context.Background(), time.Now().UTC(), 100)
	if err != nil {
		t.Fatalf("second expiry run: %v", err)
	}
	if expiredAgain != 0 {
		t.Fatalf("second run expired = %d, want 0", expiredAgain)
	}
	if available := countAvailableSlots(t, fixture); available != 3 {
		t.Fatalf("second run changed available slots to %d", available)
	}
}

func TestExpireOverdueOrdersLeavesPaidStockAllocated(t *testing.T) {
	fixture := newCheckoutFixture(t)
	result, err := fixture.checkout(t, fixture.userA, "fixture-expiry-paid", "")
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if _, err := fixture.store.TransitionOrderStatus(context.Background(), result.Order.ID, domain.OrderPaid, time.Now().UTC()); err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	makeOverdue(t, fixture, result.Order.ID)

	expired, err := fixture.store.ExpireOverdueOrders(context.Background(), time.Now().UTC(), 100)
	if err != nil {
		t.Fatalf("expire overdue: %v", err)
	}
	if expired != 0 {
		t.Fatalf("expired = %d, want 0 for a paid order", expired)
	}
	if order := loadOrder(t, fixture, result.Order.ID); order.Status != domain.OrderPaid {
		t.Fatalf("paid order status = %s, want paid", order.Status)
	}
	if reserved := countReservedSlots(t, fixture.db); reserved != 1 {
		t.Fatalf("reserved slots = %d, want 1 (paid stock stays allocated)", reserved)
	}
}

func TestAdminCancellationReleasesPendingStockExactlyOnce(t *testing.T) {
	fixture := newCheckoutFixture(t)
	result, err := fixture.checkout(t, fixture.userA, "fixture-admin-cancel", "")
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	order, err := fixture.store.TransitionOrderStatus(context.Background(), result.Order.ID, domain.OrderCancelled, time.Now().UTC())
	if err != nil {
		t.Fatalf("cancel order: %v", err)
	}
	if order.Status != domain.OrderCancelled {
		t.Fatalf("status = %s, want cancelled", order.Status)
	}
	payment := loadPayment(t, fixture, result.Order.ID)
	if payment.Status != "cancelled" {
		t.Fatalf("payment status = %s, want cancelled", payment.Status)
	}
	if reserved := countReservedSlots(t, fixture.db); reserved != 0 {
		t.Fatalf("reserved slots = %d, want 0", reserved)
	}
	if available := countAvailableSlots(t, fixture); available != 3 {
		t.Fatalf("available slots = %d, want 3", available)
	}
	if _, err := fixture.store.TransitionOrderStatus(context.Background(), result.Order.ID, domain.OrderCancelled, time.Now().UTC()); !errors.Is(err, service.ErrInvalidTransition) {
		t.Fatalf("second cancellation error = %v, want ErrInvalidTransition", err)
	}
	if available := countAvailableSlots(t, fixture); available != 3 {
		t.Fatalf("second cancellation changed available slots to %d", available)
	}
}

func TestAdminCancellationOfPaidOrderKeepsStockAllocated(t *testing.T) {
	fixture := newCheckoutFixture(t)
	result, err := fixture.checkout(t, fixture.userA, "fixture-admin-cancel-paid", "")
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if _, err := fixture.store.TransitionOrderStatus(context.Background(), result.Order.ID, domain.OrderPaid, time.Now().UTC()); err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	if _, err := fixture.store.TransitionOrderStatus(context.Background(), result.Order.ID, domain.OrderCancelled, time.Now().UTC()); err != nil {
		t.Fatalf("cancel paid order: %v", err)
	}
	payment := loadPayment(t, fixture, result.Order.ID)
	if payment.Status != "paid" {
		t.Fatalf("paid payment status = %s, want paid", payment.Status)
	}
	if reserved := countReservedSlots(t, fixture.db); reserved != 1 {
		t.Fatalf("reserved slots = %d, want 1 (paid stock stays allocated)", reserved)
	}
}

func TestDeleteInventoryAccountWithReservedSlotReturnsConflict(t *testing.T) {
	fixture := newCheckoutFixture(t)
	if _, err := fixture.checkout(t, fixture.userA, "fixture-delete-linked", ""); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	var slot domain.InventorySlot
	if err := fixture.db.Where("status = ?", "reserved").First(&slot).Error; err != nil {
		t.Fatalf("load reserved slot: %v", err)
	}

	if err := fixture.store.DeleteAccount(context.Background(), slot.InventoryAccountID); !errors.Is(err, service.ErrInventoryAccountInUse) {
		t.Fatalf("delete error = %v, want ErrInventoryAccountInUse", err)
	}
	var account domain.InventoryAccount
	if err := fixture.db.Where("id = ?", slot.InventoryAccountID).First(&account).Error; err != nil {
		t.Fatalf("account was deleted: %v", err)
	}
	if reserved := countReservedSlots(t, fixture.db); reserved != 1 {
		t.Fatalf("reserved slots = %d, want 1 after rejected delete", reserved)
	}
	if available := countAvailableSlots(t, fixture); available != 2 {
		t.Fatalf("available slots = %d, want 2 after rejected delete", available)
	}
}

func TestDeleteEmptyInventoryAccountSucceeds(t *testing.T) {
	fixture := newCheckoutFixture(t)
	now := time.Now().UTC()
	account := domain.InventoryAccount{
		ID: uuid.NewString(), ProductID: fixture.product,
		EncryptedCredentials: []byte("fixture"), Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.db.Create(&account).Error; err != nil {
		t.Fatalf("create empty account: %v", err)
	}
	if err := fixture.store.DeleteAccount(context.Background(), account.ID); err != nil {
		t.Fatalf("delete empty account: %v", err)
	}
	var count int64
	if err := fixture.db.Model(&domain.InventoryAccount{}).Where("id = ?", account.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("empty account still exists after delete")
	}
}

func TestExpiryBatchIsBounded(t *testing.T) {
	fixture := newCheckoutFixture(t)
	first, err := fixture.checkout(t, fixture.userA, "fixture-expiry-batch-a", "")
	if err != nil {
		t.Fatalf("checkout A: %v", err)
	}
	second, err := fixture.checkout(t, fixture.userB, "fixture-expiry-batch-b", "")
	if err != nil {
		t.Fatalf("checkout B: %v", err)
	}
	makeOverdue(t, fixture, first.Order.ID)
	makeOverdue(t, fixture, second.Order.ID)

	expired, err := fixture.store.ExpireOverdueOrders(context.Background(), time.Now().UTC(), 1)
	if err != nil {
		t.Fatalf("first bounded run: %v", err)
	}
	if expired != 1 {
		t.Fatalf("first bounded run expired = %d, want 1", expired)
	}
	expired, err = fixture.store.ExpireOverdueOrders(context.Background(), time.Now().UTC(), 1)
	if err != nil {
		t.Fatalf("second bounded run: %v", err)
	}
	if expired != 1 {
		t.Fatalf("second bounded run expired = %d, want 1", expired)
	}
	expired, err = fixture.store.ExpireOverdueOrders(context.Background(), time.Now().UTC(), 1)
	if err != nil {
		t.Fatalf("third bounded run: %v", err)
	}
	if expired != 0 {
		t.Fatalf("third bounded run expired = %d, want 0", expired)
	}
}

// A checkout that has already locked a slot must make the delete wait and then
// observe the committed reservation. The earlier unlocked count could pass
// while the checkout was in flight, after which the cascade removed the newly
// reserved slot.
func TestDeleteInventoryAccountWaitsForConcurrentReservation(t *testing.T) {
	fixture := newCheckoutFixture(t)
	var slot domain.InventorySlot
	if err := fixture.db.Where("status = ?", "available").Order("id").First(&slot).Error; err != nil {
		t.Fatalf("load available slot: %v", err)
	}

	checkout := fixture.db.Begin()
	if checkout.Error != nil {
		t.Fatal(checkout.Error)
	}
	defer checkout.Rollback()
	if err := checkout.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", slot.ID).First(&slot).Error; err != nil {
		t.Fatalf("lock slot as checkout: %v", err)
	}
	if err := checkout.Model(&domain.InventorySlot{}).Where("id = ?", slot.ID).
		Updates(map[string]any{"status": "reserved", "reserved_by": fixture.userA}).Error; err != nil {
		t.Fatalf("reserve slot as checkout: %v", err)
	}

	deleted := make(chan error, 1)
	go func() {
		deleted <- fixture.store.DeleteAccount(context.Background(), slot.InventoryAccountID)
	}()

	// The delete must still be waiting on the uncommitted slot lock.
	select {
	case err := <-deleted:
		t.Fatalf("delete completed before checkout committed: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	if err := checkout.Commit().Error; err != nil {
		t.Fatalf("commit checkout: %v", err)
	}

	select {
	case err := <-deleted:
		if !errors.Is(err, service.ErrInventoryAccountInUse) {
			t.Fatalf("delete error = %v, want ErrInventoryAccountInUse", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("delete did not finish after checkout committed")
	}

	var account domain.InventoryAccount
	if err := fixture.db.Where("id = ?", slot.InventoryAccountID).First(&account).Error; err != nil {
		t.Fatalf("account was deleted after concurrent reservation: %v", err)
	}
	var stored domain.InventorySlot
	if err := fixture.db.Where("id = ?", slot.ID).First(&stored).Error; err != nil {
		t.Fatalf("slot was deleted after concurrent reservation: %v", err)
	}
	if stored.Status != "reserved" {
		t.Fatalf("slot status = %s, want reserved", stored.Status)
	}
}
