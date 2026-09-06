package service

import (
	"context"
	"time"

	"github.com/necly/services/internal/domain"
)

type OrderRepository interface {
	ListUserOrders(ctx context.Context, userID string, page, perPage int) ([]domain.Order, int64, error)
	ListOrders(ctx context.Context, filter OrderFilter, page, perPage int) ([]domain.Order, int64, error)
	GetOrder(ctx context.Context, userID, orderID string) (*domain.Order, error)
	GetOrderByID(ctx context.Context, orderID string) (*domain.Order, error)
	GetOrderByIdempotencyKey(ctx context.Context, key string) (*domain.Order, error)
	GetOrdersForAdmin(ctx context.Context, filter OrderFilter, page, perPage int) ([]domain.Order, int64, error)
	UpdateOrderStatus(ctx context.Context, orderID string, status domain.OrderStatus, now time.Time) error
}

type OrderFilter struct {
	UserID   string
	Status   string
	Search   string
	DateFrom *time.Time
	DateTo   *time.Time
}
