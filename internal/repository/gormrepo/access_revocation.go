package gormrepo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
	"gorm.io/gorm"
)

var (
	accessRevocationInactiveSlotStatuses = []string{"released", "revoked", "expired"}
	accessRevocationApprovedOrderStatus  = []domain.OrderStatus{
		domain.OrderProcessing, domain.OrderShipped, domain.OrderDelivered, domain.OrderCompleted,
	}
)

// RevocationImpact returns the inventory accounts whose credentials have
// already been emailed for this order. OtherActiveUsers counts distinct
// customers on the same account, excluding the order being cancelled, because
// rotating the password will sign all of them out.
func (s *Store) RevocationImpact(ctx context.Context, orderID string) ([]service.RevocationAccountImpact, error) {
	var order domain.Order
	if err := s.db.WithContext(ctx).
		Select("id", "user_id").
		Where("id = ?", orderID).
		First(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, service.ErrNotFound
		}
		return nil, fmt.Errorf("load order for revocation: %w", err)
	}

	type accountRow struct {
		InventoryAccountID string `gorm:"column:inventory_account_id"`
		Label              string `gorm:"column:label"`
		AccountEmail       string `gorm:"column:account_email"`
	}
	var rows []accountRow
	if err := s.db.WithContext(ctx).Table("inventory_slots AS s").
		Select("a.id AS inventory_account_id, COALESCE(a.label, '') AS label, COALESCE(a.account_email, '') AS account_email").
		Joins("JOIN inventory_accounts a ON a.id = s.inventory_account_id").
		Joins("JOIN order_items oi ON oi.id = s.order_item_id").
		Where("oi.order_id = ?", orderID).
		Where("s.status NOT IN ?", accessRevocationInactiveSlotStatuses).
		Where("EXISTS (SELECT 1 FROM order_deliveries d WHERE d.order_item_id = s.order_item_id AND d.status = ?)", domain.DeliverySent).
		Group("a.id, a.label, a.account_email").
		Order("a.label ASC, a.id ASC").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load revocation accounts: %w", err)
	}

	impact := make([]service.RevocationAccountImpact, 0, len(rows))
	for _, row := range rows {
		var otherUsers int64
		if err := s.db.WithContext(ctx).Table("inventory_slots AS s2").
			Select("COUNT(DISTINCT o2.user_id)").
			Joins("JOIN order_items oi2 ON oi2.id = s2.order_item_id").
			Joins("JOIN orders o2 ON o2.id = oi2.order_id").
			Where("s2.inventory_account_id = ?", row.InventoryAccountID).
			Where("s2.status NOT IN ?", accessRevocationInactiveSlotStatuses).
			Where("o2.id <> ? AND o2.user_id <> ?", orderID, order.UserID).
			Where("o2.status IN ?", accessRevocationApprovedOrderStatus).
			Where("(s2.access_expires_at IS NULL OR s2.access_expires_at > now())").
			Row().Scan(&otherUsers); err != nil {
			return nil, fmt.Errorf("count other active users: %w", err)
		}
		impact = append(impact, service.RevocationAccountImpact{
			InventoryAccountID: row.InventoryAccountID,
			Label:              row.Label,
			AccountEmail:       row.AccountEmail,
			OtherActiveUsers:   int(otherUsers),
		})
	}
	return impact, nil
}

// ReleaseOrderSlots frees every slot that still belongs to the order. It is
// called only after the access revocation (and, for delivered accounts, the
// password rotation) succeeded. Clearing the access window is required for a
// later resale: delivery activation uses COALESCE(activated_at, now).
func (s *Store) ReleaseOrderSlots(ctx context.Context, orderID string, now time.Time) (int, error) {
	released := int64(0)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&domain.InventorySlot{}).
			Where("order_item_id IN (SELECT id FROM order_items WHERE order_id = ?) AND status <> ?", orderID, "released").
			Updates(map[string]any{
				"status":            "available",
				"reserved_by":       nil,
				"reserved_until":    nil,
				"order_item_id":     nil,
				"activated_at":      nil,
				"access_expires_at": nil,
				"updated_at":        now,
			})
		if result.Error != nil {
			return fmt.Errorf("release order slots: %w", result.Error)
		}
		released = result.RowsAffected
		return nil
	})
	if err != nil {
		return 0, err
	}
	return int(released), nil
}

// ValidateAccessCode is the runtime guard for OTP access codes. A code that was
// issued before cancellation must stop working immediately: it is only valid
// while the order is approved, the delivery is sent, and at least one slot is
// still active for that item.
func (s *Store) ValidateAccessCode(ctx context.Context, orderID, orderItemID, userID string, now time.Time) (bool, error) {
	var count int64
	if err := s.db.WithContext(ctx).Table("orders AS o").
		Select("COUNT(*)").
		Joins("JOIN order_items oi ON oi.order_id = o.id").
		Joins("JOIN order_deliveries d ON d.order_item_id = oi.id").
		Joins("JOIN inventory_slots s ON s.order_item_id = oi.id").
		Where("o.id = ? AND oi.id = ? AND o.user_id = ?", orderID, orderItemID, userID).
		Where("o.status IN ?", accessRevocationApprovedOrderStatus).
		Where("d.status = ?", domain.DeliverySent).
		Where("s.status NOT IN ?", accessRevocationInactiveSlotStatuses).
		Where("(s.access_expires_at IS NULL OR s.access_expires_at > ?)", now).
		Row().Scan(&count); err != nil {
		return false, fmt.Errorf("validate access code: %w", err)
	}
	return count > 0, nil
}
