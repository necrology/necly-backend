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

func createArchiveOrder(t *testing.T, fixture *checkoutFixture, userID string, status domain.OrderStatus) *domain.Order {
	t.Helper()
	now := time.Now().UTC().Add(-time.Hour)
	order := &domain.Order{
		ID: uuid.NewString(), Number: "ARCH-" + uuid.NewString(), UserID: userID,
		Status: status, Subtotal: 10000, Total: 10000, Currency: "IDR",
		PaymentDueAt: now, IdempotencyKey: uuid.NewString(), CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.db.Create(order).Error; err != nil {
		t.Fatalf("seed order: %v", err)
	}
	return order
}

func TestArchiveOrderOwnerOnlyTerminalAndIdempotent(t *testing.T) {
	fixture := newCheckoutFixture(t)
	ctx := context.Background()
	pending := createArchiveOrder(t, fixture, fixture.userA, domain.OrderPending)
	terminal := createArchiveOrder(t, fixture, fixture.userA, domain.OrderCompleted)
	delivered := createArchiveOrder(t, fixture, fixture.userA, domain.OrderDelivered)
	active := createArchiveOrder(t, fixture, fixture.userA, domain.OrderProcessing)
	foreign := createArchiveOrder(t, fixture, fixture.userB, domain.OrderCancelled)
	archiveTime := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)

	payment := domain.Payment{
		ID: uuid.NewString(), OrderID: terminal.ID, Provider: "midtrans",
		ProviderPaymentID: "archive-" + uuid.NewString(), Status: "paid",
		Amount: 10000, CreatedAt: archiveTime.Add(-time.Hour), UpdatedAt: archiveTime.Add(-time.Hour),
	}
	if err := fixture.db.Create(&payment).Error; err != nil {
		t.Fatalf("seed payment: %v", err)
	}

	if err := fixture.store.ArchiveOrder(ctx, fixture.userA, pending.ID, archiveTime); err != nil {
		t.Fatalf("archive pending-payment order: %v", err)
	}
	if err := fixture.store.ArchiveOrder(ctx, fixture.userA, terminal.ID, archiveTime); err != nil {
		t.Fatalf("archive terminal order: %v", err)
	}
	var archived domain.Order
	if err := fixture.db.First(&archived, "id = ?", terminal.ID).Error; err != nil {
		t.Fatal(err)
	}
	if archived.ArchivedAt == nil || !archived.ArchivedAt.Equal(archiveTime) {
		t.Fatalf("archived_at = %v, want %s", archived.ArchivedAt, archiveTime)
	}
	updatedAt := archived.UpdatedAt
	if err := fixture.store.ArchiveOrder(ctx, fixture.userA, delivered.ID, archiveTime); err != nil {
		t.Fatalf("archive delivered order: %v", err)
	}

	if err := fixture.store.ArchiveOrder(ctx, fixture.userA, terminal.ID, archiveTime.Add(time.Hour)); err != nil {
		t.Fatalf("idempotent archive: %v", err)
	}
	if err := fixture.db.First(&archived, "id = ?", terminal.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !archived.ArchivedAt.Equal(archiveTime) || !archived.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("retry mutated archived order: %+v", archived)
	}

	orders, total, err := fixture.store.ListUserOrders(ctx, fixture.userA, 1, 20)
	if err != nil {
		t.Fatalf("list orders: %v", err)
	}
	if total != 1 || len(orders) != 1 || orders[0].ID != active.ID {
		t.Fatalf("active history = %+v (total %d), want only %s", orders, total, active.ID)
	}

	// The detail read stays available to the owner, and related financial rows
	// are untouched by a soft archive.
	if _, err := fixture.store.GetOrder(ctx, fixture.userA, terminal.ID); err != nil {
		t.Fatalf("archived detail read: %v", err)
	}
	var payments int64
	if err := fixture.db.Model(&domain.Payment{}).Where("order_id = ?", terminal.ID).Count(&payments).Error; err != nil {
		t.Fatal(err)
	}
	if payments != 1 {
		t.Fatalf("payments = %d, want the original row kept", payments)
	}

	if err := fixture.store.ArchiveOrder(ctx, fixture.userA, active.ID, archiveTime); !errors.Is(err, service.ErrOrderArchiveNotAllowed) {
		t.Fatalf("archive active error = %v, want ErrOrderArchiveNotAllowed", err)
	}
	if err := fixture.store.ArchiveOrder(ctx, fixture.userA, foreign.ID, archiveTime); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("archive foreign error = %v, want ErrNotFound", err)
	}
	if err := fixture.store.ArchiveOrder(ctx, fixture.userA, uuid.NewString(), archiveTime); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("archive missing error = %v, want ErrNotFound", err)
	}
}
