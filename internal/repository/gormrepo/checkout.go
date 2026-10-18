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

// providerCallLease bounds how long a crashed request may leave an attempt in
// the "creating" state before another request may recover it. The lease is a
// timestamp check, not a database lock held across the provider network call.
const providerCallLease = 30 * time.Second

func (s *Store) BeginCheckout(ctx context.Context, cmd service.CheckoutCommand) (*service.CheckoutReservation, error) {
	var result *service.CheckoutReservation

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing domain.Order
		err := tx.Preload("Items", orderItemsByProductPrice).
			Where("user_id = ? AND idempotency_key = ?", cmd.UserID, cmd.IdempotencyKey).
			First(&existing).Error
		if err == nil {
			if existing.RequestFingerprint == nil || *existing.RequestFingerprint != cmd.Fingerprint {
				// Legacy rows without a fingerprint cannot be proven equal, so
				// they are never replayed blindly.
				return service.ErrIdempotencyConflict
			}
			attempt, attemptErr := findByKey(tx, cmd.UserID, cmd.IdempotencyKey)
			if attemptErr != nil {
				return attemptErr
			}
			payment, paymentErr := latestPaymentForOrder(tx, existing.ID)
			if paymentErr != nil {
				return paymentErr
			}
			// Payments are deliberately not preloaded into the serialized
			// order: the 201 and 200 bodies must be identical and the C-01
			// fixture does not include order.payments.
			result = &service.CheckoutReservation{
				State: service.CheckoutReplay, Order: &existing,
				Payment: payment, Attempt: attempt,
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("check idempotency: %w", err)
		}

		attempt, err := findByKey(tx, cmd.UserID, cmd.IdempotencyKey)
		if err != nil {
			return err
		}
		if attempt != nil {
			if attempt.RequestFingerprint != cmd.Fingerprint {
				return service.ErrIdempotencyConflict
			}
			result = &service.CheckoutReservation{State: service.CheckoutInProgress, Attempt: attempt}
			return nil
		}

		attempt = &domain.CheckoutAttempt{
			ID: uuid.NewString(), UserID: cmd.UserID, IdempotencyKey: cmd.IdempotencyKey,
			RequestFingerprint: cmd.Fingerprint, State: "started", CreatedAt: cmd.Now, UpdatedAt: cmd.Now,
		}
		created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(attempt)
		if created.Error != nil {
			return fmt.Errorf("create checkout attempt: %w", created.Error)
		}
		if created.RowsAffected == 0 {
			// A concurrent transaction inserted the same owner/key first.
			// ON CONFLICT waits for it to commit, so this read now sees it.
			other, err := findByKey(tx, cmd.UserID, cmd.IdempotencyKey)
			if err != nil {
				return err
			}
			if other == nil {
				return fmt.Errorf("idempotency attempt disappeared")
			}
			if other.RequestFingerprint != cmd.Fingerprint {
				return service.ErrIdempotencyConflict
			}
			result = &service.CheckoutReservation{State: service.CheckoutInProgress, Attempt: other}
			return nil
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
		if cmd.ExpectedSubtotal != nil && *cmd.ExpectedSubtotal != subtotal {
			return &service.PriceChangedError{CurrentSubtotal: subtotal, Currency: "IDR"}
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
				// An inactive, expired, exhausted or minimum-not-met voucher is
				// indistinguishable from an unknown code to the caller, per the
				// C-01 error contract: 404 not_found with no mutation.
				return service.ErrNotFound
			}
			discount = disc
		}

		// Persist the resolved voucher UUID, never the human-entered code.
		var voucherID *string
		if voucher != nil {
			voucherID = &voucher.ID
		}
		fingerprint := cmd.Fingerprint

		now := cmd.Now
		order := &domain.Order{
			ID:             uuid.NewString(),
			Number:         fmt.Sprintf("ORD-%d-%s", now.UnixMilli(), uuid.NewString()[:8]),
			UserID:         cmd.UserID,
			VoucherID:      voucherID,
			Status:         domain.OrderPending,
			Subtotal:       subtotal,
			Discount:       discount,
			Total:          subtotal - discount,
			Currency:       "IDR",
			PaymentDueAt:   cmd.PaymentDueAt,
			IdempotencyKey: cmd.IdempotencyKey,
			// Stored so a replay with a different basket is a conflict rather
			// than a silently different order.
			RequestFingerprint: &fingerprint,
			CreatedAt:          now,
			UpdatedAt:          now,
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

		attempt.State = "reserved"
		attempt.OrderID = &order.ID
		attempt.UpdatedAt = now
		if err := tx.Save(attempt).Error; err != nil {
			return fmt.Errorf("mark attempt reserved: %w", err)
		}

		// The checkout fixture requires order.items in the response. GORM does
		// not fill the association from the per-row inserts above.
		var loaded domain.Order
		if err := tx.Preload("Items", orderItemsByProductPrice).Where("id = ?", order.ID).First(&loaded).Error; err != nil {
			return fmt.Errorf("reload order: %w", err)
		}
		result = &service.CheckoutReservation{State: service.CheckoutReserved, Order: &loaded, Attempt: attempt}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func findByKey(tx *gorm.DB, userID, idempotencyKey string) (*domain.CheckoutAttempt, error) {
	var attempt domain.CheckoutAttempt
	err := tx.Where("user_id = ? AND idempotency_key = ?", userID, idempotencyKey).First(&attempt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load checkout attempt: %w", err)
	}
	return &attempt, nil
}

// orderItemsByProductPrice gives the checkout response a deterministic item
// order. order_items has no created_at column, so "creation order" is not
// representable; sorting by the canonical price UUID is stable across replays
// and matches the fingerprint ordering.
func orderItemsByProductPrice(db *gorm.DB) *gorm.DB {
	return db.Order("order_items.product_price_id ASC")
}

func latestPaymentForOrder(tx *gorm.DB, orderID string) (*domain.Payment, error) {
	var payment domain.Payment
	err := tx.Where("order_id = ?", orderID).Order("created_at DESC, id DESC").First(&payment).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load payment: %w", err)
	}
	return &payment, nil
}

func (s *Store) ClaimProviderCall(ctx context.Context, attemptID string, now time.Time) (bool, error) {
	staleBefore := now.Add(-providerCallLease)
	result := s.db.WithContext(ctx).Model(&domain.CheckoutAttempt{}).
		Where("id = ? AND (state IN ('reserved', 'started') OR (state = 'creating' AND updated_at < ?))", attemptID, staleBefore).
		Updates(map[string]any{"state": "creating", "updated_at": now})
	if result.Error != nil {
		return false, fmt.Errorf("claim provider call: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

func (s *Store) SaveProviderResult(ctx context.Context, attemptID, provider, providerPaymentID, checkoutURL string, now time.Time) error {
	result := s.db.WithContext(ctx).Model(&domain.CheckoutAttempt{}).
		Where("id = ? AND state = ?", attemptID, "creating").
		Updates(map[string]any{
			"state": "provider_created", "provider": provider,
			"provider_payment_id": providerPaymentID, "checkout_url": checkoutURL, "updated_at": now,
		})
	if result.Error != nil {
		return fmt.Errorf("save provider result: %w", result.Error)
	}
	if result.RowsAffected == 1 {
		return nil
	}
	// A retry may have already recorded the same reference. Treat that as a
	// successful idempotent write, but never overwrite a different payment id.
	var attempt domain.CheckoutAttempt
	if err := s.db.WithContext(ctx).Where("id = ?", attemptID).First(&attempt).Error; err != nil {
		return fmt.Errorf("save provider result: %w", err)
	}
	if attempt.ProviderPaymentID != nil && *attempt.ProviderPaymentID == providerPaymentID {
		return nil
	}
	return fmt.Errorf("save provider result: attempt %s is in state %s", attemptID, attempt.State)
}

func (s *Store) CompleteCheckout(ctx context.Context, attemptID string, payment *domain.Payment) (*domain.Payment, error) {
	var saved *domain.Payment
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var attempt domain.CheckoutAttempt
		if err := tx.Where("id = ?", attemptID).First(&attempt).Error; err != nil {
			return fmt.Errorf("load attempt: %w", err)
		}
		if attempt.OrderID == nil {
			return fmt.Errorf("attempt %s has no order", attemptID)
		}
		var order domain.Order
		if err := tx.Where("id = ?", *attempt.OrderID).First(&order).Error; err != nil {
			return fmt.Errorf("load order: %w", err)
		}

		var existing domain.Payment
		err := tx.Where("provider_payment_id = ?", payment.ProviderPaymentID).First(&existing).Error
		switch {
		case err == nil:
			saved = &existing
		case errors.Is(err, gorm.ErrRecordNotFound):
			payment.OrderID = order.ID
			if err := tx.Create(payment).Error; err != nil {
				return fmt.Errorf("create payment: %w", err)
			}
			saved = payment
		default:
			return fmt.Errorf("check existing payment: %w", err)
		}

		attempt.State = "completed"
		attempt.OrderID = &order.ID
		attempt.Provider = &saved.Provider
		attempt.ProviderPaymentID = &saved.ProviderPaymentID
		attempt.CheckoutURL = &saved.CheckoutURL
		attempt.ErrorCode = nil
		attempt.UpdatedAt = payment.UpdatedAt
		if err := tx.Save(&attempt).Error; err != nil {
			return fmt.Errorf("complete attempt: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return saved, nil
}

func (s *Store) FailCheckout(ctx context.Context, attemptID, errorCode string, now time.Time) error {
	return s.db.WithContext(ctx).Model(&domain.CheckoutAttempt{}).
		Where("id = ? AND state IN ('started', 'reserved', 'creating')", attemptID).
		Updates(map[string]any{"state": "failed", "error_code": errorCode, "updated_at": now}).Error
}

func (s *Store) GetCheckoutProgress(ctx context.Context, userID, idempotencyKey string) (*service.CheckoutProgress, error) {
	attempt, err := findByKey(s.db.WithContext(ctx), userID, idempotencyKey)
	if err != nil {
		return nil, err
	}
	var order domain.Order
	err = s.db.WithContext(ctx).Preload("Items", orderItemsByProductPrice).
		Where("user_id = ? AND idempotency_key = ?", userID, idempotencyKey).
		First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &service.CheckoutProgress{Attempt: attempt}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load checkout order: %w", err)
	}
	payment, err := latestPaymentForOrder(s.db.WithContext(ctx), order.ID)
	if err != nil {
		return nil, err
	}
	return &service.CheckoutProgress{Order: &order, Payment: payment, Attempt: attempt}, nil
}

// CancelAndRelease releases inventory exactly once and only while the order is
// still waiting for payment. The order row is locked before the status/payment
// checks, and the cancellation is an atomic conditional update, so an admin or
// webhook that commits `paid` concurrently can never be overwritten.
func (s *Store) CancelAndRelease(ctx context.Context, orderID string, now time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order domain.Order
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", orderID).First(&order).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("find order: %w", err)
		}
		if order.Status != domain.OrderPending {
			return nil
		}
		var payments int64
		if err := tx.Model(&domain.Payment{}).Where("order_id = ?", orderID).Count(&payments).Error; err != nil {
			return fmt.Errorf("count payments: %w", err)
		}
		if payments > 0 {
			// A payment was persisted; never cancel it because a later retry or
			// provider timeout failed.
			return nil
		}
		cancelled := tx.Model(&domain.Order{}).
			Where("id = ? AND status = ?", orderID, domain.OrderPending).
			Updates(map[string]any{"status": domain.OrderCancelled, "updated_at": now})
		if cancelled.Error != nil {
			return fmt.Errorf("cancel order: %w", cancelled.Error)
		}
		if cancelled.RowsAffected != 1 {
			// A concurrent transaction changed the status after our locked
			// read. Do not release stock for an order we did not cancel.
			return nil
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
		return nil
	})
}
