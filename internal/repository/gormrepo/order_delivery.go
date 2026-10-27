package gormrepo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var _ service.OrderDeliveryRepository = (*Store)(nil)

// EnsureOrderDeliveries creates one pending row per order item. The unique
// constraint on order_item_id makes concurrent approvals converge on the same
// set of rows; no duplicate delivery is ever created.
func (s *Store) EnsureOrderDeliveries(ctx context.Context, orderID string, now time.Time) ([]domain.OrderDelivery, error) {
	var items []domain.OrderItem
	if err := s.db.WithContext(ctx).Select("id").Where("order_id = ?", orderID).Order("id").Find(&items).Error; err != nil {
		return nil, fmt.Errorf("load order items for delivery: %w", err)
	}
	rows := make([]domain.OrderDelivery, 0, len(items))
	for _, item := range items {
		rows = append(rows, domain.OrderDelivery{
			ID: uuid.NewString(), OrderID: orderID, OrderItemID: item.ID,
			Status: domain.DeliveryPending, CreatedAt: now, UpdatedAt: now,
		})
	}
	if len(rows) > 0 {
		if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "order_item_id"}},
			DoNothing: true,
		}).Create(&rows).Error; err != nil {
			return nil, fmt.Errorf("create order deliveries: %w", err)
		}
	}
	return s.ListOrderDeliveries(ctx, orderID)
}

func (s *Store) ListOrderDeliveries(ctx context.Context, orderID string) ([]domain.OrderDelivery, error) {
	var rows []domain.OrderDelivery
	if err := s.db.WithContext(ctx).
		Where("order_id = ?", orderID).
		Order("created_at ASC, id ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list order deliveries: %w", err)
	}
	if err := s.attachDeliveryDetails(ctx, orderID, rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Store) GetOrderDelivery(ctx context.Context, orderID, deliveryID string) (*domain.OrderDelivery, error) {
	var row domain.OrderDelivery
	err := s.db.WithContext(ctx).Where("id = ? AND order_id = ?", deliveryID, orderID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrDeliveryNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get order delivery: %w", err)
	}
	rows := []domain.OrderDelivery{row}
	if err := s.attachDeliveryDetails(ctx, orderID, rows); err != nil {
		return nil, err
	}
	return &rows[0], nil
}

// LoadDeliveryMaterial returns the reserved slots and their account ciphertext
// for one order item. The join on order_items keeps an id from another order
// from being read even if a caller supplies one.
func (s *Store) LoadDeliveryMaterial(ctx context.Context, orderID, orderItemID string) (*service.DeliveryMaterial, error) {
	type materialRow struct {
		SlotID               string
		InventoryAccountID   string
		AccountLabel         string
		AccountStatus        string
		AccountExpiresAt     *time.Time
		EncryptedCredentials []byte
		DurationDays         int
	}
	var rows []materialRow
	if err := s.db.WithContext(ctx).Table("inventory_slots AS s").
		Select(`s.id AS slot_id, s.inventory_account_id,
			COALESCE(a.label, '') AS account_label, a.status AS account_status,
			a.expires_at AS account_expires_at, a.encrypted_credentials,
			COALESCE(pd.days, 0) AS duration_days`).
		Joins("JOIN inventory_accounts a ON a.id = s.inventory_account_id").
		Joins("JOIN order_items oi ON oi.id = s.order_item_id").
		Joins("JOIN product_prices pp ON pp.id = oi.product_price_id").
		Joins("JOIN product_durations pd ON pd.id = pp.duration_id").
		Where("s.order_item_id = ? AND oi.order_id = ? AND s.status NOT IN ? AND (s.access_expires_at IS NULL OR s.access_expires_at > now())",
			orderItemID, orderID, []string{"released", "revoked", "expired"}).
		Order("s.created_at ASC, s.id ASC").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load delivery material: %w", err)
	}
	material := &service.DeliveryMaterial{OrderItemID: orderItemID, Slots: make([]service.DeliverySlot, 0, len(rows))}
	if len(rows) > 0 {
		material.DurationDays = rows[0].DurationDays
	}
	for _, row := range rows {
		material.Slots = append(material.Slots, service.DeliverySlot{
			SlotID: row.SlotID, InventoryAccountID: row.InventoryAccountID,
			AccountLabel: row.AccountLabel, AccountStatus: row.AccountStatus,
			AccountExpiresAt: row.AccountExpiresAt, EncryptedCredentials: row.EncryptedCredentials,
		})
	}
	return material, nil
}

// ClaimOrderDelivery is the idempotency gate. Only one worker can move a row
// from pending/failed (or from a stale sending lease) to sending.
func (s *Store) ClaimOrderDelivery(ctx context.Context, deliveryID string, now, staleBefore time.Time) (bool, error) {
	result := s.db.WithContext(ctx).Model(&domain.OrderDelivery{}).
		Where("id = ? AND (status IN ? OR (status = ? AND updated_at < ?))",
			deliveryID, []string{domain.DeliveryPending, domain.DeliveryFailed}, domain.DeliverySending, staleBefore).
		Updates(map[string]any{
			"status":          domain.DeliverySending,
			"attempts":        gorm.Expr("attempts + 1"),
			"last_attempt_at": now,
			"updated_at":      now,
			"error_code":      nil,
		})
	if result.Error != nil {
		return false, fmt.Errorf("claim order delivery: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

func (s *Store) MarkOrderDeliverySent(ctx context.Context, deliveryID string, now time.Time) error {
	result := s.db.WithContext(ctx).Model(&domain.OrderDelivery{}).
		Where("id = ? AND status = ?", deliveryID, domain.DeliverySending).
		Updates(map[string]any{
			"status":     domain.DeliverySent,
			"sent_at":    now,
			"updated_at": now,
			"error_code": nil,
		})
	if result.Error != nil {
		return fmt.Errorf("mark order delivery sent: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return service.ErrConflict
	}
	return nil
}

func (s *Store) MarkOrderDeliveryFailed(ctx context.Context, deliveryID, errorCode string, now time.Time) error {
	result := s.db.WithContext(ctx).Model(&domain.OrderDelivery{}).
		Where("id = ? AND status = ?", deliveryID, domain.DeliverySending).
		Updates(map[string]any{
			"status":     domain.DeliveryFailed,
			"error_code": errorCode,
			"updated_at": now,
		})
	if result.Error != nil {
		return fmt.Errorf("mark order delivery failed: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return service.ErrConflict
	}
	return nil
}
