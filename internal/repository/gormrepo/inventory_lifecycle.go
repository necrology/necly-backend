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

// ExpireInventory applies the daily lifecycle policy in one bounded
// transaction. Accounts are locked before slots to keep the same lock order as
// account update/delete, and SKIP LOCKED lets a second application instance
// process a different batch instead of blocking.
func (s *Store) ExpireInventory(ctx context.Context, now time.Time, limit int) (service.InventoryExpiryResult, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > 1000 {
		limit = 1000
	}
	result := service.InventoryExpiryResult{}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var accountIDs []string
		if err := tx.Model(&domain.InventoryAccount{}).
			Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ? AND expires_at IS NOT NULL AND expires_at <= ?", service.InventoryStatusActive, now).
			Order("expires_at ASC, id ASC").
			Limit(limit).
			Pluck("id", &accountIDs).Error; err != nil {
			return fmt.Errorf("select expired inventory accounts: %w", err)
		}
		if len(accountIDs) > 0 {
			update := tx.Model(&domain.InventoryAccount{}).
				Where("id IN ? AND status = ? AND expires_at IS NOT NULL AND expires_at <= ?",
					accountIDs, service.InventoryStatusActive, now).
				Updates(map[string]any{"status": service.InventoryStatusExpired, "updated_at": now})
			if update.Error != nil {
				return fmt.Errorf("expire inventory accounts: %w", update.Error)
			}
			result.AccountIDs = accountIDs
		}

		var slotIDs []string
		if err := tx.Model(&domain.InventorySlot{}).
			Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("access_expires_at IS NOT NULL AND access_expires_at <= ? AND status NOT IN ?",
				now, []string{"expired", "released"}).
			Order("access_expires_at ASC, id ASC").
			Limit(limit).
			Pluck("id", &slotIDs).Error; err != nil {
			return fmt.Errorf("select expired inventory slots: %w", err)
		}
		if len(slotIDs) > 0 {
			update := tx.Model(&domain.InventorySlot{}).
				Where("id IN ? AND access_expires_at IS NOT NULL AND access_expires_at <= ? AND status NOT IN ?",
					slotIDs, now, []string{"expired", "released"}).
				Updates(map[string]any{"status": "expired", "updated_at": now})
			if update.Error != nil {
				return fmt.Errorf("expire inventory slots: %w", update.Error)
			}
			result.SlotIDs = slotIDs
		}
		return nil
	})
	if err != nil {
		return service.InventoryExpiryResult{}, err
	}
	return result, nil
}

// ListSlotExpiryReminders returns slots inside one H-N window that belong to a
// real buyer. The recipient is the order owner, with reserved_by as the legacy
// fallback for rows whose order row is no longer reachable.
func (s *Store) ListSlotExpiryReminders(ctx context.Context, now time.Time, windowDays int) ([]service.InventoryExpiryReminder, error) {
	after, upTo := service.InventoryReminderWindow(now, windowDays)
	type reminderRow struct {
		TargetID     string
		UserID       string
		ExpiresAt    time.Time
		ProductName  string
		AccountLabel string
	}
	var rows []reminderRow
	if err := s.db.WithContext(ctx).Table("inventory_slots AS s").
		Select(`s.id AS target_id,
			COALESCE(o.user_id, s.reserved_by) AS user_id,
			s.access_expires_at AS expires_at,
			COALESCE(p.name, '') AS product_name,
			COALESCE(a.label, '') AS account_label`).
		Joins("JOIN inventory_accounts a ON a.id = s.inventory_account_id").
		Joins("JOIN product_prices pp ON pp.id = s.product_price_id").
		Joins("JOIN products p ON p.id = pp.product_id").
		Joins("LEFT JOIN order_items oi ON oi.id = s.order_item_id").
		Joins("LEFT JOIN orders o ON o.id = oi.order_id").
		Where("s.access_expires_at > ? AND s.access_expires_at <= ?", after, upTo).
		Where("s.status NOT IN ?", []string{"expired", "released"}).
		Where("COALESCE(o.user_id, s.reserved_by) IS NOT NULL").
		Order("s.access_expires_at ASC, s.id ASC").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list slot expiry reminders: %w", err)
	}
	reminders := make([]service.InventoryExpiryReminder, len(rows))
	for i, row := range rows {
		reminders[i] = service.InventoryExpiryReminder{
			TargetKind: "slot", TargetID: row.TargetID, UserID: row.UserID,
			ExpiresAt: row.ExpiresAt, ProductName: row.ProductName, AccountLabel: row.AccountLabel,
		}
	}
	return reminders, nil
}

