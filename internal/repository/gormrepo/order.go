package gormrepo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type OrderRepository interface {
	ListUserOrders(ctx context.Context, userID string, page, perPage int) ([]domain.Order, int64, error)
	GetOrder(ctx context.Context, userID, orderID string) (*domain.Order, error)
	GetOrderByID(ctx context.Context, orderID string) (*domain.Order, error)
	GetOrderByIdempotencyKey(ctx context.Context, key string) (*domain.Order, error)
	GetOrdersForAdmin(ctx context.Context, filter service.OrderFilter, page, perPage int) ([]domain.Order, int64, error)
	UpdateOrderStatus(ctx context.Context, orderID string, status domain.OrderStatus, now time.Time) error
}

type OrderFilter struct {
	UserID   string
	Status   string
	Search   string
	DateFrom *time.Time
	DateTo   *time.Time
}

func (s *Store) ListUserOrders(ctx context.Context, userID string, page, perPage int) ([]domain.Order, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	query := s.db.WithContext(ctx).Model(&domain.Order{}).Preload("Items").Preload("Payments").Where("user_id = ?", userID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count orders: %w", err)
	}
	var orders []domain.Order
	offset := (page - 1) * perPage
	if err := query.Order("created_at DESC").Offset(offset).Limit(perPage).Find(&orders).Error; err != nil {
		return nil, 0, fmt.Errorf("list orders: %w", err)
	}
	return orders, total, nil
}

func (s *Store) GetOrder(ctx context.Context, userID, orderID string) (*domain.Order, error) {
	var order domain.Order
	err := s.db.WithContext(ctx).Preload("Items").Preload("Payments").
		Where("id = ? AND user_id = ?", orderID, userID).First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	return &order, err
}

func (s *Store) GetOrderByID(ctx context.Context, orderID string) (*domain.Order, error) {
	var order domain.Order
	err := s.db.WithContext(ctx).Preload("Customer").Preload("Items").Preload("Payments").
		Where("id = ?", orderID).First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	return &order, err
}

func (s *Store) GetOrderByIdempotencyKey(ctx context.Context, key string) (*domain.Order, error) {
	var order domain.Order
	err := s.db.WithContext(ctx).Preload("Items").Preload("Payments").
		Where("idempotency_key = ?", key).First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	return &order, err
}

func (s *Store) GetOrdersForAdmin(ctx context.Context, filter service.OrderFilter, page, perPage int) ([]domain.Order, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	query := s.db.WithContext(ctx).Model(&domain.Order{}).Preload("Customer").Preload("Items").Preload("Payments")
	if filter.UserID != "" {
		query = query.Where("user_id = ?", filter.UserID)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.Search != "" {
		like := "%" + strings.ToLower(filter.Search) + "%"
		query = query.Where("LOWER(number) LIKE ? OR LOWER(id) LIKE ?", like, like)
	}
	if filter.DateFrom != nil {
		query = query.Where("created_at >= ?", *filter.DateFrom)
	}
	if filter.DateTo != nil {
		query = query.Where("created_at <= ?", *filter.DateTo)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count orders: %w", err)
	}
	var orders []domain.Order
	offset := (page - 1) * perPage
	if err := query.Order("created_at DESC").Offset(offset).Limit(perPage).Find(&orders).Error; err != nil {
		return nil, 0, fmt.Errorf("list orders: %w", err)
	}
	return orders, total, nil
}

func (s *Store) UpdateOrderStatus(ctx context.Context, orderID string, status domain.OrderStatus, now time.Time) error {
	result := s.db.WithContext(ctx).Model(&domain.Order{}).
		Where("id = ?", orderID).
		Updates(map[string]any{"status": status, "updated_at": now})
	if result.Error != nil {
		return fmt.Errorf("update order status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return service.ErrNotFound
	}
	return nil
}

// TransitionOrderStatus is the transactional admin path. It locks the order,
// applies the shared state machine, releases inventory exactly once for a
// cancelled/expired reservation, and keeps payment rows in step. A paid order
// never releases its stock through a manual cancellation or refund.
func (s *Store) TransitionOrderStatus(ctx context.Context, orderID string, status domain.OrderStatus, now time.Time) (*domain.Order, error) {
	var updated *domain.Order
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order domain.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", orderID).First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return service.ErrNotFound
			}
			return fmt.Errorf("load order: %w", err)
		}
		previous := order.Status
		if !service.IsValidOrderTransition(previous, status) {
			return service.ErrInvalidTransition
		}

		order.Status = status
		order.UpdatedAt = now
		if err := tx.Save(&order).Error; err != nil {
			return fmt.Errorf("update order status: %w", err)
		}

		if previous == domain.OrderPending && (status == domain.OrderCancelled || status == domain.OrderExpired) {
			if err := tx.Model(&domain.InventorySlot{}).
				Where("order_item_id IN (SELECT id FROM order_items WHERE order_id = ?) AND status = ?", order.ID, "reserved").
				Updates(map[string]any{
					"status":         "available",
					"reserved_by":    nil,
					"reserved_until": nil,
					"order_item_id":  nil,
					"updated_at":     now,
				}).Error; err != nil {
				return fmt.Errorf("release slots: %w", err)
			}
		}

		switch status {
		case domain.OrderPaid:
			result := tx.Model(&domain.Payment{}).
				Where("order_id = ? AND status <> ?", order.ID, "paid").
				Updates(map[string]any{"status": "paid", "paid_at": now, "updated_at": now})
			if result.Error != nil {
				return fmt.Errorf("synchronise paid payment: %w", result.Error)
			}
			if result.RowsAffected == 0 {
				var payments int64
				if err := tx.Model(&domain.Payment{}).Where("order_id = ?", order.ID).Count(&payments).Error; err != nil {
					return fmt.Errorf("count payments: %w", err)
				}
				if payments == 0 {
					manual := domain.Payment{
						ID: uuid.NewString(), OrderID: order.ID, Provider: "manual",
						ProviderPaymentID: "manual-" + order.ID, Status: "paid",
						Amount: order.Total, PaidAt: &now, CreatedAt: now, UpdatedAt: now,
					}
					if err := tx.Create(&manual).Error; err != nil {
						return fmt.Errorf("create manual payment: %w", err)
					}
				}
			}
		case domain.OrderRefunded:
			if err := tx.Model(&domain.Payment{}).
				Where("order_id = ? AND status = ?", order.ID, "paid").
				Updates(map[string]any{"status": "refunded", "updated_at": now}).Error; err != nil {
				return fmt.Errorf("synchronise refunded payment: %w", err)
			}
		case domain.OrderCancelled, domain.OrderExpired:
			if err := tx.Model(&domain.Payment{}).
				Where("order_id = ? AND status IN ?", order.ID, []string{"pending", "failed"}).
				Updates(map[string]any{"status": string(status), "updated_at": now}).Error; err != nil {
				return fmt.Errorf("synchronise %s payment: %w", status, err)
			}
		}

		var loaded domain.Order
		if err := tx.Preload("Items").Preload("Payments").Where("id = ?", order.ID).First(&loaded).Error; err != nil {
			return fmt.Errorf("reload order: %w", err)
		}
		updated = &loaded
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *Store) ListOrders(ctx context.Context, filter service.OrderFilter, page, perPage int) ([]domain.Order, int64, error) {
	return s.GetOrdersForAdmin(ctx, filter, page, perPage)
}

func (s *Store) UpdateOrder(ctx context.Context, o *domain.Order) error {
	o.UpdatedAt = s.nowUTC()
	return s.db.WithContext(ctx).Where("id = ?", o.ID).Save(o).Error
}
