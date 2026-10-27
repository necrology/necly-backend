package service

import (
	"context"
	"fmt"
	"time"

	"github.com/necly/services/internal/domain"
)

type AdminOrderService struct {
	repo AdminOrderRepository

	// audit, credentialRotator and now are optional so existing tests can keep
	// constructing the service with only a repository. Cancellation uses them
	// when they are wired by the server.
	audit             AuditWriter
	credentialRotator AccountPasswordRotator
	now               func() time.Time
}

func NewAdminOrderService(repo AdminOrderRepository) *AdminOrderService {
	return &AdminOrderService{repo: repo, now: func() time.Time { return time.Now().UTC() }}
}

// SetAuditWriter enables the cancellation audit trail without changing the
// constructor used by older wiring and tests.
func (s *AdminOrderService) SetAuditWriter(audit AuditWriter) { s.audit = audit }

// SetCredentialRotator wires the inventory service that can replace the stored
// provider password after the operator changed it on the platform.
func (s *AdminOrderService) SetCredentialRotator(rotator AccountPasswordRotator) {
	s.credentialRotator = rotator
}

func (s *AdminOrderService) ListOrders(ctx context.Context, filter OrderFilter, page, perPage int) ([]domain.Order, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	return s.repo.ListOrders(ctx, filter, page, perPage)
}

func (s *AdminOrderService) GetOrder(ctx context.Context, id string) (*domain.Order, error) {
	order, err := s.repo.GetOrderByID(ctx, id)
	if err != nil {
		return nil, err
	}
	applyLatestPaymentStatus(order)
	return order, nil
}

// applyLatestPaymentStatus exposes the newest payment row as a single field for
// the admin detail view. The full payment history stays in Payments.
func applyLatestPaymentStatus(order *domain.Order) {
	if order == nil || len(order.Payments) == 0 {
		return
	}
	latest := order.Payments[0]
	for _, payment := range order.Payments[1:] {
		if payment.CreatedAt.After(latest.CreatedAt) {
			latest = payment
		}
	}
	order.PaymentStatus = latest.Status
}

func (s *AdminOrderService) UpdateOrderStatus(ctx context.Context, id string, status domain.OrderStatus) (*domain.Order, error) {
	validStatuses := map[domain.OrderStatus]bool{
		domain.OrderPending:    true,
		domain.OrderPaid:       true,
		domain.OrderFulfilling: true,
		domain.OrderProcessing: true,
		domain.OrderShipped:    true,
		domain.OrderDelivered:  true,
		domain.OrderCompleted:  true,
		domain.OrderCancelled:  true,
		domain.OrderRefunded:   true,
		domain.OrderExpired:    true,
	}
	if !validStatuses[status] {
		return nil, ErrInvalidInput
	}

	// A delivered order has credentials in the buyer's hands. A plain status
	// change cannot take them back, so cancellation must go through the
	// revoke-access flow that rotates the provider password first.
	if status == domain.OrderCancelled || status == domain.OrderRefunded {
		impact, err := s.repo.RevocationImpact(ctx, id)
		if err != nil {
			return nil, err
		}
		if len(impact) > 0 {
			return nil, ErrProviderRotationRequired
		}
	}

	// The repository re-reads and locks the order inside the same transaction
	// that updates it, releases stock and synchronises payments. Validating here
	// too keeps the public error contract stable without a check-then-act gap.
	return s.repo.TransitionOrderStatus(ctx, id, status, time.Now().UTC())
}

func (s *AdminOrderService) UpdateOrder(ctx context.Context, id, shippingAddress, notes string) (*domain.Order, error) {
	order, err := s.repo.GetOrderByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if shippingAddress != "" {
		order.ShippingAddress = shippingAddress
	}
	if notes != "" {
		order.Notes = notes
	}

	order.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpdateOrder(ctx, order); err != nil {
		return nil, fmt.Errorf("update order: %w", err)
	}
	return order, nil
}
