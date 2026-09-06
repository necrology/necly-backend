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
	mu    sync.Mutex
	stock int
	next  int
}

func (r *atomicStockRepository) ReserveCheckout(_ context.Context, command CheckoutCommand) (*domain.Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	wanted := 0
	for _, item := range command.Items {
		wanted += item.Quantity
	}
	if wanted > r.stock {
		return nil, ErrOutOfStock
	}
	r.stock -= wanted
	r.next++
	return &domain.Order{ID: fmt.Sprintf("order-%d", r.next), UserID: command.UserID, Status: domain.OrderPending, Total: int64(wanted) * 10_000, Currency: "IDR", PaymentDueAt: command.Now.Add(15 * time.Minute)}, nil
}
func (r *atomicStockRepository) SavePayment(_ context.Context, _ string, payment *domain.Payment) error {
	return nil
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
