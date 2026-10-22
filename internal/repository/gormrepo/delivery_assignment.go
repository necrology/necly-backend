package gormrepo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// This file holds the NEC-89 Fase 2 delivery allocation rules:
//
//   - one account serves at most five users (hard cap), and sharing_limit can
//     only lower that number;
//   - a buyer must not receive the same account twice for the same product
//     while another unused account is available;
//   - selecting, moving and replacing accounts all happen under row locks so
//     two admins cannot oversubscribe an account;
//   - the access window is per buyer slot, not the account expiry.

// effectiveSharingLimit applies the five-user product rule on top of the stored
// sharing_limit. Legacy accounts above five keep their existing rows but cannot
// receive new assignments.
func effectiveSharingLimit(account domain.InventoryAccount) int {
	limit := account.SharingLimit
	if limit > service.MaxSharingLimit {
		limit = service.MaxSharingLimit
	}
	if limit < 1 {
		limit = 1
	}
	return limit
}

type accountUsage struct {
	Rows  int64
	Users int64
}

type deliveryItemMeta struct {
	ProductID    string
	DurationDays int
}

func (s *Store) deliveryItemMetaTx(tx *gorm.DB, productPriceID string) (deliveryItemMeta, error) {
	var meta deliveryItemMeta
	if err := tx.Table("product_prices AS pp").
		Select("pp.product_id, COALESCE(pd.days, 0) AS duration_days").
		Joins("JOIN product_durations pd ON pd.id = pp.duration_id").
		Where("pp.id = ?", productPriceID).
		Scan(&meta).Error; err != nil {
		return meta, fmt.Errorf("load order item product: %w", err)
	}
	return meta, nil
}

// accountUsageTx counts active seat rows and distinct buyers. Released rows are
// historical audit data and no longer consume the account's capacity.
func accountUsageTx(tx *gorm.DB, accountID string) (accountUsage, error) {
	var row struct {
		Rows  int64
		Users int64
	}
	if err := tx.Table("inventory_slots AS s").
		Select(`COUNT(*) AS rows,
			COUNT(DISTINCT COALESCE(o.user_id, s.reserved_by))
				FILTER (WHERE COALESCE(o.user_id, s.reserved_by) IS NOT NULL) AS users`).
		Joins("LEFT JOIN order_items oi ON oi.id = s.order_item_id").
		Joins("LEFT JOIN orders o ON o.id = oi.order_id").
		Where("s.inventory_account_id = ? AND s.status <> ?", accountID, "released").
		Scan(&row).Error; err != nil {
		return accountUsage{}, fmt.Errorf("count account usage: %w", err)
	}
	return accountUsage{Rows: row.Rows, Users: row.Users}, nil
}

// accountUsageExcludingItemTx removes the current order item's rows from the
// count. Assigning to the same account is then a no-op instead of adding the
// item's quantity a second time.
func accountUsageExcludingItemTx(tx *gorm.DB, accountID, orderItemID string) (accountUsage, error) {
	var row struct {
		Rows  int64
		Users int64
	}
	if err := tx.Table("inventory_slots AS s").
		Select(`COUNT(*) AS rows,
			COUNT(DISTINCT COALESCE(o.user_id, s.reserved_by))
				FILTER (WHERE COALESCE(o.user_id, s.reserved_by) IS NOT NULL) AS users`).
		Joins("LEFT JOIN order_items oi ON oi.id = s.order_item_id").
		Joins("LEFT JOIN orders o ON o.id = oi.order_id").
		Where("s.inventory_account_id = ? AND s.status <> ? AND (s.order_item_id IS NULL OR s.order_item_id <> ?)",
			accountID, "released", orderItemID).
		Scan(&row).Error; err != nil {
		return accountUsage{}, fmt.Errorf("count account usage excluding item: %w", err)
	}
	return accountUsage{Rows: row.Rows, Users: row.Users}, nil
}

