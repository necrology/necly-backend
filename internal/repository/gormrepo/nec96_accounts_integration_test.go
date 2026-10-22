package gormrepo

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
)

func TestNEC96AccountAggregatesAndSlotAccessWindow(t *testing.T) {
	fixture := newCheckoutFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()

	cipher, err := service.NewCredentialCipher("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	svc := service.NewAdminInventoryService(fixture.store, cipher)

	accountExpiry := now.Add(10 * 24 * time.Hour)
	secret := "JBSWY3DPEHPK3PXP"
	account, err := svc.CreateInventoryAccount(ctx, service.CreateInventoryAccountInput{
		ProductID:    fixture.product,
		Credentials:  "buyer@example.test:kata-sandi",
		Label:        "Akun NEC-96",
		SharingLimit: 2,
		ExpiresAt:    &accountExpiry,
		OTPSecret:    &secret,
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	if !account.OTPConfigured {
		t.Fatal("created account should report otp_configured")
	}
	if account.OTPSecretEncrypted != nil {
		t.Fatal("create response exposed the OTP ciphertext")
	}

	var stored domain.InventoryAccount
	if err := fixture.db.First(&stored, "id = ?", account.ID).Error; err != nil {
		t.Fatalf("load stored account: %v", err)
	}
	if len(stored.OTPSecretEncrypted) == 0 {
		t.Fatal("OTP secret was not persisted")
	}
	if bytes.Contains(stored.OTPSecretEncrypted, []byte(secret)) {
		t.Fatal("OTP secret stored in plaintext")
	}

	if _, err := svc.CreateSlots(ctx, service.CreateSlotsInput{
		ProductPriceID:     fixture.price,
		InventoryAccountID: account.ID,
		Quantity:           2,
	}); err != nil {
		t.Fatalf("create slots: %v", err)
	}

	var slots []domain.InventorySlot
	if err := fixture.db.Where("inventory_account_id = ?", account.ID).Order("id").Find(&slots).Error; err != nil {
		t.Fatalf("load slots: %v", err)
	}
	if len(slots) != 2 {
		t.Fatalf("slots = %d, want 2", len(slots))
	}

	makeOrder := func(userID, suffix string, paid bool) (domain.Order, domain.OrderItem) {
		t.Helper()
		order := domain.Order{
			ID: uuid.NewString(), Number: "NEC96-" + suffix, UserID: userID,
			Status: domain.OrderProcessing, Subtotal: 186000, Total: 186000,
			Currency: "IDR", PaymentDueAt: now, IdempotencyKey: "nec96-" + suffix + "-" + uuid.NewString(),
			CreatedAt: now, UpdatedAt: now,
		}
		if err := fixture.db.Create(&order).Error; err != nil {
			t.Fatalf("create order: %v", err)
		}
		item := domain.OrderItem{
			ID: uuid.NewString(), OrderID: order.ID, ProductPriceID: fixture.price,
			ProductName: "Netflix Premium", DurationName: "1 Bulan",
			UnitPrice: 186000, Quantity: 1, DeviceCount: 1, LineTotal: 186000,
		}
		if err := fixture.db.Create(&item).Error; err != nil {
			t.Fatalf("create order item: %v", err)
		}
		if paid {
			payment := domain.Payment{
				ID: uuid.NewString(), OrderID: order.ID, Provider: "manual",
				ProviderPaymentID: "nec96-pay-" + uuid.NewString(), Status: "paid",
				Amount: 186000, PaidAt: &now, CreatedAt: now, UpdatedAt: now,
			}
			if err := fixture.db.Create(&payment).Error; err != nil {
				t.Fatalf("create payment: %v", err)
			}
		}
		return order, item
	}
	orderA, itemA := makeOrder(fixture.userA, "A", true)
	orderB, itemB := makeOrder(fixture.userB, "B", false)

	activatedA := now.Add(-5 * 24 * time.Hour)
	accessA := now.Add(5 * 24 * time.Hour)
	activatedB := now.Add(-1 * 24 * time.Hour)
	accessB := now.Add(2 * 24 * time.Hour)
	slots[0].Status = "reserved"
	slots[0].ReservedBy = &fixture.userA
	slots[0].OrderItemID = &itemA.ID
	slots[0].ActivatedAt = &activatedA
	slots[0].AccessExpiresAt = &accessA
	if err := fixture.db.Save(&slots[0]).Error; err != nil {
		t.Fatalf("assign slot A: %v", err)
	}
	slots[1].Status = "reserved"
	slots[1].ReservedBy = &fixture.userB
	slots[1].OrderItemID = &itemB.ID
	slots[1].ActivatedAt = &activatedB
	slots[1].AccessExpiresAt = &accessB
	if err := fixture.db.Save(&slots[1]).Error; err != nil {
		t.Fatalf("assign slot B: %v", err)
	}

	accounts, _, err := svc.ListInventoryAccounts(ctx, service.InventoryAccountFilter{ProductID: fixture.product}, 1, 100)
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	var got *domain.InventoryAccount
	for i := range accounts {
		if accounts[i].ID == account.ID {
			got = &accounts[i]
		}
	}
	if got == nil {
		t.Fatal("account missing from list")
	}
	if got.AssignedUsers != 2 {
		t.Fatalf("assigned_users = %d, want 2", got.AssignedUsers)
	}
	if got.TotalSlots != 2 || got.UsedSlots != 2 || got.RemainingSlots != 0 {
		t.Fatalf("slot counters total=%d used=%d remaining=%d, want 2/2/0", got.TotalSlots, got.UsedSlots, got.RemainingSlots)
	}
	if !got.OTPConfigured || got.NeedsReview || !got.Sellable {
		t.Fatalf("account flags otp=%t needs_review=%t sellable=%t", got.OTPConfigured, got.NeedsReview, got.Sellable)
	}
	if got.DaysRemaining == nil || *got.DaysRemaining != 10 {
		t.Fatalf("days_remaining = %v, want 10", got.DaysRemaining)
	}
	if got.EncryptedCredentials != nil || got.OTPSecretEncrypted != nil {
		t.Fatal("list response leaked ciphertext")
	}

	assignments, total, err := svc.ListSlots(ctx, account.ID, "", "", 1, 50)
	if err != nil {
		t.Fatalf("list slots: %v", err)
	}
	if total != 2 || len(assignments) != 2 {
		t.Fatalf("assignments = %d/%d, want 2", total, len(assignments))
	}
	var slotA *domain.InventorySlotAssignment
	for i := range assignments {
		if assignments[i].ID == slots[0].ID {
			slotA = &assignments[i]
		}
	}
	if slotA == nil {
		t.Fatal("assigned slot A missing from detail")
	}
	if slotA.OrderID == nil || *slotA.OrderID != orderA.ID {
		t.Fatalf("order id = %v, want %s", slotA.OrderID, orderA.ID)
	}
	if slotA.OrderNumber == nil || *slotA.OrderNumber != orderA.Number {
		t.Fatalf("order number = %v, want %s", slotA.OrderNumber, orderA.Number)
	}
	if slotA.PaymentStatus == nil || *slotA.PaymentStatus != "paid" {
		t.Fatalf("payment status = %v, want paid", slotA.PaymentStatus)
	}
	if slotA.ActivatedAt == nil || slotA.AccessExpiresAt == nil {
		t.Fatal("slot access window was not loaded")
	}
	if slotA.DaysRemaining == nil || *slotA.DaysRemaining != 5 {
		t.Fatalf("slot days_remaining = %v, want 5", slotA.DaysRemaining)
	}

	// A used account cannot be deleted; the existing guard must still see the
	// newly introduced access window as part of the sold seat.
	if err := svc.DeleteInventoryAccount(ctx, account.ID); !errors.Is(err, service.ErrInventoryAccountInUse) {
		t.Fatalf("delete used account error = %v, want ErrInventoryAccountInUse", err)
	}
	_ = orderB
}

func TestNEC96LegacySharingLimitNeedsReviewAndRejectsNewSlots(t *testing.T) {
	fixture := newCheckoutFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()

	legacy := domain.InventoryAccount{
		ID: uuid.NewString(), ProductID: fixture.product, Label: "Akun lama",
		EncryptedCredentials: []byte("cipher"), Status: "active", SharingLimit: 10,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.db.Create(&legacy).Error; err != nil {
		t.Fatalf("create legacy account: %v", err)
	}

	accounts, _, err := fixture.store.ListAccounts(ctx, 1, 100, fixture.product, "")
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	var found *domain.InventoryAccount
	for i := range accounts {
		if accounts[i].ID == legacy.ID {
			found = &accounts[i]
		}
	}
	if found == nil {
		t.Fatal("legacy account missing from list")
	}
	if !found.NeedsReview {
		t.Fatal("legacy account above five users must be flagged needs_review")
	}

	err = fixture.store.CreateSlotsForAccount(ctx, legacy.ID, fixture.product, slotPayload(fixture.price, 1))
	if !errors.Is(err, service.ErrInventoryAccountNeedsReview) {
		t.Fatalf("new slot on legacy account error = %v, want ErrInventoryAccountNeedsReview", err)
	}
	if count := countAccountSlots(t, fixture, legacy.ID); count != 0 {
		t.Fatalf("legacy account slots = %d, want 0", count)
	}
}
