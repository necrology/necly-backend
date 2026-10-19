package gormrepo

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
)

func newInventoryAccount(t *testing.T, fixture *checkoutFixture, productID string, limit int, status string, expiresAt *time.Time) *domain.InventoryAccount {
	t.Helper()
	now := time.Now().UTC()
	account := &domain.InventoryAccount{
		ID:                   uuid.NewString(),
		ProductID:            productID,
		Label:                "Akun uji",
		EncryptedCredentials: []byte("fixture"),
		Status:               status,
		SharingLimit:         limit,
		ExpiresAt:            expiresAt,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if err := fixture.db.Create(account).Error; err != nil {
		t.Fatalf("create inventory account: %v", err)
	}
	return account
}

func seedInventorySlots(t *testing.T, fixture *checkoutFixture, accountID, priceID string, count int, status string) {
	t.Helper()
	now := time.Now().UTC()
	slots := make([]domain.InventorySlot, 0, count)
	for i := 0; i < count; i++ {
		slots = append(slots, domain.InventorySlot{
			ID: uuid.NewString(), InventoryAccountID: accountID, ProductPriceID: priceID,
			Status: status, CreatedAt: now, UpdatedAt: now,
		})
	}
	if err := fixture.db.Create(&slots).Error; err != nil {
		t.Fatalf("seed slots: %v", err)
	}
}

func slotPayload(priceID string, count int) []domain.InventorySlot {
	slots := make([]domain.InventorySlot, 0, count)
	for i := 0; i < count; i++ {
		slots = append(slots, domain.InventorySlot{ProductPriceID: priceID, Status: "available"})
	}
	return slots
}

func countAccountSlots(t *testing.T, fixture *checkoutFixture, accountID string) int64 {
	t.Helper()
	var count int64
	if err := fixture.db.Model(&domain.InventorySlot{}).Where("inventory_account_id = ?", accountID).Count(&count).Error; err != nil {
		t.Fatalf("count slots: %v", err)
	}
	return count
}

func TestCreateSlotsForAccountEnforcesSharingLimit(t *testing.T) {
	fixture := newCheckoutFixture(t)
	ctx := context.Background()
	account := newInventoryAccount(t, fixture, fixture.product, 2, "active", nil)

	if err := fixture.store.CreateSlotsForAccount(ctx, account.ID, fixture.product, slotPayload(fixture.price, 2)); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	if err := fixture.store.CreateSlotsForAccount(ctx, account.ID, fixture.product, slotPayload(fixture.price, 1)); !errors.Is(err, service.ErrSharingLimitExceeded) {
		t.Fatalf("oversell error = %v, want ErrSharingLimitExceeded", err)
	}
	if count := countAccountSlots(t, fixture, account.ID); count != 2 {
		t.Fatalf("slots = %d, want 2 after rejected oversell", count)
	}
}

func TestCreateSlotsForAccountRejectsMismatchedProduct(t *testing.T) {
	fixture := newCheckoutFixture(t)
	account := newInventoryAccount(t, fixture, fixture.product, 2, "active", nil)

	err := fixture.store.CreateSlotsForAccount(context.Background(), account.ID, uuid.NewString(), slotPayload(fixture.price, 1))

	if !errors.Is(err, service.ErrInventoryAccountMismatch) {
		t.Fatalf("error = %v, want ErrInventoryAccountMismatch", err)
	}
	if count := countAccountSlots(t, fixture, account.ID); count != 0 {
		t.Fatalf("slots = %d, want 0", count)
	}
}

func TestCreateSlotsForAccountRejectsUnavailableAccount(t *testing.T) {
	fixture := newCheckoutFixture(t)
	ctx := context.Background()

	inactive := newInventoryAccount(t, fixture, fixture.product, 5, "inactive", nil)
	if err := fixture.store.CreateSlotsForAccount(ctx, inactive.ID, fixture.product, slotPayload(fixture.price, 1)); !errors.Is(err, service.ErrInventoryAccountUnavailable) {
		t.Fatalf("inactive error = %v, want ErrInventoryAccountUnavailable", err)
	}

	past := time.Now().UTC().Add(-time.Hour)
	expired := newInventoryAccount(t, fixture, fixture.product, 5, "active", &past)
	if err := fixture.store.CreateSlotsForAccount(ctx, expired.ID, fixture.product, slotPayload(fixture.price, 1)); !errors.Is(err, service.ErrInventoryAccountUnavailable) {
		t.Fatalf("expired error = %v, want ErrInventoryAccountUnavailable", err)
	}
}

func TestCreateSlotsForAccountIsConcurrencySafe(t *testing.T) {
	fixture := newCheckoutFixture(t)
	ctx := context.Background()
	account := newInventoryAccount(t, fixture, fixture.product, 1, "active", nil)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			errs[index] = fixture.store.CreateSlotsForAccount(ctx, account.ID, fixture.product, slotPayload(fixture.price, 1))
		}(i)
	}
	wg.Wait()

	successes, limitRejections := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, service.ErrSharingLimitExceeded):
			limitRejections++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 || limitRejections != 1 {
		t.Fatalf("successes=%d limitRejections=%d, want 1/1", successes, limitRejections)
	}
	if count := countAccountSlots(t, fixture, account.ID); count != 1 {
		t.Fatalf("slots = %d, want 1", count)
	}
}

