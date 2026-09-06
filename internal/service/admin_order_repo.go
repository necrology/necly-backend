package service

import (
	"context"
	"time"

	"github.com/necly/services/internal/domain"
)

type AdminOrderRepository interface {
	ListOrders(ctx context.Context, filter OrderFilter, page, perPage int) ([]domain.Order, int64, error)
	GetOrderByID(ctx context.Context, id string) (*domain.Order, error)
	UpdateOrderStatus(ctx context.Context, id string, status domain.OrderStatus, now time.Time) error
	UpdateOrder(ctx context.Context, o *domain.Order) error
}