// accountUsedByCustomerTx answers "has this buyer already received this account
// for this product?". Only a successfully sent delivery counts: an order that is
// still waiting for approval has not used the account yet.
func accountUsedByCustomerTx(tx *gorm.DB, accountID, userID, productID, excludeOrderID string) (bool, error) {
	var count int64
	if err := tx.Table("inventory_slots AS s").
		Joins("JOIN order_items oi ON oi.id = s.order_item_id").
		Joins("JOIN orders o ON o.id = oi.order_id").
		Joins("JOIN product_prices pp ON pp.id = oi.product_price_id").
		Joins("JOIN order_deliveries d ON d.order_item_id = oi.id AND d.status = ?", domain.DeliverySent).
		Where("s.inventory_account_id = ? AND s.status <> ? AND o.user_id = ? AND pp.product_id = ? AND o.id <> ?",
			accountID, "released", userID, productID, excludeOrderID).
		Count(&count).Error; err != nil {
		return false, fmt.Errorf("check account reuse: %w", err)
	}
	return count > 0, nil
}

func lockAccountTx(tx *gorm.DB, accountID string) (*domain.InventoryAccount, error) {
	var account domain.InventoryAccount
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", accountID).First(&account).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, service.ErrInventoryAccountUnavailable
		}
		return nil, fmt.Errorf("load inventory account: %w", err)
	}
	return &account, nil
}

func isAccountSellable(account domain.InventoryAccount, now time.Time) bool {
	return account.Status == "active" && (account.ExpiresAt == nil || account.ExpiresAt.After(now))
}

// findUnusedAccountTx locks every active candidate for the product in a stable
// order and returns the first one that can absorb the item without exceeding
// the five-user cap. exclude accounts are still locked (for deterministic lock
// order) but are skipped; replacement uses this to avoid the current account.
func (s *Store) findUnusedAccountTx(tx *gorm.DB, productID, userID, excludeOrderID, orderItemID string, quantity int, now time.Time, exclude map[string]bool) (string, error) {
	var accounts []domain.InventoryAccount
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("product_id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)", productID, "active", now).
		Order("created_at ASC, id ASC").
		Find(&accounts).Error; err != nil {
		return "", fmt.Errorf("load candidate accounts: %w", err)
	}
	for _, account := range accounts {
		if exclude[account.ID] {
			continue
		}
		limit := effectiveSharingLimit(account)
		if quantity > limit {
			continue
		}
		usage, err := accountUsageExcludingItemTx(tx, account.ID, orderItemID)
		if err != nil {
			return "", err
		}
		if usage.Rows+int64(quantity) > int64(limit) {
			continue
		}
		// The row cap already enforces five users, but keep the explicit check
		// for legacy data and for clarity.
		addedUsers := int64(1)
		var currentUserCount int64
		if err := tx.Table("inventory_slots AS s").
			Joins("LEFT JOIN order_items oi ON oi.id = s.order_item_id").
			Joins("LEFT JOIN orders o ON o.id = oi.order_id").
			Where("s.inventory_account_id = ? AND s.status <> ? AND COALESCE(o.user_id, s.reserved_by) = ? AND (s.order_item_id IS NULL OR s.order_item_id <> ?)",
				account.ID, "released", userID, orderItemID).
			Count(&currentUserCount).Error; err != nil {
			return "", fmt.Errorf("count current user slots: %w", err)
		}
		if currentUserCount > 0 {
			addedUsers = 0
		}
		if usage.Users+addedUsers > int64(service.MaxSharingLimit) {
			continue
		}
		used, err := accountUsedByCustomerTx(tx, account.ID, userID, productID, excludeOrderID)
		if err != nil {
			return "", err
		}
		if used {
			continue
		}
		return account.ID, nil
	}
	return "", nil
}

