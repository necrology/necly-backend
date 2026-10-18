package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/necly/services/internal/domain"
)

type atomicStockRepository struct {
	mu       sync.Mutex
	stock    int
	next     int
	attempts map[string]*domain.CheckoutAttempt
	orders   map[string]*domain.Order
	payments map[string]*domain.Payment
}

func (r *atomicStockRepository) key(userID, key string) string { return userID + "|" + key }

func (r *atomicStockRepository) BeginCheckout(_ context.Context, command CheckoutCommand) (*CheckoutReservation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.attempts == nil {
		r.attempts = make(map[string]*domain.CheckoutAttempt)
		r.orders = make(map[string]*domain.Order)
		r.payments = make(map[string]*domain.Payment)
	}
	key := r.key(command.UserID, command.IdempotencyKey)
	if order, ok := r.orders[key]; ok {
		return &CheckoutReservation{State: CheckoutReplay, Order: order, Payment: r.payments[key], Attempt: r.attempts[key]}, nil
	}
	if attempt, ok := r.attempts[key]; ok {
		return &CheckoutReservation{State: CheckoutInProgress, Attempt: attempt}, nil
	}
	wanted := 0
	for _, item := range command.Items {
		wanted += item.Quantity
	}
	if wanted > r.stock {
		return nil, ErrOutOfStock
	}
	r.stock -= wanted
	r.next++
	order := &domain.Order{ID: fmt.Sprintf("order-%d", r.next), Number: fmt.Sprintf("ORD-%d", r.next), UserID: command.UserID, Status: domain.OrderPending, Total: int64(wanted) * 10_000, Currency: "IDR", PaymentDueAt: command.Now.Add(15 * time.Minute)}
	attempt := &domain.CheckoutAttempt{ID: fmt.Sprintf("attempt-%d", r.next), UserID: command.UserID, IdempotencyKey: command.IdempotencyKey, RequestFingerprint: command.Fingerprint, State: "reserved", OrderID: &order.ID}
	r.orders[key] = order
	r.attempts[key] = attempt
	return &CheckoutReservation{State: CheckoutReserved, Order: order, Attempt: attempt}, nil
}

func (r *atomicStockRepository) ClaimProviderCall(_ context.Context, _ string, _ time.Time) (bool, error) {
	return true, nil
}

func (r *atomicStockRepository) SaveProviderResult(_ context.Context, attemptID, provider, providerPaymentID, checkoutURL string, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, attempt := range r.attempts {
		if attempt.ID == attemptID {
			attempt.Provider = &provider
			attempt.ProviderPaymentID = &providerPaymentID
			attempt.CheckoutURL = &checkoutURL
			attempt.State = "provider_created"
		}
	}
	return nil
}

func (r *atomicStockRepository) CompleteCheckout(_ context.Context, attemptID string, payment *domain.Payment) (*domain.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, attempt := range r.attempts {
		if attempt.ID != attemptID {
			continue
		}
		attempt.State = "completed"
		if order, ok := r.orders[key]; ok {
			payment.OrderID = order.ID
		}
		r.payments[key] = payment
	}
	return payment, nil
}

func (r *atomicStockRepository) FailCheckout(_ context.Context, _ string, _ string, _ time.Time) error {
	return nil
}

func (r *atomicStockRepository) GetCheckoutProgress(_ context.Context, userID, idempotencyKey string) (*CheckoutProgress, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := r.key(userID, idempotencyKey)
	progress := &CheckoutProgress{Order: r.orders[key], Payment: r.payments[key], Attempt: r.attempts[key]}
	return progress, nil
}

func (r *atomicStockRepository) CancelAndRelease(_ context.Context, _ string, _ time.Time) error {
	return nil
}

type fakeCheckoutProvider struct{ calls atomic.Int32 }

func (p *fakeCheckoutProvider) Name() string { return "test" }
func (p *fakeCheckoutProvider) CreatePayment(_ context.Context, req PaymentRequest) (*PaymentInitiation, error) {
	p.calls.Add(1)
	return &PaymentInitiation{ProviderPaymentID: "pay-" + req.OrderID, Status: "pending", CheckoutURL: "https://pay.test/" + req.OrderID}, nil
}
func (p *fakeCheckoutProvider) VerifyWebhook(_ map[string]string, _ []byte) (*PaymentEvent, error) {
	return nil, nil
}

