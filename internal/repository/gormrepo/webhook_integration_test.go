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

func paymentEventFor(result *service.CheckoutResult, id, status string) service.PaymentEvent {
	return service.PaymentEvent{
		ID:                id,
		Provider:          result.Payment.Provider,
		ProviderPaymentID: result.Payment.ProviderPaymentID,
		OrderID:           result.Order.ID,
		Status:            status,
		Amount:            result.Order.Total,
		Currency:          result.Order.Currency,
		PaidAt:            time.Now().UTC(),
		Raw:               []byte(`{"fixture":"` + id + `"}`),
	}
}

func loadOrder(t *testing.T, fixture *checkoutFixture, orderID string) domain.Order {
	t.Helper()
	var order domain.Order
	if err := fixture.db.Where("id = ?", orderID).First(&order).Error; err != nil {
		t.Fatalf("load order: %v", err)
	}
	return order
}

func loadPayment(t *testing.T, fixture *checkoutFixture, orderID string) domain.Payment {
	t.Helper()
	var payment domain.Payment
	if err := fixture.db.Where("order_id = ?", orderID).Order("created_at DESC, id DESC").First(&payment).Error; err != nil {
		t.Fatalf("load payment: %v", err)
	}
	return payment
}

func TestPaymentEventPendingToPaidIsAppliedOnce(t *testing.T) {
	fixture := newCheckoutFixture(t)
	result, err := fixture.checkout(t, fixture.userA, "fixture-webhook-paid", "")
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}

	event := paymentEventFor(result, "evt-paid-1", "paid")
	applied, err := fixture.store.ApplyPaymentEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("apply paid event: %v", err)
	}
	if !applied {
		t.Fatal("first paid event was not applied")
	}

	order := loadOrder(t, fixture, result.Order.ID)
	payment := loadPayment(t, fixture, result.Order.ID)
	if order.Status != domain.OrderPaid {
		t.Fatalf("order status = %s, want paid", order.Status)
	}
	if payment.Status != "paid" || payment.PaidAt == nil {
		t.Fatalf("payment = %#v, want paid with paid_at", payment)
	}
	if reserved := countReservedSlots(t, fixture.db); reserved != 1 {
		t.Fatalf("reserved slots = %d, want 1 (paid stock stays allocated)", reserved)
	}
	if events := countRows(t, fixture.db, &domain.WebhookEvent{}); events != 1 {
		t.Fatalf("webhook events = %d, want 1", events)
	}

	applied, err = fixture.store.ApplyPaymentEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("replay paid event: %v", err)
	}
	if applied {
		t.Fatal("identical paid event was applied twice")
	}
	if events := countRows(t, fixture.db, &domain.WebhookEvent{}); events != 1 {
		t.Fatalf("webhook events after replay = %d, want 1", events)
	}
	if reserved := countReservedSlots(t, fixture.db); reserved != 1 {
		t.Fatalf("reserved slots after replay = %d, want 1", reserved)
	}
}

func TestStaleExpiryCannotUndoPaidOrder(t *testing.T) {
	fixture := newCheckoutFixture(t)
	result, err := fixture.checkout(t, fixture.userA, "fixture-webhook-stale", "")
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if _, err := fixture.store.ApplyPaymentEvent(context.Background(), paymentEventFor(result, "evt-stale-paid", "paid")); err != nil {
		t.Fatalf("apply paid: %v", err)
	}

	// A different event key is deliberate: this is a delayed expiry
	// notification, not a duplicate of the paid notification.
	applied, err := fixture.store.ApplyPaymentEvent(context.Background(), paymentEventFor(result, "evt-stale-expired", "expired"))
	if err != nil {
		t.Fatalf("apply stale expiry: %v", err)
	}
	if !applied {
		t.Fatal("stale expiry was not acknowledged")
	}

	order := loadOrder(t, fixture, result.Order.ID)
	payment := loadPayment(t, fixture, result.Order.ID)
	if order.Status != domain.OrderPaid {
		t.Fatalf("stale expiry changed order status to %s", order.Status)
	}
	if payment.Status != "paid" {
		t.Fatalf("stale expiry changed payment status to %s", payment.Status)
	}
	if reserved := countReservedSlots(t, fixture.db); reserved != 1 {
		t.Fatalf("reserved slots = %d, want 1 (stale expiry must not release paid stock)", reserved)
	}
}

func TestRefundDoesNotReleasePaidStock(t *testing.T) {
	fixture := newCheckoutFixture(t)
	result, err := fixture.checkout(t, fixture.userA, "fixture-webhook-refund", "")
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if _, err := fixture.store.ApplyPaymentEvent(context.Background(), paymentEventFor(result, "evt-refund-paid", "paid")); err != nil {
		t.Fatalf("apply paid: %v", err)
	}
	if _, err := fixture.store.ApplyPaymentEvent(context.Background(), paymentEventFor(result, "evt-refund-refunded", "refunded")); err != nil {
		t.Fatalf("apply refund: %v", err)
	}

	order := loadOrder(t, fixture, result.Order.ID)
	payment := loadPayment(t, fixture, result.Order.ID)
	if order.Status != domain.OrderRefunded || payment.Status != "refunded" {
		t.Fatalf("order/payment = %s/%s, want refunded/refunded", order.Status, payment.Status)
	}
	if reserved := countReservedSlots(t, fixture.db); reserved != 1 {
		t.Fatalf("reserved slots = %d, want 1 (refund must not release stock automatically)", reserved)
	}
}

