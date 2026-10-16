package gormrepo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Store) ApplyPaymentEvent(ctx context.Context, event service.PaymentEvent) (bool, error) {
	var applied bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		insert := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "provider"}, {Name: "provider_event_id"}}, DoNothing: true}).
			Create(&domain.WebhookEvent{
				ID:              uuid.NewString(),
				Provider:        event.Provider,
				ProviderEventID: event.ProviderPaymentID,
				PayloadHash:     "",
				ProcessedAt:     s.nowUTC(),
				CreatedAt:       s.nowUTC(),
			})
		if insert.Error != nil {
			return fmt.Errorf("insert webhook event: %w", insert.Error)
		}

		// Clauses().Create() executes on a clone, so the affected-row count lives
		// on the returned statement. Reading tx.RowsAffected here was always 0,
		// which made every callback answer "processed": false and skip the order
		// update entirely — a paid order stayed on pending_payment forever.
		if insert.RowsAffected == 0 {
			return nil
		}

		var payment domain.Payment
		if err := tx.Set("gorm:query_option", "FOR UPDATE").
			Where("provider = ? AND provider_payment_id = ?", event.Provider, event.ProviderPaymentID).
			First(&payment).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return service.ErrNotFound
			}
			return fmt.Errorf("find payment: %w", err)
		}

		var order domain.Order
		if err := tx.Set("gorm:query_option", "FOR UPDATE").
			Where("id = ?", event.OrderID).
			First(&order).Error; err != nil {
			return fmt.Errorf("find order: %w", err)
		}

		newStatus, err := service.TransitionOrder(order.Status, event.Status)
		if err != nil {
			return fmt.Errorf("transition order: %w", err)
		}
		if newStatus == order.Status {
			return nil
		}

		now := s.nowUTC()
		switch newStatus {
		case domain.OrderPaid:
			payment.Status = "paid"
			payment.PaidAt = &event.PaidAt
			payment.UpdatedAt = now
			order.Status = newStatus
			order.UpdatedAt = now
		case domain.OrderCompleted, domain.OrderCancelled, domain.OrderExpired, domain.OrderRefunded:
			payment.Status = event.Status
			payment.UpdatedAt = now
			order.Status = newStatus
			order.UpdatedAt = now
			if newStatus != domain.OrderPaid && newStatus != domain.OrderCompleted {
				if err := tx.Model(&domain.InventorySlot{}).
					Where("order_item_id IN (SELECT id FROM order_items WHERE order_id = ?)", order.ID).
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
		}

		if err := tx.Save(&payment).Error; err != nil {
			return fmt.Errorf("save payment: %w", err)
		}
		if err := tx.Save(&order).Error; err != nil {
			return fmt.Errorf("save order: %w", err)
		}

		applied = true
		return nil
	})
	return applied, err
}