// ListAccountExpiryReminders returns active provider accounts inside one H-N
// window. These reminders are intended for administrators because renewing the
// provider subscription is an operator action.
func (s *Store) ListAccountExpiryReminders(ctx context.Context, now time.Time, windowDays int) ([]service.InventoryExpiryReminder, error) {
	after, upTo := service.InventoryReminderWindow(now, windowDays)
	type reminderRow struct {
		TargetID     string
		ExpiresAt    time.Time
		ProductName  string
		AccountLabel string
	}
	var rows []reminderRow
	if err := s.db.WithContext(ctx).Table("inventory_accounts AS a").
		Select(`a.id AS target_id,
			a.expires_at AS expires_at,
			COALESCE(p.name, '') AS product_name,
			COALESCE(a.label, '') AS account_label`).
		Joins("JOIN products p ON p.id = a.product_id").
		Where("a.status = ? AND a.expires_at > ? AND a.expires_at <= ?", service.InventoryStatusActive, after, upTo).
		Order("a.expires_at ASC, a.id ASC").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list account expiry reminders: %w", err)
	}
	reminders := make([]service.InventoryExpiryReminder, len(rows))
	for i, row := range rows {
		reminders[i] = service.InventoryExpiryReminder{
			TargetKind: "account", TargetID: row.TargetID,
			ExpiresAt: row.ExpiresAt, ProductName: row.ProductName, AccountLabel: row.AccountLabel,
		}
	}
	return reminders, nil
}

func (s *Store) ListActiveAdminIDs(ctx context.Context) ([]string, error) {
	var ids []string
	if err := s.db.WithContext(ctx).Model(&domain.User{}).
		Where("role = ? AND active = TRUE AND deleted_at IS NULL", domain.RoleAdmin).
		Order("id ASC").
		Pluck("id", &ids).Error; err != nil {
		return nil, fmt.Errorf("list active admins: %w", err)
	}
	return ids, nil
}

// CreateNotifications writes reminder rows idempotently. Existing notifications
// have a NULL dedupe_key; the unique index permits any number of NULL values,
// while each scheduled reminder carries a deterministic key.
func (s *Store) CreateNotifications(ctx context.Context, notifications []domain.Notification) (int, error) {
	if len(notifications) == 0 {
		return 0, nil
	}
	now := s.nowUTC()
	for i := range notifications {
		if notifications[i].ID == "" {
			notifications[i].ID = uuid.NewString()
		}
		if notifications[i].CreatedAt.IsZero() {
			notifications[i].CreatedAt = now
		}
	}
	created := s.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		CreateInBatches(notifications, 100)
	if created.Error != nil {
		return 0, fmt.Errorf("create notifications: %w", created.Error)
	}
	return int(created.RowsAffected), nil
}

// ExtendSlotAccess applies the perpanjangan formula under lock. The account is
// locked before the slot, matching UpdateAccount and DeleteAccount, and the new
// expiry is rejected when it would outlive the provider account.
func (s *Store) ExtendSlotAccess(ctx context.Context, slotID string, now time.Time, durationDays int) (*domain.InventorySlot, error) {
	if durationDays < 1 {
		return nil, service.ErrInvalidInput
	}
	var updated domain.InventorySlot
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var peek domain.InventorySlot
		if err := tx.Select("id", "inventory_account_id").Where("id = ?", slotID).First(&peek).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return service.ErrNotFound
			}
			return fmt.Errorf("load inventory slot: %w", err)
		}

		var account domain.InventoryAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", peek.InventoryAccountID).First(&account).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return service.ErrNotFound
			}
			return fmt.Errorf("load inventory account: %w", err)
		}
		if !accountSellable(account, now) {
			return service.ErrInventoryAccountUnavailable
		}

		var slot domain.InventorySlot
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", slotID).First(&slot).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return service.ErrNotFound
			}
			return fmt.Errorf("lock inventory slot: %w", err)
		}
		if slot.Status == "released" {
			return service.ErrInventorySlotUnavailable
		}

		newExpiry := service.NextSlotExpiry(slot.AccessExpiresAt, now, durationDays)
		if err := service.ValidateSlotExpiryWithinAccount(newExpiry, account.ExpiresAt); err != nil {
			return err
		}

		updates := map[string]any{"access_expires_at": newExpiry, "updated_at": now}
		// Extending an expired seat reactivates that one seat only. The other
		// slots on the account keep their own status and expiry.
		if slot.Status == service.InventoryStatusExpired {
			if slot.OrderItemID != nil {
				updates["status"] = "reserved"
			} else {
				updates["status"] = "available"
			}
		}
		if err := tx.Model(&domain.InventorySlot{}).Where("id = ?", slotID).Updates(updates).Error; err != nil {
			return fmt.Errorf("extend inventory slot: %w", err)
		}
		slot.AccessExpiresAt = &newExpiry
		slot.UpdatedAt = now
		if status, ok := updates["status"].(string); ok {
			slot.Status = status
		}
		updated = slot
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// DeactivateSlot removes a seat from sale while keeping its order history. The
// operation is idempotent and never deletes the row.
func (s *Store) DeactivateSlot(ctx context.Context, slotID string, now time.Time) (*domain.InventorySlot, error) {
	var updated domain.InventorySlot
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var slot domain.InventorySlot
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", slotID).First(&slot).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return service.ErrNotFound
			}
			return fmt.Errorf("lock inventory slot: %w", err)
		}
		if slot.Status == "released" {
			return service.ErrInventorySlotUnavailable
		}
		if slot.Status != service.InventoryStatusInactive {
			if err := tx.Model(&domain.InventorySlot{}).Where("id = ?", slotID).
				Updates(map[string]any{"status": service.InventoryStatusInactive, "updated_at": now}).Error; err != nil {
				return fmt.Errorf("deactivate inventory slot: %w", err)
			}
			slot.Status = service.InventoryStatusInactive
			slot.UpdatedAt = now
		}
		updated = slot
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}
