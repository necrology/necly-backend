package service

import (
	"context"
	"errors"
	"strings"

	"github.com/necly/services/internal/domain"
)

var ErrInvalidWebhookSignature = errors.New("invalid webhook signature")

type WebhookRepository interface {
	ApplyPaymentEvent(ctx context.Context, event PaymentEvent) (bool, error)
}

type WebhookResult struct {
	Processed bool `json:"processed"`
}

type PaymentProviderRegistryInterface interface {
	Get(name string) (PaymentProvider, error)
	List() []string
}

type WebhookService struct {
	repo      WebhookRepository
	providers PaymentProviderRegistryInterface
}

func NewWebhookService(repo WebhookRepository, providers PaymentProviderRegistryInterface) *WebhookService {
	return &WebhookService{repo: repo, providers: providers}
}

func (s *WebhookService) Process(ctx context.Context, headers map[string]string, body []byte) (*WebhookResult, error) {
	// Determine provider from headers or try all
	var provider PaymentProvider
	providerName := headers["X-Provider"]
	if providerName == "" {
		providerName = headers["x-provider"]
	}

	if providerName != "" {
		p, err := s.providers.Get(providerName)
		if err != nil {
			return nil, err
		}
		provider = p
	} else {
		// Try each registered provider
		var lastErr error
		for _, name := range s.providers.List() {
			p, _ := s.providers.Get(name)
			event, err := p.VerifyWebhook(headers, body)
			if err == nil && event != nil && event.ID != "" && event.OrderID != "" && event.ProviderPaymentID != "" {
				provider = p
				break
			}
			lastErr = err
		}
		if provider == nil && lastErr != nil {
			return nil, lastErr
		}
	}

	if provider == nil {
		return nil, ErrUnauthorized
	}

	event, err := provider.VerifyWebhook(headers, body)
	if err != nil {
		return nil, err
	}
	if event == nil || event.ID == "" || event.OrderID == "" || event.ProviderPaymentID == "" || event.Provider != provider.Name() {
		return nil, ErrInvalidInput
	}
	processed, err := s.repo.ApplyPaymentEvent(ctx, *event)
	if err != nil {
		return nil, err
	}
	return &WebhookResult{Processed: processed}, nil
}

func TransitionOrder(current domain.OrderStatus, paymentEvent string) (domain.OrderStatus, error) {
	event := strings.ToLower(strings.TrimSpace(paymentEvent))
	switch current {
	case domain.OrderPending:
		switch event {
		case "paid", "settlement", "capture":
			return domain.OrderPaid, nil
		case "cancelled", "canceled", "deny", "failed":
			return domain.OrderCancelled, nil
		case "expired", "expire":
			return domain.OrderExpired, nil
		case "pending", "pending_payment":
			return current, nil
		}
	case domain.OrderPaid, domain.OrderProcessing, domain.OrderShipped, domain.OrderDelivered:
		switch event {
		case "fulfilled", "completed":
			return domain.OrderCompleted, nil
		case "refunded", "refund":
			return domain.OrderRefunded, nil
		case "paid", "settlement", "capture":
			return current, nil
		}
	case domain.OrderCompleted:
		if event == "refunded" || event == "refund" {
			return domain.OrderRefunded, nil
		}
	case domain.OrderCancelled, domain.OrderExpired, domain.OrderRefunded:
		// Terminal states cannot be revived by delayed or replayed events.
	}
	return current, ErrInvalidTransition
}