func (s *Store) loadDeliveryContextTx(tx *gorm.DB, orderID, deliveryID string) (*domain.Order, *domain.OrderDelivery, *domain.OrderItem, deliveryItemMeta, []domain.InventorySlot, error) {
	var order domain.Order
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", orderID).First(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil, deliveryItemMeta{}, nil, service.ErrNotFound
		}
		return nil, nil, nil, deliveryItemMeta{}, nil, fmt.Errorf("load order: %w", err)
	}
	var delivery domain.OrderDelivery
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND order_id = ?", deliveryID, order.ID).First(&delivery).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil, deliveryItemMeta{}, nil, service.ErrDeliveryNotFound
		}
		return nil, nil, nil, deliveryItemMeta{}, nil, fmt.Errorf("load order delivery: %w", err)
	}
	var item domain.OrderItem
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND order_id = ?", delivery.OrderItemID, order.ID).First(&item).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil, deliveryItemMeta{}, nil, service.ErrDeliveryNotFound
		}
		return nil, nil, nil, deliveryItemMeta{}, nil, fmt.Errorf("load order item: %w", err)
	}
	meta, err := s.deliveryItemMetaTx(tx, item.ProductPriceID)
	if err != nil {
		return nil, nil, nil, deliveryItemMeta{}, nil, err
	}
	var slots []domain.InventorySlot
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("order_item_id = ? AND status <> ?", item.ID, "released").
		Order("id ASC").Find(&slots).Error; err != nil {
		return nil, nil, nil, deliveryItemMeta{}, nil, fmt.Errorf("load item slots: %w", err)
	}
	return &order, &delivery, &item, meta, slots, nil
}

// PrepareDeliveryAssignment is the automatic selection path used right before
// the first email. It keeps the current assignment when every account is still
// active, not reused and has room; otherwise it moves the item's slots to a
// fresh account.
func (s *Store) PrepareDeliveryAssignment(ctx context.Context, orderID, orderItemID string, now time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order domain.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", orderID).First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return service.ErrNotFound
			}
			return fmt.Errorf("load order: %w", err)
		}
		var item domain.OrderItem
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND order_id = ?", orderItemID, order.ID).First(&item).Error; err != nil {
			return service.ErrDeliveryNotFound
		}
		meta, err := s.deliveryItemMetaTx(tx, item.ProductPriceID)
		if err != nil {
			return err
		}
		var slots []domain.InventorySlot
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("order_item_id = ? AND status <> ?", item.ID, "released").
			Order("id ASC").Find(&slots).Error; err != nil {
			return fmt.Errorf("load item slots: %w", err)
		}
		if len(slots) < item.Quantity {
			return service.ErrOutOfStock
		}

		// Lock every account currently backing the item before validating it.
		accountIDs := make([]string, 0, len(slots))
		seen := make(map[string]bool, len(slots))
		for _, slot := range slots {
			if !seen[slot.InventoryAccountID] {
				seen[slot.InventoryAccountID] = true
				accountIDs = append(accountIDs, slot.InventoryAccountID)
			}
		}
		sort.Strings(accountIDs)
		currentAssignmentsValid := true
		for _, accountID := range accountIDs {
			account, err := lockAccountTx(tx, accountID)
			if err != nil {
				currentAssignmentsValid = false
				break
			}
			if !isAccountSellable(*account, now) {
				currentAssignmentsValid = false
				break
			}
			used, err := accountUsedByCustomerTx(tx, account.ID, order.UserID, meta.ProductID, order.ID)
			if err != nil {
				return err
			}
			if used {
				currentAssignmentsValid = false
				break
			}
			usage, err := accountUsageTx(tx, account.ID)
			if err != nil {
				return err
			}
			if usage.Rows > int64(effectiveSharingLimit(*account)) {
				currentAssignmentsValid = false
				break
			}
		}
		if currentAssignmentsValid {
			return nil
		}

		chosen, err := s.findUnusedAccountTx(tx, meta.ProductID, order.UserID, order.ID, item.ID, item.Quantity, now, nil)
		if err != nil {
			return err
		}
		if chosen == "" {
			return service.ErrNoAlternativeAccount
		}
		if err := tx.Model(&domain.InventorySlot{}).
			Where("order_item_id = ? AND status <> ?", item.ID, "released").
			Updates(map[string]any{"inventory_account_id": chosen, "updated_at": now}).Error; err != nil {
			return fmt.Errorf("move delivery slots: %w", err)
		}
		return nil
	})
}

