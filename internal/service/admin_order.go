package service

import (
	"context"
	"fmt"
	"time"

	"github.com/necly/services/internal/domain"
)

type AdminOrderService struct {
	repo AdminOrderRepository
}

func NewAdminOrderService(repo AdminOrderRepository) *AdminOrderService {
	return &AdminOrderService{repo: repo}
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
	return s.repo.GetOrderByID(ctx, id)
}

func (s *AdminOrderService) UpdateOrderStatus(ctx context.Context, id string, status domain.OrderStatus) (*domain.Order, error) {
	validStatuses := map[domain.OrderStatus]bool{
		domain.OrderPending:    true,
		domain.OrderPaid:       true,
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

	order, err := s.repo.GetOrderByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if !isValidTransition(order.Status, status) {
		return nil, ErrInvalidTransition
	}

	now := time.Now().UTC()
	order.Status = status
	order.UpdatedAt = now

	if err := s.repo.UpdateOrderStatus(ctx, id, status, now); err != nil {
		return nil, fmt.Errorf("update order status: %w", err)
	}
	return order, nil
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
