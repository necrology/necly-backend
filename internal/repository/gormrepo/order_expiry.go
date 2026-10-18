package gormrepo

import (
	"context"
	"fmt"
	"time"

	"github.com/necly/services/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ExpireOverdueOrders selects a bounded batch of unpaid orders whose payment
// window has passed, marks them expired and releases their reservations in one
// transaction. SKIP LOCKED lets a second application instance process a
// different batch instead of blocking. Re-running the same batch is a no-op
// because the status predicate is part of the update.
func (s *Store) ExpireOverdueOrders(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > 1000 {
		limit = 1000
	}
	expired := 0
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []string
		if err := tx.Model(&domain.Order{}).
			Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ? AND payment_due_at <= ?", domain.OrderPending, now).
			Order("payment_due_at ASC").
			Limit(limit).
			Pluck("id", &ids).Error; err != nil {
			return fmt.Errorf("select overdue orders: %w", err)
		}
		if len(ids) == 0 {
			return nil
		}

		update := tx.Model(&domain.Order{}).
			Where("id IN ? AND status = ?", ids, domain.OrderPending).
			Updates(map[string]any{"status": domain.OrderExpired, "updated_at": now})
		if update.Error != nil {
			return fmt.Errorf("expire orders: %w", update.Error)
		}
		expired = int(update.RowsAffected)

		if err := tx.Model(&domain.InventorySlot{}).
			Where("order_item_id IN (SELECT id FROM order_items WHERE order_id IN ?) AND status = ?", ids, "reserved").
			Updates(map[string]any{
				"status":         "available",
				"reserved_by":    nil,
				"reserved_until": nil,
				"order_item_id":  nil,
				"updated_at":     now,
			}).Error; err != nil {
			return fmt.Errorf("release expired slots: %w", err)
		}

		if err := tx.Model(&domain.Payment{}).
			Where("order_id IN ? AND status IN ?", ids, []string{"pending", "failed"}).
			Updates(map[string]any{"status": "expired", "updated_at": now}).Error; err != nil {
			return fmt.Errorf("expire payments: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return expired, nil
}
