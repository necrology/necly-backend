package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
)

type CheckoutItemInput struct {
	ProductPriceID string `json:"product_price_id"`
	Quantity       int    `json:"quantity"`
}

type CheckoutInput struct {
	UserID         string              `json:"-"`
	IdempotencyKey string              `json:"-"`
	VoucherCode    string              `json:"voucher_code"`
	Items          []CheckoutItemInput `json:"items"`
}

type CheckoutCommand struct {
	UserID         string
	IdempotencyKey string
	VoucherCode    string
	Items          []CheckoutItemInput
	Now            time.Time
	PaymentDueAt   time.Time
}

type PaymentRequest struct {
	OrderID     string
	OrderNumber string
	Amount      int64
	Currency    string
	CustomerID  string
}

type PaymentInitiation struct {
	ProviderPaymentID string
	Status            string
	CheckoutURL       string
}

type PaymentEvent struct {
	ID                string
	Provider          string
	ProviderPaymentID string
	OrderID           string
	Status            string
	PaidAt            time.Time
	Raw               []byte
}

type PaymentProvider interface {
	Name() string
	CreatePayment(ctx context.Context, request PaymentRequest) (*PaymentInitiation, error)
	VerifyWebhook(headers map[string]string, body []byte) (*PaymentEvent, error)
}

type CheckoutRepository interface {
	ReserveCheckout(ctx context.Context, command CheckoutCommand) (*domain.Order, error)
	SavePayment(ctx context.Context, orderID string, payment *domain.Payment) error
	CancelAndRelease(ctx context.Context, orderID string, now time.Time) error
}

type CheckoutResult struct {
	Order       domain.Order   `json:"order"`
	Payment     domain.Payment `json:"payment"`
	CheckoutURL string         `json:"checkout_url"`
}

type CheckoutService struct {
	repo       CheckoutRepository
	provider   PaymentProvider
	paymentTTL time.Duration
	now        func() time.Time
}

func NewCheckoutService(repo CheckoutRepository, provider PaymentProvider, paymentTTL time.Duration) *CheckoutService {
	return &CheckoutService{repo: repo, provider: provider, paymentTTL: paymentTTL, now: time.Now}
}

func (s *CheckoutService) Checkout(ctx context.Context, input CheckoutInput) (*CheckoutResult, error) {
	input.UserID = strings.TrimSpace(input.UserID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.UserID == "" || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 || len(input.Items) == 0 || len(input.Items) > 50 {
		return nil, ErrInvalidInput
	}
	seen := make(map[string]bool, len(input.Items))
	for i := range input.Items {
		input.Items[i].ProductPriceID = strings.TrimSpace(input.Items[i].ProductPriceID)
		item := input.Items[i]
		if item.ProductPriceID == "" || item.Quantity <= 0 || item.Quantity > 100 || seen[item.ProductPriceID] {
			return nil, ErrInvalidInput
		}
		seen[item.ProductPriceID] = true
	}
	now := s.now().UTC()
	order, err := s.repo.ReserveCheckout(ctx, CheckoutCommand{
		UserID: input.UserID, IdempotencyKey: input.IdempotencyKey, VoucherCode: strings.ToUpper(strings.TrimSpace(input.VoucherCode)),
		Items: input.Items, Now: now, PaymentDueAt: now.Add(s.paymentTTL),
	})
	if err != nil {
		return nil, err
	}
	initiation, err := s.provider.CreatePayment(ctx, PaymentRequest{OrderID: order.ID, OrderNumber: order.Number, Amount: order.Total, Currency: order.Currency, CustomerID: order.UserID})
	if err != nil {
		_ = s.repo.CancelAndRelease(ctx, order.ID, now)
		return nil, fmt.Errorf("create payment: %w", err)
	}
	if initiation == nil || initiation.ProviderPaymentID == "" {
		_ = s.repo.CancelAndRelease(ctx, order.ID, now)
		return nil, errors.New("Penyedia pembayaran mengembalikan respons yang tidak valid")
	}
	payment := domain.Payment{
		ID: uuid.NewString(), OrderID: order.ID, Provider: s.provider.Name(), ProviderPaymentID: initiation.ProviderPaymentID,
		Status: initiation.Status, Amount: order.Total, CheckoutURL: initiation.CheckoutURL, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.repo.SavePayment(ctx, order.ID, &payment); err != nil {
		_ = s.repo.CancelAndRelease(ctx, order.ID, now)
		return nil, err
	}
	return &CheckoutResult{Order: *order, Payment: payment, CheckoutURL: initiation.CheckoutURL}, nil
}