func TestCreateSlotsAutoPicksAccountWithSpareCapacity(t *testing.T) {
	fixture := newCheckoutFixture(t)
	ctx := context.Background()

	// The fixture account is already full (limit 3, 3 slots). A full account and
	// one with spare capacity must route the new slots to the latter.
	full := newInventoryAccount(t, fixture, fixture.product, 1, "active", nil)
	seedInventorySlots(t, fixture, full.ID, fixture.price, 1, "available")
	spare := newInventoryAccount(t, fixture, fixture.product, 5, "active", nil)

	chosen, err := fixture.store.CreateSlotsAuto(ctx, fixture.product, slotPayload(fixture.price, 3))
	if err != nil {
		t.Fatalf("CreateSlotsAuto: %v", err)
	}
	if chosen != spare.ID {
		t.Fatalf("chosen account = %s, want %s", chosen, spare.ID)
	}
	if count := countAccountSlots(t, fixture, spare.ID); count != 3 {
		t.Fatalf("spare account slots = %d, want 3", count)
	}
}

func TestCreateSlotsAutoRejectsWhenEveryAccountIsFull(t *testing.T) {
	fixture := newCheckoutFixture(t)

	// The fixture account has limit 3 and exactly 3 slots.
	if _, err := fixture.store.CreateSlotsAuto(context.Background(), fixture.product, slotPayload(fixture.price, 1)); !errors.Is(err, service.ErrSharingLimitExceeded) {
		t.Fatalf("error = %v, want ErrSharingLimitExceeded", err)
	}
}

func TestCreateSlotsAutoNeedsAnActiveAccount(t *testing.T) {
	fixture := newCheckoutFixture(t)

	if _, err := fixture.store.CreateSlotsAuto(context.Background(), uuid.NewString(), slotPayload(fixture.price, 1)); !errors.Is(err, service.ErrAccountRequired) {
		t.Fatalf("error = %v, want ErrAccountRequired", err)
	}
}

func TestListAccountsReportsSlotCounters(t *testing.T) {
	fixture := newCheckoutFixture(t)
	ctx := context.Background()
	account := newInventoryAccount(t, fixture, fixture.product, 5, "active", nil)
	seedInventorySlots(t, fixture, account.ID, fixture.price, 2, "available")
	seedInventorySlots(t, fixture, account.ID, fixture.price, 1, "reserved")

	accounts, total, err := fixture.store.ListAccounts(ctx, 1, 50, fixture.product, "")
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if int(total) != len(accounts) {
		t.Fatalf("total = %d, len = %d", total, len(accounts))
	}
	var found *domain.InventoryAccount
	for i := range accounts {
		if accounts[i].ID == account.ID {
			found = &accounts[i]
		}
	}
	if found == nil {
		t.Fatal("new account missing from list")
	}
	if found.TotalSlots != 3 || found.AvailableSlots != 2 || found.UsedSlots != 1 {
		t.Fatalf("counters total=%d available=%d used=%d, want 3/2/1", found.TotalSlots, found.AvailableSlots, found.UsedSlots)
	}
	if found.RemainingSlots != 2 {
		t.Fatalf("remaining = %d, want 2", found.RemainingSlots)
	}
	if !found.Sellable {
		t.Fatal("active non-expired account should be sellable")
	}
	if found.EncryptedCredentials != nil {
		t.Fatal("ListAccounts returned ciphertext")
	}
}

