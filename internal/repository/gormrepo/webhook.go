package gormrepo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ApplyPaymentEvent records the provider event and applies its state transition
// in one transaction. The event is inserted first so concurrent deliveries of
// the same event serialize on the unique (provider, provider_event_id) index;
// the order and payment rows are then locked before amount, currency and status
// are checked. Any mismatch rolls the whole transaction back, including the
// event row, so a later correct callback can still be processed.
func (s *Store) ApplyPaymentEvent(ctx context.Context, event service.PaymentEvent) (bool, error) {
	if _, err := uuid.Parse(strings.TrimSpace(event.OrderID)); err != nil {
		return false, service.ErrInvalidPaymentEvent
	}
	if event.Amount <= 0 || strings.TrimSpace(event.Currency) == "" || strings.TrimSpace(event.ID) == "" {
		return false, service.ErrInvalidPaymentEvent
	}

	var applied bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := s.nowUTC()
		payloadHash := sha256.Sum256(event.Raw)
		insert := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "provider"}, {Name: "provider_event_id"}},
			DoNothing: true,
		}).Create(&domain.WebhookEvent{
			ID:              uuid.NewString(),
			Provider:        event.Provider,
			ProviderEventID: event.ID,
			PayloadHash:     hex.EncodeToString(payloadHash[:]),
			ProcessedAt:     now,
			CreatedAt:       now,
		})
		if insert.Error != nil {
			return fmt.Errorf("insert webhook event: %w", insert.Error)
		}
		if insert.RowsAffected == 0 {
			// Exact replay of an event already applied. No lookup, no mutation.
			return nil
		}

		var order domain.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", event.OrderID).
			First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return service.ErrNotFound
			}
			return fmt.Errorf("find order: %w", err)
		}

		// Bind the callback to the payment stored for this order and provider.
		// Midtrans callbacks identify the transaction_id while the payment row
		// stores the Snap token, so its order_id is the binding; Xendit and the
		// mock adapter carry the same payment identity they stored.
		var payment domain.Payment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("order_id = ? AND provider = ?", order.ID, event.Provider).
			Order("created_at DESC, id DESC").
			First(&payment).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return service.ErrNotFound
			}
			return fmt.Errorf("find payment: %w", err)
		}

		if event.Provider != "midtrans" && payment.ProviderPaymentID != event.ProviderPaymentID {
			return service.ErrPaymentMismatch
		}
		if event.Amount != order.Total || event.Amount != payment.Amount {
			return service.ErrPaymentMismatch
		}
		if !strings.EqualFold(event.Currency, order.Currency) {
			return service.ErrPaymentMismatch
		}

		newStatus, transitionErr := service.TransitionOrder(order.Status, event.Status)
		if transitionErr != nil {
			// Terminal or stale events (for example an expiry that arrives
			// after settlement) are acknowledged but never allowed to undo a
			// paid order or release its stock. The event row stays as evidence.
			if !errors.Is(transitionErr, service.ErrInvalidTransition) {
				return transitionErr
			}
			newStatus = order.Status
		}

		release := order.Status == domain.OrderPending &&
			(newStatus == domain.OrderCancelled || newStatus == domain.OrderExpired)

		if newStatus != order.Status {
			order.Status = newStatus
			order.UpdatedAt = now
			if err := tx.Save(&order).Error; err != nil {
				return fmt.Errorf("save order: %w", err)
			}
		}

		// Keep the payment row truthful even when the order was already paid
		// through another path (for example an admin transition). A stale event
		// that could not change the order never rewrites the payment either.
		paymentChanged := false
		switch newStatus {
		case domain.OrderPaid:
			if payment.Status != "paid" {
				payment.Status = "paid"
				if !event.PaidAt.IsZero() {
					paidAt := event.PaidAt.UTC()
					payment.PaidAt = &paidAt
				}
				paymentChanged = true
			}
		case domain.OrderRefunded:
			if payment.Status != "refunded" {
				payment.Status = "refunded"
				paymentChanged = true
			}
		case domain.OrderCancelled, domain.OrderExpired:
			if payment.Status == "pending" {
				payment.Status = string(newStatus)
				paymentChanged = true
			}
		}
		if paymentChanged {
			payment.UpdatedAt = now
			if err := tx.Save(&payment).Error; err != nil {
				return fmt.Errorf("save payment: %w", err)
			}
		}

		if release {
			// Only a reservation that was still awaiting payment may be freed.
			// Paid, fulfilled or refunded stock stays allocated until the
			// documented fulfilment/reconciliation policy says otherwise.
			result := tx.Model(&domain.InventorySlot{}).
				Where("order_item_id IN (SELECT id FROM order_items WHERE order_id = ?) AND status = ?", order.ID, "reserved").
				Updates(map[string]any{
					"status":         "available",
					"reserved_by":    nil,
					"reserved_until": nil,
					"order_item_id":  nil,
					"updated_at":     now,
				})
			if result.Error != nil {
				return fmt.Errorf("release slots: %w", result.Error)
			}
		}

		applied = true
		return nil
	})
	return applied, err
}