// AssignDeliveryAccount handles the explicit admin choice. It validates the
// target under locks, enforces the anti-reuse rule and moves every slot of the
// item in one transaction.
func (s *Store) AssignDeliveryAccount(ctx context.Context, orderID, deliveryID, accountID string, now time.Time) (*domain.OrderDelivery, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		order, delivery, item, meta, slots, err := s.loadDeliveryContextTx(tx, orderID, deliveryID)
		if err != nil {
			return err
		}
		if !service.IsDeliveryApprovedStatus(order.Status) {
			return service.ErrOrderNotApproved
		}
		if delivery.Status == domain.DeliverySent {
			return service.ErrDeliveryAlreadySent
		}
		if len(slots) < item.Quantity {
			return service.ErrOutOfStock
		}

		account, err := lockAccountTx(tx, accountID)
		if err != nil {
			return err
		}
		if account.ProductID != meta.ProductID {
			return service.ErrInventoryAccountMismatch
		}
		if !isAccountSellable(*account, now) {
			return service.ErrInventoryAccountUnavailable
		}
		used, err := accountUsedByCustomerTx(tx, account.ID, order.UserID, meta.ProductID, order.ID)
		if err != nil {
			return err
		}
		if used {
			alternative, err := s.findUnusedAccountTx(tx, meta.ProductID, order.UserID, order.ID, item.ID, item.Quantity, now, map[string]bool{account.ID: true})
			if err != nil {
				return err
			}
			if alternative != "" {
				return service.ErrAccountUsedByCustomer
			}
			return service.ErrNoAlternativeAccount
		}
		usage, err := accountUsageExcludingItemTx(tx, account.ID, item.ID)
		if err != nil {
			return err
		}
		if usage.Rows+int64(item.Quantity) > int64(effectiveSharingLimit(*account)) {
			return service.ErrDeliveryAccountFull
		}
		if err := tx.Model(&domain.InventorySlot{}).
			Where("order_item_id = ? AND status <> ?", item.ID, "released").
			Updates(map[string]any{"inventory_account_id": account.ID, "updated_at": now}).Error; err != nil {
			return fmt.Errorf("assign delivery slots: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetOrderDelivery(ctx, orderID, deliveryID)
}

// ReplaceDeliveryAccount releases expired (or complaint-approved) slots and
// provisions fresh reserved slots on another account. The audit row is written
// in the same transaction as the move; the caller sends the email afterwards.
func (s *Store) ReplaceDeliveryAccount(ctx context.Context, orderID, deliveryID, accountID, reason string, complaintApproved bool, actorUserID string, now time.Time) (*domain.DeliveryReplacement, error) {
	var replacement *domain.DeliveryReplacement
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		order, delivery, item, meta, slots, err := s.loadDeliveryContextTx(tx, orderID, deliveryID)
		if err != nil {
			return err
		}
		if delivery.Status != domain.DeliverySent {
			return service.ErrDeliveryNotSent
		}
		if len(slots) == 0 {
			return service.ErrDeliveryNotSent
		}
		if meta.DurationDays <= 0 {
			return service.ErrDeliveryDurationUnavailable
		}

		account, err := lockAccountTx(tx, accountID)
		if err != nil {
			return err
		}
		if account.ProductID != meta.ProductID {
			return service.ErrInventoryAccountMismatch
		}
		if !isAccountSellable(*account, now) {
			return service.ErrInventoryAccountUnavailable
		}

		oldAccountIDs := make(map[string]bool, len(slots))
		oldSlotIDs := make(domain.StringArray, 0, len(slots))
		unexpired := false
		for _, slot := range slots {
			oldAccountIDs[slot.InventoryAccountID] = true
			oldSlotIDs = append(oldSlotIDs, slot.ID)
			if slot.AccessExpiresAt == nil || slot.AccessExpiresAt.After(now) {
				unexpired = true
			}
		}
		if oldAccountIDs[account.ID] {
			return service.ErrAccountNotChanged
		}
		if unexpired && !complaintApproved {
			return service.ErrAccountNotExpired
		}

		used, err := accountUsedByCustomerTx(tx, account.ID, order.UserID, meta.ProductID, order.ID)
		if err != nil {
			return err
		}
		if used {
			alternative, err := s.findUnusedAccountTx(tx, meta.ProductID, order.UserID, order.ID, item.ID, item.Quantity, now, oldAccountIDs)
			if err != nil {
				return err
			}
			if alternative != "" {
				return service.ErrAccountUsedByCustomer
			}
			return service.ErrNoAlternativeAccount
		}
		usage, err := accountUsageExcludingItemTx(tx, account.ID, item.ID)
		if err != nil {
			return err
		}
		if usage.Rows+int64(item.Quantity) > int64(effectiveSharingLimit(*account)) {
			return service.ErrDeliveryAccountFull
		}

		if err := tx.Model(&domain.InventorySlot{}).
			Where("order_item_id = ? AND status <> ?", item.ID, "released").
			Updates(map[string]any{
				"status":         "released",
				"reserved_by":    nil,
				"reserved_until": nil,
				"order_item_id":  nil,
				"updated_at":     now,
			}).Error; err != nil {
			return fmt.Errorf("release old slots: %w", err)
		}

		expiresAt := now.AddDate(0, 0, meta.DurationDays)
		newSlots := make([]domain.InventorySlot, 0, item.Quantity)
		newSlotIDs := make(domain.StringArray, 0, item.Quantity)
		for i := 0; i < item.Quantity; i++ {
			slotID := uuid.NewString()
			newSlotIDs = append(newSlotIDs, slotID)
			reservedBy := order.UserID
			orderItemID := item.ID
			activatedAt := now
			newSlots = append(newSlots, domain.InventorySlot{
				ID: slotID, InventoryAccountID: account.ID, ProductPriceID: item.ProductPriceID,
				Status: "reserved", ReservedBy: &reservedBy, OrderItemID: &orderItemID,
				ActivatedAt: &activatedAt, AccessExpiresAt: &expiresAt,
				CreatedAt: now, UpdatedAt: now,
			})
		}
		if err := tx.Create(&newSlots).Error; err != nil {
			return fmt.Errorf("create replacement slots: %w", err)
		}

		var replacedBy *string
		if actorUserID != "" {
			replacedBy = &actorUserID
		}
		row := domain.DeliveryReplacement{
			ID: uuid.NewString(), OrderID: order.ID, OrderItemID: item.ID, DeliveryID: delivery.ID,
			ReplacementOf: oldSlotIDs[0], OldInventoryAccountID: stringPtr(slots[0].InventoryAccountID),
			InventoryAccountID: account.ID, OldSlotIDs: oldSlotIDs, NewSlotIDs: newSlotIDs,
			Reason: reason, ComplaintApproved: complaintApproved, Status: "pending",
			ReplacedBy: replacedBy, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("create delivery replacement: %w", err)
		}
		replacement = &row
		return nil
	})
	if err != nil {
		return nil, err
	}
	return replacement, nil
}