func TestListAccountsFiltersByStatus(t *testing.T) {
	fixture := newCheckoutFixture(t)
	ctx := context.Background()
	inactive := newInventoryAccount(t, fixture, fixture.product, 1, "inactive", nil)

	accounts, total, err := fixture.store.ListAccounts(ctx, 1, 50, fixture.product, "inactive")
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if total != 1 || len(accounts) != 1 || accounts[0].ID != inactive.ID {
		t.Fatalf("inactive filter = %d %#v, want only %s", total, accounts, inactive.ID)
	}
	if accounts[0].Sellable {
		t.Fatal("inactive account reported as sellable")
	}
}

func TestListSlotAssignmentsJoinsOrderAndCustomer(t *testing.T) {
	fixture := newCheckoutFixture(t)
	ctx := context.Background()
	result, err := fixture.checkout(t, fixture.userA, "w15-slot-assignments", "")
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}

	var slot domain.InventorySlot
	if err := fixture.db.Where("status = ? AND order_item_id IS NOT NULL", "reserved").First(&slot).Error; err != nil {
		t.Fatalf("load reserved slot: %v", err)
	}

	assignments, total, err := fixture.store.ListSlotAssignments(ctx, slot.InventoryAccountID, "", "reserved", 1, 50)
	if err != nil {
		t.Fatalf("ListSlotAssignments: %v", err)
	}
	if total != 1 || len(assignments) != 1 {
		t.Fatalf("assignments = %d/%d, want 1", total, len(assignments))
	}
	got := assignments[0]
	if got.OrderID == nil || *got.OrderID != result.Order.ID {
		t.Fatalf("order id = %v, want %s", got.OrderID, result.Order.ID)
	}
	if got.OrderNumber == nil || *got.OrderNumber != result.Order.Number {
		t.Fatalf("order number = %v, want %s", got.OrderNumber, result.Order.Number)
	}
	if got.ReservedEmail == nil || *got.ReservedEmail != "fixture.customer@example.test" {
		t.Fatalf("reserved email = %v, want customer email", got.ReservedEmail)
	}
	if got.ReservedName == nil || *got.ReservedName != "Fixture Customer" {
		t.Fatalf("reserved name = %v, want customer name", got.ReservedName)
	}
}

func TestProductExists(t *testing.T) {
	fixture := newCheckoutFixture(t)
	ctx := context.Background()

	if exists, err := fixture.store.ProductExists(ctx, fixture.product); err != nil || !exists {
		t.Fatalf("existing product = %t, %v; want true", exists, err)
	}
	if exists, err := fixture.store.ProductExists(ctx, uuid.NewString()); err != nil || exists {
		t.Fatalf("unknown product = %t, %v; want false", exists, err)
	}
}

func TestServiceUpdateRejectsSharingLimitBelowProvisionedSlots(t *testing.T) {
	fixture := newCheckoutFixture(t)
	ctx := context.Background()
	account := newInventoryAccount(t, fixture, fixture.product, 3, "active", nil)
	seedInventorySlots(t, fixture, account.ID, fixture.price, 3, "available")

	cipher, err := service.NewCredentialCipher("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	svc := service.NewAdminInventoryService(fixture.store, cipher)

	limit := 2
	if _, err := svc.UpdateInventoryAccount(ctx, account.ID, service.UpdateInventoryAccountInput{SharingLimit: &limit}); !errors.Is(err, service.ErrSharingLimitExceeded) {
		t.Fatalf("error = %v, want ErrSharingLimitExceeded", err)
	}
	if count := countAccountSlots(t, fixture, account.ID); count != 3 {
		t.Fatalf("slots = %d, want 3 unchanged", count)
	}

	limit = 4
	updated, err := svc.UpdateInventoryAccount(ctx, account.ID, service.UpdateInventoryAccountInput{SharingLimit: &limit})
	if err != nil {
		t.Fatalf("raise limit: %v", err)
	}
	if updated.SharingLimit != 4 {
		t.Fatalf("sharing_limit = %d, want 4", updated.SharingLimit)
	}
}
