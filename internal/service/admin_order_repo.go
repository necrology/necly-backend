package service

import (
	"context"
	"time"

	"github.com/necly/services/internal/domain"
)

type AdminOrderRepository interface {
	ListOrders(ctx context.Context, filter OrderFilter, page, perPage int) ([]domain.Order, int64, error)
	GetOrderByID(ctx context.Context, id string) (*domain.Order, error)
	TransitionOrderStatus(ctx context.Context, id string, status domain.OrderStatus, now time.Time) (*domain.Order, error)
	UpdateOrder(ctx context.Context, o *domain.Order) error
	// RevocationImpact returns one row per delivered account that still backs
	// the order. It is used to warn the operator before rotating a shared
	// password and to reject a plain cancellation of a delivered order.
	RevocationImpact(ctx context.Context, orderID string) ([]RevocationAccountImpact, error)
	// ReleaseOrderSlots frees every slot linked to the order after the access
	// revocation has been committed. Credentials must have been rotated first.
	ReleaseOrderSlots(ctx context.Context, orderID string, now time.Time) (int, error)
}
