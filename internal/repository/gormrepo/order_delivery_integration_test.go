package gormrepo

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
)

// paidProcessingOrder runs the real checkout reservation and the real status
// transitions so the delivery ledger is exercised against the same slot rows an
// administrator would approve in production.
func paidProcessingOrder(t *testing.T, fixture *checkoutFixture) *domain.Order {
	t.Helper()
	result, err := fixture.checkout(t, fixture.userA, "delivery-ledger-"+time.Now().UTC().Format("150405.000000000"), "")
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	now := time.Now().UTC()
	if _, err := fixture.store.TransitionOrderStatus(context.Background(), result.Order.ID, domain.OrderPaid, now); err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	order, err := fixture.store.TransitionOrderStatus(context.Background(), result.Order.ID, domain.OrderProcessing, now)
	if err != nil {
		t.Fatalf("mark processing: %v", err)
	}
	return order
}

func TestOrderDeliveryLedgerIsIdempotentAcrossEnsurances(t *testing.T) {
	fixture := newCheckoutFixture(t)
	ctx := context.Background()
	order := paidProcessingOrder(t, fixture)

	first, err := fixture.store.EnsureOrderDeliveries(ctx, order.ID, time.Now().UTC())
	if err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	second, err := fixture.store.EnsureOrderDeliveries(ctx, order.ID, time.Now().UTC())
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if len(first) != len(order.Items) || len(second) != len(order.Items) {
		t.Fatalf("delivery rows = %d then %d, want %d", len(first), len(second), len(order.Items))
	}
	if first[0].ID != second[0].ID {
		t.Fatalf("ensure created a second row for the same order item: %s vs %s", first[0].ID, second[0].ID)
	}
	if first[0].Status != domain.DeliveryPending {
		t.Fatalf("initial status = %s, want pending", first[0].Status)
	}

	material, err := fixture.store.LoadDeliveryMaterial(ctx, order.ID, first[0].OrderItemID)
	if err != nil {
		t.Fatalf("load material: %v", err)
	}
	if len(material.Slots) != 1 {
		t.Fatalf("material slots = %d, want 1", len(material.Slots))
	}

	if _, err := fixture.store.GetOrderDelivery(ctx, fixture.userB, first[0].ID); !errors.Is(err, service.ErrDeliveryNotFound) {
		t.Fatalf("cross-order read error = %v, want ErrDeliveryNotFound", err)
	}
}

func TestOrderDeliveryClaimAllowsExactlyOneSender(t *testing.T) {
	fixture := newCheckoutFixture(t)
	ctx := context.Background()
	order := paidProcessingOrder(t, fixture)
	rows, err := fixture.store.EnsureOrderDeliveries(ctx, order.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	deliveryID := rows[0].ID
	now := time.Now().UTC()

	var wg sync.WaitGroup
	claims := make([]bool, 2)
	errs := make([]error, 2)
	for i := range claims {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			claims[index], errs[index] = fixture.store.ClaimOrderDelivery(ctx, deliveryID, now, now.Add(-time.Minute))
		}(i)
	}
	wg.Wait()

	successes := 0
	for i := range claims {
		if errs[i] != nil {
			t.Fatalf("claim %d error = %v", i, errs[i])
		}
		if claims[i] {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent claims succeeded %d times, want exactly 1", successes)
	}

	if err := fixture.store.MarkOrderDeliveryFailed(ctx, deliveryID, service.DeliveryErrorEmailFailed, now); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	// A failed row may be retried, but only after the previous worker has
	// finished. The retry claim is allowed and increments attempts.
	claimed, err := fixture.store.ClaimOrderDelivery(ctx, deliveryID, now.Add(time.Second), now)
	if err != nil || !claimed {
		t.Fatalf("retry claim = %t, %v; want true, nil", claimed, err)
	}
	if err := fixture.store.MarkOrderDeliverySent(ctx, deliveryID, now.Add(2*time.Second)); err != nil {
		t.Fatalf("mark sent: %v", err)
	}
	claimed, err = fixture.store.ClaimOrderDelivery(ctx, deliveryID, now.Add(3*time.Second), now)
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("a sent delivery was claimed again")
	}

	stored, err := fixture.store.GetOrderDelivery(ctx, order.ID, deliveryID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.DeliverySent || stored.Attempts != 2 || stored.SentAt == nil {
		t.Fatalf("stored delivery = %#v, want sent after two attempts", stored)
	}
}

func TestOrderDeliveryStaleSendingLeaseCanBeReclaimed(t *testing.T) {
	fixture := newCheckoutFixture(t)
	ctx := context.Background()
	order := paidProcessingOrder(t, fixture)
	rows, err := fixture.store.EnsureOrderDeliveries(ctx, order.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	deliveryID := rows[0].ID
	now := time.Now().UTC()
	if claimed, err := fixture.store.ClaimOrderDelivery(ctx, deliveryID, now, now.Add(-time.Minute)); err != nil || !claimed {
		t.Fatalf("initial claim = %t, %v", claimed, err)
	}

	// Before the lease expires a second worker must not steal the row.
	if claimed, _ := fixture.store.ClaimOrderDelivery(ctx, deliveryID, now.Add(time.Second), now.Add(-time.Minute)); claimed {
		t.Fatal("fresh sending lease was stolen")
	}
	// Once the row is older than the lease, recovery is allowed.
	claimed, err := fixture.store.ClaimOrderDelivery(ctx, deliveryID, now.Add(10*time.Minute), now.Add(5*time.Minute))
	if err != nil || !claimed {
		t.Fatalf("stale claim = %t, %v; want true, nil", claimed, err)
	}
}