func stringPtr(value string) *string { return &value }

func (s *Store) MarkDeliveryReplacementSent(ctx context.Context, replacementID string, now time.Time) error {
	result := s.db.WithContext(ctx).Model(&domain.DeliveryReplacement{}).
		Where("id = ? AND status IN ?", replacementID, []string{"pending", "sending", "failed"}).
		Updates(map[string]any{
			"status": "sent", "email_sent_at": now, "updated_at": now, "error_code": nil,
		})
	if result.Error != nil {
		return fmt.Errorf("mark replacement sent: %w", result.Error)
	}
	return nil
}

func (s *Store) MarkDeliveryReplacementFailed(ctx context.Context, replacementID, errorCode string, now time.Time) error {
	result := s.db.WithContext(ctx).Model(&domain.DeliveryReplacement{}).
		Where("id = ? AND status IN ?", replacementID, []string{"pending", "sending"}).
		Updates(map[string]any{
			"status": "failed", "error_code": errorCode, "updated_at": now,
			"attempts": gorm.Expr("attempts + 1"),
		})
	if result.Error != nil {
		return fmt.Errorf("mark replacement failed: %w", result.Error)
	}
	return nil
}

// ActivateDeliveryAccess fills the per-buyer access window. A retry must not
// move the window: COALESCE keeps the first activation and expiry.
func (s *Store) ActivateDeliveryAccess(ctx context.Context, orderID, orderItemID string, now, expiresAt time.Time) error {
	result := s.db.WithContext(ctx).Model(&domain.InventorySlot{}).
		Where("order_item_id = ? AND status <> ?", orderItemID, "released").
		Where("EXISTS (SELECT 1 FROM order_items oi WHERE oi.id = inventory_slots.order_item_id AND oi.order_id = ?)", orderID).
		Updates(map[string]any{
			"activated_at":      gorm.Expr("COALESCE(activated_at, ?)", now),
			"access_expires_at": gorm.Expr("COALESCE(access_expires_at, ?)", expiresAt),
			"updated_at":        now,
		})
	if result.Error != nil {
		return fmt.Errorf("activate delivery access: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return service.ErrDeliveryNotFound
	}
	return nil
}

// LoadDeliveryOptions returns credential-free candidates per order item. The
// order and its delivery rows must already exist.
func (s *Store) LoadDeliveryOptions(ctx context.Context, orderID string) ([]service.DeliveryItemOptions, error) {
	var order domain.Order
	if err := s.db.WithContext(ctx).Where("id = ?", orderID).First(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, service.ErrNotFound
		}
		return nil, fmt.Errorf("load order: %w", err)
	}

	type itemRow struct {
		DeliveryID     string
		OrderItemID    string
		ProductPriceID string
		ProductName    string
		DurationName   string
		Quantity       int
	}
	var items []itemRow
	if err := s.db.WithContext(ctx).Table("order_items AS oi").
		Select("d.id AS delivery_id, oi.id AS order_item_id, oi.product_price_id, oi.product_name, oi.duration_name, oi.quantity").
		Joins("JOIN order_deliveries d ON d.order_item_id = oi.id").
		Where("oi.order_id = ?", order.ID).
		Order("oi.id ASC").
		Scan(&items).Error; err != nil {
		return nil, fmt.Errorf("load order item delivery options: %w", err)
	}

	now := s.nowUTC()
	result := make([]service.DeliveryItemOptions, 0, len(items))
	for _, item := range items {
		meta, err := s.deliveryItemMetaTx(s.db.WithContext(ctx), item.ProductPriceID)
		if err != nil {
			return nil, err
		}
		var slots []domain.InventorySlot
		if err := s.db.WithContext(ctx).
			Where("order_item_id = ? AND status <> ?", item.OrderItemID, "released").
			Find(&slots).Error; err != nil {
			return nil, fmt.Errorf("load item slots: %w", err)
		}
		assignedPerAccount := make(map[string]int, len(slots))
		assignedIDs := make([]string, 0, len(slots))
		seenAssigned := make(map[string]bool, len(slots))
		for _, slot := range slots {
			assignedPerAccount[slot.InventoryAccountID]++
			if !seenAssigned[slot.InventoryAccountID] {
				seenAssigned[slot.InventoryAccountID] = true
				assignedIDs = append(assignedIDs, slot.InventoryAccountID)
			}
		}

		var accounts []domain.InventoryAccount
		if err := s.db.WithContext(ctx).
			Where("product_id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)", meta.ProductID, "active", now).
			Order("created_at ASC, id ASC").
			Find(&accounts).Error; err != nil {
			return nil, fmt.Errorf("load eligible accounts: %w", err)
		}
		options := make([]service.DeliveryAccountOption, 0, len(accounts))
		for _, account := range accounts {
			usage, err := accountUsageExcludingItemTx(s.db.WithContext(ctx), account.ID, item.OrderItemID)
			if err != nil {
				return nil, err
			}
			used, err := accountUsedByCustomerTx(s.db.WithContext(ctx), account.ID, order.UserID, meta.ProductID, order.ID)
			if err != nil {
				return nil, err
			}
			limit := effectiveSharingLimit(account)
			remaining := limit - int(usage.Rows) - assignedPerAccount[account.ID]
			if remaining < 0 {
				remaining = 0
			}
			canAssign := !used && int(usage.Rows)+item.Quantity <= limit &&
				usage.Users+int64(boolToInt(assignedPerAccount[account.ID] == 0)) <= int64(service.MaxSharingLimit)
			options = append(options, service.DeliveryAccountOption{
				InventoryAccountID: account.ID,
				Label:              account.Label,
				Status:             account.Status,
				ExpiresAt:          account.ExpiresAt,
				DaysRemaining:      domain.DaysRemaining(account.ExpiresAt, now),
				SharingLimit:       limit,
				AssignedUsers:      int(usage.Users),
				UsedSlots:          int(usage.Rows) + assignedPerAccount[account.ID],
				RemainingSlots:     remaining,
				UsedByCustomer:     used,
				AssignedToItem:     assignedPerAccount[account.ID] > 0,
				CanAssign:          canAssign,
			})
		}
		sort.SliceStable(options, func(i, j int) bool {
			if options[i].CanAssign != options[j].CanAssign {
				return options[i].CanAssign
			}
			if options[i].UsedByCustomer != options[j].UsedByCustomer {
				return !options[i].UsedByCustomer
			}
			return options[i].RemainingSlots > options[j].RemainingSlots
		})
		result = append(result, service.DeliveryItemOptions{
			DeliveryID: item.DeliveryID, OrderItemID: item.OrderItemID,
			ProductID: meta.ProductID, ProductPriceID: item.ProductPriceID,
			ProductName: item.ProductName, DurationName: item.DurationName,
			Quantity: item.Quantity, AssignedAccountIDs: assignedIDs, Accounts: options,
		})
	}
	return result, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// attachDeliveryDetails fills the credential-free presentation fields. It is
// called by both the single-delivery and list reads so the admin detail always
// shows the chosen accounts and replacement history.
func (s *Store) attachDeliveryDetails(ctx context.Context, orderID string, rows []domain.OrderDelivery) error {
	if len(rows) == 0 {
		return nil
	}
	type accountRow struct {
		OrderItemID        string
		InventoryAccountID string
		Label              string
		Status             string
		ActivatedAt        *time.Time
		AccessExpiresAt    *time.Time
	}
	var accounts []accountRow
	if err := s.db.WithContext(ctx).Table("inventory_slots AS s").
		Select("s.order_item_id, s.inventory_account_id, COALESCE(a.label, '') AS label, a.status, s.activated_at, s.access_expires_at").
		Joins("JOIN inventory_accounts a ON a.id = s.inventory_account_id").
		Where("s.order_item_id IN (SELECT id FROM order_items WHERE order_id = ?) AND s.status <> ?", orderID, "released").
		Order("s.created_at ASC, s.id ASC").
		Scan(&accounts).Error; err != nil {
		return fmt.Errorf("load delivery assigned accounts: %w", err)
	}
	now := s.nowUTC()
	byItem := make(map[string][]domain.DeliveryAssignedAccount)
	for _, row := range accounts {
		byItem[row.OrderItemID] = append(byItem[row.OrderItemID], domain.DeliveryAssignedAccount{
			InventoryAccountID: row.InventoryAccountID, Label: row.Label, Status: row.Status,
			ActivatedAt: row.ActivatedAt, AccessExpiresAt: row.AccessExpiresAt,
			DaysRemaining: domain.DaysRemaining(row.AccessExpiresAt, now),
		})
	}

	var replacements []domain.DeliveryReplacement
	if err := s.db.WithContext(ctx).
		Where("order_id = ?", orderID).
		Order("created_at DESC, id DESC").
		Find(&replacements).Error; err != nil {
		return fmt.Errorf("load delivery replacements: %w", err)
	}
	byDelivery := make(map[string][]domain.DeliveryReplacement)
	for _, replacement := range replacements {
		byDelivery[replacement.DeliveryID] = append(byDelivery[replacement.DeliveryID], replacement)
	}
	for i := range rows {
		rows[i].AssignedAccounts = byItem[rows[i].OrderItemID]
		rows[i].Replacements = byDelivery[rows[i].ID]
	}
	return nil
}

// normalizeReason is shared by the service validation and repository guard.
func normalizeReason(reason string) string {
	return strings.TrimSpace(reason)
}
