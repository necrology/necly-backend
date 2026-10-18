package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/necly/services/internal/domain"
)

type hmacTestProvider struct{ secret []byte }

func (p hmacTestProvider) Name() string { return "testpay" }
func (p hmacTestProvider) CreatePayment(context.Context, PaymentRequest) (*PaymentInitiation, error) {
	return nil, errors.New("unused")
}
func (p hmacTestProvider) VerifyWebhook(headers map[string]string, body []byte) (*PaymentEvent, error) {
	provided, err := hex.DecodeString(headers["X-Signature"])
	if err != nil {
		return nil, ErrInvalidWebhookSignature
	}
	mac := hmac.New(sha256.New, p.secret)
	_, _ = mac.Write(body)
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return nil, ErrInvalidWebhookSignature
	}
	return &PaymentEvent{ID: "event-1", Provider: p.Name(), ProviderPaymentID: "payment-1", OrderID: "order-1", Status: "paid", Amount: 10000, Currency: "IDR", PaidAt: time.Now().UTC(), Raw: body}, nil
}

type webhookRepo struct {
	mu      sync.Mutex
	seen    map[string]bool
	order   domain.Order
	applied int
}

func (r *webhookRepo) ApplyPaymentEvent(_ context.Context, event PaymentEvent) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := event.Provider + ":" + event.ID
	if r.seen[key] {
		return false, nil
	}
	r.seen[key] = true
	next, err := TransitionOrder(r.order.Status, event.Status)
	if err != nil {
		return false, err
	}
	r.order.Status = next
	r.applied++
	return true, nil
}

type testRegistry struct {
	provider PaymentProvider
}

func (r *testRegistry) Get(name string) (PaymentProvider, error) {
	return r.provider, nil
}
func (r *testRegistry) List() []string                                 { return []string{r.provider.Name()} }
func (r *testRegistry) Register(name string, provider PaymentProvider) { r.provider = provider }

func TestWebhookRejectsInvalidSignature(t *testing.T) {
	repo := &webhookRepo{seen: map[string]bool{}, order: domain.Order{Status: domain.OrderPending}}
	provider := hmacTestProvider{secret: []byte("webhook-secret")}
	registry := &testRegistry{provider: provider}
	service := NewWebhookService(repo, registry)
	if _, err := service.Process(context.Background(), map[string]string{"X-Signature": "00"}, []byte(`{"status":"paid"}`)); !errors.Is(err, ErrInvalidWebhookSignature) {
		t.Fatalf("Process() error = %v", err)
	}
	if repo.applied != 0 {
		t.Fatal("invalid webhook changed an order")
	}
}

func TestWebhookIsIdempotentAndTransitionsOrderOnce(t *testing.T) {
	secret := []byte("webhook-secret")
	body := []byte(`{"status":"paid"}`)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	headers := map[string]string{"X-Signature": hex.EncodeToString(mac.Sum(nil))}
	repo := &webhookRepo{seen: map[string]bool{}, order: domain.Order{Status: domain.OrderPending}}
	provider := hmacTestProvider{secret: secret}
	registry := &testRegistry{provider: provider}
	service := NewWebhookService(repo, registry)
	first, err := service.Process(context.Background(), headers, body)
	if err != nil || !first.Processed {
		t.Fatalf("first = %#v, %v", first, err)
	}
	second, err := service.Process(context.Background(), headers, body)
	if err != nil || second.Processed {
		t.Fatalf("second = %#v, %v", second, err)
	}
	if repo.order.Status != domain.OrderPaid || repo.applied != 1 {
		t.Fatalf("status=%s applied=%d", repo.order.Status, repo.applied)
	}
}

func TestOrderTransitionsRejectRegressions(t *testing.T) {
	valid := []struct {
		from  domain.OrderStatus
		event string
		want  domain.OrderStatus
	}{
		{domain.OrderPending, "paid", domain.OrderPaid},
		{domain.OrderPaid, "fulfilled", domain.OrderCompleted},
		{domain.OrderPending, "expired", domain.OrderExpired},
		{domain.OrderPaid, "refunded", domain.OrderRefunded},
	}
	for _, tc := range valid {
		got, err := TransitionOrder(tc.from, tc.event)
		if err != nil || got != tc.want {
			t.Fatalf("TransitionOrder(%s,%s)=%s,%v", tc.from, tc.event, got, err)
		}
	}
	for _, tc := range []struct {
		from  domain.OrderStatus
		event string
	}{{domain.OrderCompleted, "paid"}, {domain.OrderCancelled, "paid"}, {domain.OrderPaid, "expired"}} {
		if _, err := TransitionOrder(tc.from, tc.event); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("TransitionOrder(%s,%s) error=%v", tc.from, tc.event, err)
		}
	}
}
