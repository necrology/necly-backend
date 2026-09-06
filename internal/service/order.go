package service

import (
	"context"
	"fmt"
	"time"

	"github.com/necly/services/internal/domain"
)

type OrderService struct {
	orderRepo     OrderRepository
	cartRepo      CartRepository
	inventoryRepo InventoryRepository
}

func NewOrderService(orderRepo OrderRepository, cartRepo CartRepository, inventoryRepo InventoryRepository) *OrderService {
	return &OrderService{
		orderRepo:     orderRepo,
		cartRepo:      cartRepo,
		inventoryRepo: inventoryRepo,
	}
}

func (s *OrderService) ListOrders(ctx context.Context, userID string, page, perPage int) ([]domain.Order, int64, error) {
	return s.orderRepo.ListUserOrders(ctx, userID, page, perPage)
}

func (s *OrderService) GetOrder(ctx context.Context, userID, orderID string) (*domain.Order, error) {
	return s.orderRepo.GetOrder(ctx, userID, orderID)
}

func (s *OrderService) GetOrderByID(ctx context.Context, orderID string) (*domain.Order, error) {
	return s.orderRepo.GetOrderByID(ctx, orderID)
}

func (s *OrderService) GetOrderByIdempotencyKey(ctx context.Context, key string) (*domain.Order, error) {
	return s.orderRepo.GetOrderByIdempotencyKey(ctx, key)
}

func (s *OrderService) GetOrdersForAdmin(ctx context.Context, filter OrderFilter, page, perPage int) ([]domain.Order, int64, error) {
	return s.orderRepo.GetOrdersForAdmin(ctx, filter, page, perPage)
}

func (s *OrderService) UpdateOrderStatus(ctx context.Context, orderID string, status domain.OrderStatus, now time.Time) error {
	// Validate status transition
	current, err := s.orderRepo.GetOrderByID(ctx, orderID)
	if err != nil {
		return err
	}
	if !isValidTransition(current.Status, status) {
		return ErrInvalidTransition
	}
	return s.orderRepo.UpdateOrderStatus(ctx, orderID, status, now)
}

func isValidTransition(from, to domain.OrderStatus) bool {
	validTransitions := map[domain.OrderStatus][]domain.OrderStatus{
		domain.OrderPending:    {domain.OrderPaid, domain.OrderCancelled, domain.OrderExpired},
		domain.OrderPaid:       {domain.OrderProcessing, domain.OrderCancelled, domain.OrderRefunded},
		domain.OrderProcessing: {domain.OrderShipped, domain.OrderCancelled, domain.OrderRefunded},
		domain.OrderShipped:    {domain.OrderDelivered, domain.OrderCancelled, domain.OrderRefunded},
		domain.OrderDelivered:  {domain.OrderCompleted, domain.OrderRefunded},
		domain.OrderCompleted:  {domain.OrderRefunded},
		domain.OrderCancelled:  {},
		domain.OrderExpired:    {},
		domain.OrderRefunded:   {},
	}
	allowed, ok := validTransitions[from]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}

func generateOrderNumber(now time.Time) string {
	return fmt.Sprintf("NEC-%s-%04d", now.Format("20060102"), now.Nanosecond()%10000)
}
