package service

import (
	"context"
	"testing"
	"time"

	"github.com/necly/services/internal/domain"
)

type archiveOrderRepository struct {
	archiveUserID  string
	archiveOrderID string
	archiveNow     time.Time
	archiveErr     error
}

func (r *archiveOrderRepository) ListUserOrders(context.Context, string, int, int) ([]domain.Order, int64, error) {
	return nil, 0, nil
}
func (r *archiveOrderRepository) ListOrders(context.Context, OrderFilter, int, int) ([]domain.Order, int64, error) {
	return nil, 0, nil
}
func (r *archiveOrderRepository) GetOrder(context.Context, string, string) (*domain.Order, error) {
	return nil, nil
}
func (r *archiveOrderRepository) GetOrderByID(context.Context, string) (*domain.Order, error) {
	return nil, nil
}
func (r *archiveOrderRepository) GetOrderByIdempotencyKey(context.Context, string) (*domain.Order, error) {
	return nil, nil
}
func (r *archiveOrderRepository) GetOrdersForAdmin(context.Context, OrderFilter, int, int) ([]domain.Order, int64, error) {
	return nil, 0, nil
}
func (r *archiveOrderRepository) UpdateOrderStatus(context.Context, string, domain.OrderStatus, time.Time) error {
	return nil
}
func (r *archiveOrderRepository) ArchiveOrder(_ context.Context, userID, orderID string, now time.Time) error {
	r.archiveUserID, r.archiveOrderID, r.archiveNow = userID, orderID, now
	return r.archiveErr
}

func TestOrderServiceArchiveOrderPassesOwnerAndTime(t *testing.T) {
	repo := &archiveOrderRepository{}
	svc := NewOrderService(repo, nil, nil)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

	if err := svc.ArchiveOrder(context.Background(), "owner-1", "order-1", now); err != nil {
		t.Fatal(err)
	}
	if repo.archiveUserID != "owner-1" || repo.archiveOrderID != "order-1" || !repo.archiveNow.Equal(now) {
		t.Fatalf("archive arguments = (%q, %q, %s)", repo.archiveUserID, repo.archiveOrderID, repo.archiveNow)
	}
}
