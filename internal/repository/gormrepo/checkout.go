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

func (s *Store) ReserveCheckout(ctx context.Context, cmd service.CheckoutCommand) (*domain.Order, error) {
	var order *domain.Order
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing domain.Order
		err := tx.Where("idempotency_key = ?", cmd.IdempotencyKey).First(&existing).Error
		if err == nil {
			order = &existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("check idempotency: %w", err)
		}

		voucherCode := cmd.VoucherCode
		var voucher *domain.Voucher
		if voucherCode != "" {
			var v domain.Voucher
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("code = ?", voucherCode).First(&v).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return service.ErrNotFound
				}
				return fmt.Errorf("lock voucher: %w", err)
			}
			voucher = &v
		}

		type priceInfo struct {
			domain.ProductPrice
			ProductName  string
			DurationName string
			SlotCount    int64
		}
		var prices []priceInfo
		priceIDs := make([]string, len(cmd.Items))
		for i, item := range cmd.Items {
			priceIDs[i] = item.ProductPriceID
		}
		// The slot table was aliased `is`, which is a reserved word in Postgres:
		// the query never parsed, so every checkout failed with SQLSTATE 42601
		// before any order could be reserved.
		if err := tx.Table("product_prices pp").
			Select("pp.*, p.name as product_name, pd.name as duration_name, count(sl.id) as slot_count").
			Joins("JOIN products p ON p.id = pp.product_id AND p.active = true").
			Joins("JOIN product_durations pd ON pd.id = pp.duration_id").
			Joins("LEFT JOIN inventory_slots sl ON sl.product_price_id = pp.id AND sl.status = 'available'").
			Where("pp.id IN ?", priceIDs).
			Where("pp.active = ?", true).
			Group("pp.id, p.name, pd.name").
			Find(&prices).Error; err != nil {
			return fmt.Errorf("load prices: %w", err)
		}
		if len(prices) != len(cmd.Items) {
			return service.ErrNotFound
		}

		var subtotal int64
		for _, item := range cmd.Items {
			var price priceInfo
			for _, p := range prices {
				if p.ID == item.ProductPriceID {
					price = p
					break
				}
			}
			if price.ID == "" || price.SlotCount < int64(item.Quantity) {
				return service.ErrOutOfStock
			}
			subtotal += price.Amount * int64(item.Quantity)
		}

		var discount int64
		if voucher != nil {
			used := int64(0)
			if err := tx.Model(&domain.VoucherRedemption{}).Where("voucher_id = ?", voucher.ID).Count(&used).Error; err != nil {
				return fmt.Errorf("count voucher uses: %w", err)
			}
			userUsed := int64(0)
			if err := tx.Model(&domain.VoucherRedemption{}).Where("voucher_id = ? AND user_id = ?", voucher.ID, cmd.UserID).Count(&userUsed).Error; err != nil {
				return fmt.Errorf("count user voucher uses: %w", err)
			}
			disc, err := service.CalculateVoucherDiscount(*voucher, subtotal, int(used), int(userUsed), cmd.Now)
			if err != nil {
				return err
			}
			discount = disc
		}

		now := cmd.Now
		order = &domain.Order{
			ID:             uuid.NewString(),
			Number:         fmt.Sprintf("ORD-%d-%s", now.UnixMilli(), uuid.NewString()[:8]),
			UserID:         cmd.UserID,
			VoucherID:      nullableString(voucherCode),
			Status:         domain.OrderPending,
			Subtotal:       subtotal,
			Discount:       discount,
			Total:          subtotal - discount,
			Currency:       "IDR",
			PaymentDueAt:   cmd.PaymentDueAt,
			IdempotencyKey: cmd.IdempotencyKey,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := tx.Create(order).Error; err != nil {
			return fmt.Errorf("create order: %w", err)
		}

		for _, item := range cmd.Items {
			var price priceInfo
			for _, p := range prices {
				if p.ID == item.ProductPriceID {
					price = p
					break
				}
			}
			slots := make([]domain.InventorySlot, 0, item.Quantity)
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
				Where("product_price_id = ? AND status = ?", item.ProductPriceID, "available").
				Limit(item.Quantity).
				Find(&slots).Error; err != nil {
				return fmt.Errorf("lock slots: %w", err)
			}
			if len(slots) < item.Quantity {
				return service.ErrOutOfStock
			}
			orderItem := domain.OrderItem{
				ID:             uuid.NewString(),
				OrderID:        order.ID,
				ProductPriceID: item.ProductPriceID,
				ProductName:    price.ProductName,
				DurationName:   price.DurationName,
				UnitPrice:      price.Amount,
				Quantity:       item.Quantity,
				DeviceCount:    item.DeviceCount,
				LineTotal:      price.Amount * int64(item.Quantity),
			}
			if err := tx.Create(&orderItem).Error; err != nil {
				return fmt.Errorf("create order item: %w", err)
			}
			for _, slot := range slots {
				slot.Status = "reserved"
				slot.ReservedBy = &cmd.UserID
				slot.ReservedUntil = &cmd.PaymentDueAt
				slot.OrderItemID = &orderItem.ID
				slot.UpdatedAt = now
				if err := tx.Save(&slot).Error; err != nil {
					return fmt.Errorf("reserve slot: %w", err)
				}
			}
		}

		if voucher != nil {
			redemption := domain.VoucherRedemption{
				ID:        uuid.NewString(),
				VoucherID: voucher.ID,
				UserID:    cmd.UserID,
				OrderID:   order.ID,
				Amount:    discount,
				CreatedAt: now,
			}
			if err := tx.Create(&redemption).Error; err != nil {
				return fmt.Errorf("create redemption: %w", err)
			}
		}

		return nil
	})
	return order, err
}

func (s *Store) SavePayment(ctx context.Context, orderID string, payment *domain.Payment) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order domain.Order
		if err := tx.Where("id = ?", orderID).First(&order).Error; err != nil {
			return fmt.Errorf("find order: %w", err)
		}
		if err := tx.Create(payment).Error; err != nil {
			return fmt.Errorf("create payment: %w", err)
		}
		return nil
	})
}

func (s *Store) CancelAndRelease(ctx context.Context, orderID string, now time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order domain.Order
		if err := tx.Where("id = ?", orderID).First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return fmt.Errorf("find order: %w", err)
		}
		if err := tx.Model(&domain.InventorySlot{}).
			Where("order_item_id IN (SELECT id FROM order_items WHERE order_id = ?)", orderID).
			Updates(map[string]any{
				"status":         "available",
				"reserved_by":    nil,
				"reserved_until": nil,
				"order_item_id":  nil,
				"updated_at":     now,
			}).Error; err != nil {
			return fmt.Errorf("release slots: %w", err)
		}
		order.Status = domain.OrderCancelled
		order.UpdatedAt = now
		if err := tx.Save(&order).Error; err != nil {
			return fmt.Errorf("cancel order: %w", err)
		}
		return nil
	})
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