func TestCheckoutConcurrentRequestsCannotOversell(t *testing.T) {
	repo := &atomicStockRepository{stock: 1}
	provider := &fakeCheckoutProvider{}
	checkout := NewCheckoutService(repo, provider, 15*time.Minute)
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(n int) {
			<-start
			_, err := checkout.Checkout(context.Background(), CheckoutInput{UserID: fmt.Sprintf("user-%d", n), IdempotencyKey: fmt.Sprintf("key-%d", n), Items: []CheckoutItemInput{{ProductPriceID: "price-1", Quantity: 1}}})
			results <- err
		}(i)
	}
	close(start)
	var success, outOfStock int
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			success++
		} else if err == ErrOutOfStock {
			outOfStock++
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if success != 1 || outOfStock != 1 || repo.stock != 0 {
		t.Fatalf("success=%d outOfStock=%d stock=%d", success, outOfStock, repo.stock)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("payment calls = %d, want 1", provider.calls.Load())
	}
}

func TestCheckoutRejectsInvalidInputBeforeRepository(t *testing.T) {
	repo := &atomicStockRepository{stock: 2}
	checkout := NewCheckoutService(repo, &fakeCheckoutProvider{}, 15*time.Minute)
	cases := []CheckoutInput{
		{UserID: "", IdempotencyKey: "key", Items: []CheckoutItemInput{{ProductPriceID: "price", Quantity: 1}}},
		{UserID: "user", IdempotencyKey: "", Items: []CheckoutItemInput{{ProductPriceID: "price", Quantity: 1}}},
		{UserID: "user", IdempotencyKey: "key", Items: nil},
		{UserID: "user", IdempotencyKey: "key", Items: []CheckoutItemInput{{ProductPriceID: "price", Quantity: 0}}},
	}
	for _, input := range cases {
		if _, err := checkout.Checkout(context.Background(), input); err == nil {
			t.Fatalf("Checkout(%#v) accepted invalid input", input)
		}
	}
	if repo.stock != 2 {
		t.Fatal("repository was called for invalid input")
	}
}

type leaseRecoveryRepository struct {
	attempt *domain.CheckoutAttempt
	order   *domain.Order
	payment *domain.Payment
	claims  int
}

func (r *leaseRecoveryRepository) BeginCheckout(_ context.Context, _ CheckoutCommand) (*CheckoutReservation, error) {
	return &CheckoutReservation{State: CheckoutInProgress, Attempt: r.attempt}, nil
}

func (r *leaseRecoveryRepository) ClaimProviderCall(_ context.Context, _ string, _ time.Time) (bool, error) {
	r.claims++
	return true, nil
}

func (r *leaseRecoveryRepository) SaveProviderResult(_ context.Context, _, _, _, _ string, _ time.Time) error {
	return nil
}

func (r *leaseRecoveryRepository) CompleteCheckout(_ context.Context, _ string, payment *domain.Payment) (*domain.Payment, error) {
	r.payment = payment
	return payment, nil
}

func (r *leaseRecoveryRepository) FailCheckout(_ context.Context, _, _ string, _ time.Time) error {
	return nil
}

func (r *leaseRecoveryRepository) GetCheckoutProgress(_ context.Context, _, _ string) (*CheckoutProgress, error) {
	return &CheckoutProgress{Order: r.order, Payment: r.payment, Attempt: r.attempt}, nil
}

func (r *leaseRecoveryRepository) CancelAndRelease(_ context.Context, _ string, _ time.Time) error {
	return nil
}

func TestWaiterRecoversStaleReservationWithoutDoubleClaim(t *testing.T) {
	now := time.Now().UTC()
	order := &domain.Order{
		ID: "order-lease", Number: "ORD-lease", UserID: "user-lease", Status: domain.OrderPending,
		Total: 10000, Currency: "IDR", PaymentDueAt: now.Add(time.Minute),
	}
	attempt := &domain.CheckoutAttempt{
		ID: "attempt-lease", UserID: order.UserID, IdempotencyKey: "key-lease",
		RequestFingerprint: "fixture", State: "reserved", OrderID: &order.ID,
		UpdatedAt: now.Add(-2 * time.Minute),
	}
	repo := &leaseRecoveryRepository{attempt: attempt, order: order}
	provider := &fakeCheckoutProvider{}
	checkout := NewCheckoutService(repo, provider, 15*time.Minute)

	result, err := checkout.Checkout(context.Background(), CheckoutInput{
		UserID: order.UserID, IdempotencyKey: attempt.IdempotencyKey,
		Items: []CheckoutItemInput{{ProductPriceID: "price-1", Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if result == nil || result.Payment.ID == "" {
		t.Fatalf("recovery returned no payment: %#v", result)
	}
	if repo.claims != 1 {
		t.Fatalf("claims = %d, want 1 (the waiter's claim must be reused)", repo.claims)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls.Load())
	}
}