func TestPaymentEventMismatchDoesNotMutateState(t *testing.T) {
	fixture := newCheckoutFixture(t)
	result, err := fixture.checkout(t, fixture.userA, "fixture-webhook-mismatch", "")
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*service.PaymentEvent)
	}{
		{"wrong amount", func(event *service.PaymentEvent) { event.Amount++ }},
		{"wrong currency", func(event *service.PaymentEvent) { event.Currency = "USD" }},
		{"wrong payment identity", func(event *service.PaymentEvent) { event.ProviderPaymentID = "someone-else" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := paymentEventFor(result, "evt-mismatch-"+tc.name, "paid")
			tc.mutate(&event)
			_, err := fixture.store.ApplyPaymentEvent(context.Background(), event)
			if !errors.Is(err, service.ErrPaymentMismatch) {
				t.Fatalf("error = %v, want ErrPaymentMismatch", err)
			}
		})
	}

	order := loadOrder(t, fixture, result.Order.ID)
	payment := loadPayment(t, fixture, result.Order.ID)
	if order.Status != domain.OrderPending || payment.Status != "pending" {
		t.Fatalf("mismatched events mutated order/payment: %s/%s", order.Status, payment.Status)
	}
	if reserved := countReservedSlots(t, fixture.db); reserved != 1 {
		t.Fatalf("reserved slots = %d, want 1", reserved)
	}
	if events := countRows(t, fixture.db, &domain.WebhookEvent{}); events != 0 {
		t.Fatalf("mismatched events left %d webhook rows, want 0", events)
	}
}

func TestPaymentEventWrongOrderDoesNotMutateEitherOrder(t *testing.T) {
	fixture := newCheckoutFixture(t)
	first, err := fixture.checkout(t, fixture.userA, "fixture-webhook-order-a", "")
	if err != nil {
		t.Fatalf("checkout A: %v", err)
	}
	second, err := fixture.checkout(t, fixture.userB, "fixture-webhook-order-b", "")
	if err != nil {
		t.Fatalf("checkout B: %v", err)
	}

	// The provider identity belongs to order A but the callback claims B.
	event := paymentEventFor(second, "evt-wrong-order", "paid")
	event.ProviderPaymentID = first.Payment.ProviderPaymentID
	if _, err := fixture.store.ApplyPaymentEvent(context.Background(), event); !errors.Is(err, service.ErrPaymentMismatch) {
		t.Fatalf("error = %v, want ErrPaymentMismatch", err)
	}
	if loadOrder(t, fixture, first.Order.ID).Status != domain.OrderPending {
		t.Fatal("order A was mutated by a callback for B")
	}
	if loadOrder(t, fixture, second.Order.ID).Status != domain.OrderPending {
		t.Fatal("order B was mutated by a mismatched payment identity")
	}
	if events := countRows(t, fixture.db, &domain.WebhookEvent{}); events != 0 {
		t.Fatalf("mismatched event left %d webhook rows, want 0", events)
	}
}

func TestConcurrentPaymentDeliveryIsAppliedOnce(t *testing.T) {
	fixture := newCheckoutFixture(t)
	result, err := fixture.checkout(t, fixture.userA, "fixture-webhook-concurrent", "")
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	event := paymentEventFor(result, "evt-concurrent-paid", "paid")

	const workers = 8
	var waitGroup sync.WaitGroup
	applied := make([]bool, workers)
	errs := make([]error, workers)
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func(slot int) {
			defer waitGroup.Done()
			applied[slot], errs[slot] = fixture.store.ApplyPaymentEvent(context.Background(), event)
		}(index)
	}
	waitGroup.Wait()

	appliedCount := 0
	for index := range errs {
		if errs[index] != nil {
			t.Fatalf("worker %d: %v", index, errs[index])
		}
		if applied[index] {
			appliedCount++
		}
	}
	if appliedCount != 1 {
		t.Fatalf("applied count = %d, want 1", appliedCount)
	}
	if events := countRows(t, fixture.db, &domain.WebhookEvent{}); events != 1 {
		t.Fatalf("webhook events = %d, want 1", events)
	}
	if order := loadOrder(t, fixture, result.Order.ID); order.Status != domain.OrderPaid {
		t.Fatalf("order status = %s, want paid", order.Status)
	}
	if reserved := countReservedSlots(t, fixture.db); reserved != 1 {
		t.Fatalf("reserved slots = %d, want 1", reserved)
	}
}

// Midtrans notifications carry transaction_id, while the payment row stores the
// Snap token. The order id is the binding, and a valid amount/currency must
// still match; this regression keeps that special case explicit.
func TestMidtransPaymentEventBindsByOrderID(t *testing.T) {
	fixture := newCheckoutFixture(t)
	provider := &stubProvider{name: "midtrans"}
	svc := fixture.service(provider)
	result, err := svc.Checkout(context.Background(), fixture.input(fixture.userA, "fixture-webhook-midtrans", ""))
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	event := paymentEventFor(result, "evt-midtrans-paid", "paid")
	event.ProviderPaymentID = "midtrans-transaction-id"
	if applied, err := fixture.store.ApplyPaymentEvent(context.Background(), event); err != nil || !applied {
		t.Fatalf("apply midtrans event = %t, %v", applied, err)
	}
	if order := loadOrder(t, fixture, result.Order.ID); order.Status != domain.OrderPaid {
		t.Fatalf("order status = %s, want paid", order.Status)
	}
}
