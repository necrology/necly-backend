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

// The service layer owns the inventory contract. This assertion keeps the
// GORM store honest: if the interface grows, the build fails here instead of at
// the wiring site in cmd/server.
var _ service.InventoryRepository = (*Store)(nil)

func (s *Store) ListAccounts(ctx context.Context, page, perPage int, productID, status string) ([]domain.InventoryAccount, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	query := s.db.WithContext(ctx).Model(&domain.InventoryAccount{}).Preload("Product")
	if productID != "" {
		// product_id is uuid; a malformed filter would otherwise surface as a
		// PostgreSQL 22P02 and a 500. An impossible id matches nothing.
		if _, err := uuid.Parse(productID); err != nil {
			return []domain.InventoryAccount{}, 0, nil
		}
		query = query.Where("product_id = ?", productID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count inventory accounts: %w", err)
	}
	var accounts []domain.InventoryAccount
	offset := (page - 1) * perPage
	if err := query.Order("created_at DESC, id DESC").Offset(offset).Limit(perPage).Find(&accounts).Error; err != nil {
		return nil, 0, fmt.Errorf("list inventory accounts: %w", err)
	}
	if err := s.attachSlotCounts(ctx, accounts); err != nil {
		return nil, 0, err
	}
	// Never return encrypted credentials or the encrypted OTP seed.
	for i := range accounts {
		accounts[i].EncryptedCredentials = nil
		accounts[i].OTPSecretEncrypted = nil
	}
	return accounts, total, nil
}

// attachSlotCounts fills the derived capacity fields in one grouped query so
// the admin list does not issue one COUNT per account. assigned_users follows
// the same ownership rule as the slot detail: an order's buyer, or the user who
// reserved the seat when the order row is not reachable.
func (s *Store) attachSlotCounts(ctx context.Context, accounts []domain.InventoryAccount) error {
	if len(accounts) == 0 {
		return nil
	}
	ids := make([]string, 0, len(accounts))
	for i := range accounts {
		ids = append(ids, accounts[i].ID)
	}
	type slotCountRow struct {
		InventoryAccountID string
		Total              int64
		Available          int64
		Assigned           int64
	}
	var rows []slotCountRow
	if err := s.db.WithContext(ctx).Table("inventory_slots AS s").
		Select(`s.inventory_account_id,
			COUNT(*) AS total,
			COUNT(*) FILTER (WHERE s.status = 'available') AS available,
			COUNT(DISTINCT COALESCE(o.user_id, s.reserved_by))
				FILTER (WHERE COALESCE(o.user_id, s.reserved_by) IS NOT NULL) AS assigned`).
		Joins("LEFT JOIN order_items oi ON oi.id = s.order_item_id").
		Joins("LEFT JOIN orders o ON o.id = oi.order_id").
		Where("s.inventory_account_id IN ?", ids).
		Group("s.inventory_account_id").
		Scan(&rows).Error; err != nil {
		return fmt.Errorf("count inventory slots: %w", err)
	}
	counts := make(map[string]slotCountRow, len(rows))
	for _, row := range rows {
		counts[row.InventoryAccountID] = row
	}
	now := s.nowUTC()
	for i := range accounts {
		count := counts[accounts[i].ID]
		applyAccountDerived(&accounts[i], count.Total, count.Available, count.Assigned, now)
	}
	return nil
}

// applyAccountDerived fills the read-only capacity fields the admin API
// exposes. They are never stored (gorm:"-"), so every response path must
// recompute them from the slot rows it just read.
func applyAccountDerived(account *domain.InventoryAccount, total, available, assigned int64, now time.Time) {
	account.TotalSlots = int(total)
	account.AvailableSlots = int(available)
	account.UsedSlots = account.TotalSlots - account.AvailableSlots
	account.AssignedUsers = int(assigned)
	if account.SharingLimit > account.TotalSlots {
		account.RemainingSlots = account.SharingLimit - account.TotalSlots
	} else {
		account.RemainingSlots = 0
	}
	account.Sellable = account.Status == "active" && (account.ExpiresAt == nil || account.ExpiresAt.After(now))
	account.NeedsReview = account.SharingLimit > service.MaxSharingLimit || account.TotalSlots > service.MaxSharingLimit
	account.OTPConfigured = len(account.OTPSecretEncrypted) > 0
	account.DaysRemaining = domain.DaysRemaining(account.ExpiresAt, now)
}

// hydrateAccount counts the slots owned by a single account and fills its
// derived fields. It keeps the single-account read paths (GET/POST/PATCH) from
// returning zero counters while the list endpoint reports the real ones.
func (s *Store) hydrateAccount(ctx context.Context, account *domain.InventoryAccount) error {
	items := []domain.InventoryAccount{*account}
	if err := s.attachSlotCounts(ctx, items); err != nil {
		return err
	}
	*account = items[0]
	return nil
}

func (s *Store) GetAccount(ctx context.Context, id string) (*domain.InventoryAccount, error) {
	var a domain.InventoryAccount
	err := s.db.WithContext(ctx).Preload("Product").Where("id = ?", id).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get inventory account: %w", err)
	}
	a.EncryptedCredentials = nil
	if err := s.hydrateAccount(ctx, &a); err != nil {
		return nil, err
	}
	a.OTPSecretEncrypted = nil
	return &a, nil
}

// GetAccountWithCredentials returns the stored ciphertext. It exists only for
// the update path, which must preserve credentials the request did not replace.
// Every other read strips the ciphertext before returning.
func (s *Store) GetAccountWithCredentials(ctx context.Context, id string) (*domain.InventoryAccount, error) {
	var a domain.InventoryAccount
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get inventory account: %w", err)
	}
	return &a, nil
}

func (s *Store) GetAccountByProductID(ctx context.Context, productID string) (*domain.InventoryAccount, error) {
	var a domain.InventoryAccount
	err := s.db.WithContext(ctx).Where("product_id = ?", productID).Order("created_at ASC, id ASC").First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get inventory account by product: %w", err)
	}
	a.EncryptedCredentials = nil
	if err := s.hydrateAccount(ctx, &a); err != nil {
		return nil, err
	}
	a.OTPSecretEncrypted = nil
	return &a, nil
}

func (s *Store) CreateAccount(ctx context.Context, a *domain.InventoryAccount) error {
	a.ID = uuid.NewString()
	a.CreatedAt = s.nowUTC()
	a.UpdatedAt = a.CreatedAt
	if err := s.db.WithContext(ctx).Create(a).Error; err != nil {
		return err
	}
	// A brand-new account owns no slots yet, so the derived counters are known
	// without another query while still matching the list endpoint shape.
	applyAccountDerived(a, 0, 0, 0, a.CreatedAt)
	return nil
}

// UpdateAccount saves the account under a row lock and re-checks sharing_limit
// against the provisioned slot rows in the same transaction. Without the lock a
// concurrent CreateSlotsForAccount/CreateSlotsAuto (which lock the account row)
// could add slots between the service-layer count and this write, leaving the
// account over its new limit.
func (s *Store) UpdateAccount(ctx context.Context, a *domain.InventoryAccount) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current domain.InventoryAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", a.ID).First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return service.ErrNotFound
			}
			return fmt.Errorf("load inventory account: %w", err)
		}

		var total, available, assigned int64
		if err := tx.Model(&domain.InventorySlot{}).
			Where("inventory_account_id = ?", a.ID).Count(&total).Error; err != nil {
			return fmt.Errorf("count inventory slots: %w", err)
		}
		if err := tx.Model(&domain.InventorySlot{}).
			Where("inventory_account_id = ? AND status = ?", a.ID, "available").Count(&available).Error; err != nil {
			return fmt.Errorf("count available inventory slots: %w", err)
		}
		if err := tx.Raw(`SELECT COUNT(DISTINCT COALESCE(o.user_id, s.reserved_by))
			FROM inventory_slots s
			LEFT JOIN order_items oi ON oi.id = s.order_item_id
			LEFT JOIN orders o ON o.id = oi.order_id
			WHERE s.inventory_account_id = ? AND COALESCE(o.user_id, s.reserved_by) IS NOT NULL`, a.ID).
			Scan(&assigned).Error; err != nil {
			return fmt.Errorf("count assigned inventory users: %w", err)
		}
		if int64(a.SharingLimit) < total {
			return service.ErrSharingLimitExceeded
		}

		a.UpdatedAt = s.nowUTC()
		if err := tx.Save(a).Error; err != nil {
			return err
		}
		applyAccountDerived(a, total, available, assigned, a.UpdatedAt)
		return nil
	})
}

// ProductExists keeps a stale or fabricated product id from surfacing as a
// foreign-key violation (and a 500) when an inventory account is created.
func (s *Store) ProductExists(ctx context.Context, productID string) (bool, error) {
	var count int64
	if err := s.db.WithContext(ctx).Model(&domain.Product{}).Where("id = ?", productID).Count(&count).Error; err != nil {
		return false, fmt.Errorf("check inventory product: %w", err)
	}
	return count > 0, nil
}

func (s *Store) CountSlotsForAccount(ctx context.Context, accountID string) (int64, error) {
	var count int64
	if err := s.db.WithContext(ctx).Model(&domain.InventorySlot{}).
		Where("inventory_account_id = ?", accountID).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count inventory slots: %w", err)
	}
	return count, nil
}

func (s *Store) DeleteAccount(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var account domain.InventoryAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&account).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return service.ErrNotFound
			}
			return fmt.Errorf("load inventory account: %w", err)
		}

		// Lock every slot row before deciding whether the account is safe to
		// delete. Without this, a concurrent checkout could reserve a slot
		// between an unlocked count and the DELETE; the cascade would then
		// remove the freshly reserved slot. Holding slot locks makes checkout's
		// FOR UPDATE SKIP LOCKED skip them, and a checkout that already holds a
		// slot forces this transaction to wait and observe its committed state.
		var slots []domain.InventorySlot
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "inventory_account_id", "status", "reserved_by", "order_item_id").
			Where("inventory_account_id = ?", id).
			Order("id").
			Find(&slots).Error; err != nil {
			return fmt.Errorf("lock inventory slots: %w", err)
		}
		for _, slot := range slots {
			if slot.Status != "available" || slot.OrderItemID != nil || slot.ReservedBy != nil {
				// The schema still has ON DELETE CASCADE for historical rows, so
				// the guard must run inside the same transaction as the delete. A
				// 409 here leaves the account and every slot untouched.
				return service.ErrInventoryAccountInUse
			}
		}

		if err := tx.Delete(&domain.InventoryAccount{}, "id = ?", id).Error; err != nil {
			return fmt.Errorf("delete inventory account: %w", err)
		}
		return nil
	})
}

// ListSlotAssignments returns the read-only slot view for one account, joined
// to the owning order and customer. Credentials are never part of this shape.
func (s *Store) ListSlotAssignments(ctx context.Context, accountID, productPriceID, status string, page, perPage int) ([]domain.InventorySlotAssignment, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 50
	}
	query := s.db.WithContext(ctx).Table("inventory_slots AS s").
		Joins("LEFT JOIN order_items oi ON oi.id = s.order_item_id").
		Joins("LEFT JOIN orders o ON o.id = oi.order_id").
		Joins("LEFT JOIN users u ON u.id = COALESCE(o.user_id, s.reserved_by)").
		Where("s.inventory_account_id = ?", accountID)
	if productPriceID != "" {
		if _, err := uuid.Parse(productPriceID); err != nil {
			return []domain.InventorySlotAssignment{}, 0, nil
		}
		query = query.Where("s.product_price_id = ?", productPriceID)
	}
	if status != "" {
		query = query.Where("s.status = ?", status)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count inventory slots: %w", err)
	}
	var assignments []domain.InventorySlotAssignment
	offset := (page - 1) * perPage
	if err := query.
		Select(`s.id, s.inventory_account_id, s.product_price_id, s.status,
			s.reserved_by, s.reserved_until, s.order_item_id,
			s.activated_at, s.access_expires_at,
			o.id AS order_id, o.number AS order_number,
			(SELECT p.status FROM payments p WHERE p.order_id = o.id
				ORDER BY p.created_at DESC, p.id DESC LIMIT 1) AS payment_status,
			u.email AS reserved_email, u.name AS reserved_name,
			s.created_at, s.updated_at`).
		Order("s.created_at DESC, s.id DESC").
		Offset(offset).Limit(perPage).
		Scan(&assignments).Error; err != nil {
		return nil, 0, fmt.Errorf("list inventory slots: %w", err)
	}
	now := s.nowUTC()
	for i := range assignments {
		assignments[i].DaysRemaining = domain.DaysRemaining(assignments[i].AccessExpiresAt, now)
	}
	return assignments, total, nil
}

func (s *Store) GetSlot(ctx context.Context, id string) (*domain.InventorySlot, error) {
	var slot domain.InventorySlot
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&slot).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	return &slot, err
}

// GetPriceProductID melacak produk pemilik sebuah harga paket. Galatnya memakai
// kosakata service seperti method lain di berkas ini, supaya handler dapat
// menjawab 404 alih-alih 500 untuk paket yang tidak ada.
func (s *Store) GetPriceProductID(ctx context.Context, priceID string) (string, error) {
	// product_prices.id bertipe uuid; id yang bukan uuid tidak mungkin cocok dan
	// hanya akan membuat Postgres melempar SQLSTATE 22P02.
	if _, err := uuid.Parse(priceID); err != nil {
		return "", service.ErrNotFound
	}

	var price domain.ProductPrice
	err := s.db.WithContext(ctx).Select("product_id").Where("id = ?", priceID).First(&price).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", service.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get price product: %w", err)
	}
	return price.ProductID, nil
}

// CreateSlotsForAccount adds capacity to one explicit account. The account row
// is locked for the whole transaction so two concurrent requests cannot both
// observe spare capacity and overshoot sharing_limit.
func (s *Store) CreateSlotsForAccount(ctx context.Context, accountID, productID string, slots []domain.InventorySlot) error {
	if len(slots) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := s.nowUTC()
		var account domain.InventoryAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", accountID).First(&account).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return service.ErrNotFound
			}
			return fmt.Errorf("load inventory account: %w", err)
		}
		if account.ProductID != productID {
			return service.ErrInventoryAccountMismatch
		}
		if !accountSellable(account, now) {
			return service.ErrInventoryAccountUnavailable
		}
		if account.SharingLimit > service.MaxSharingLimit {
			// A legacy account above the five-user rule keeps every existing
			// slot, but adding more would deepen the violation. The admin must
			// lower the limit first (or split capacity into another account).
			return service.ErrInventoryAccountNeedsReview
		}
		var count int64
		if err := tx.Model(&domain.InventorySlot{}).
			Where("inventory_account_id = ?", accountID).Count(&count).Error; err != nil {
			return fmt.Errorf("count inventory slots: %w", err)
		}
		if int(count)+len(slots) > account.SharingLimit {
			return service.ErrSharingLimitExceeded
		}
		return insertSlots(tx, accountID, slots, now)
	})
}

// CreateSlotsAuto picks the oldest active, non-expired account with spare
// capacity and adds the slots there. It returns the chosen account id so the
// caller can report where capacity landed. All candidate accounts are locked in
// a deterministic order, so concurrent requests serialize instead of deadlock.
func (s *Store) CreateSlotsAuto(ctx context.Context, productID string, slots []domain.InventorySlot) (string, error) {
	if len(slots) == 0 {
		return "", nil
	}
	chosen := ""
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := s.nowUTC()
		var accounts []domain.InventoryAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("product_id = ?", productID).
			Where("status = ?", "active").
			Where("expires_at IS NULL OR expires_at > ?", now).
			Order("created_at ASC, id ASC").
			Find(&accounts).Error; err != nil {
			return fmt.Errorf("load inventory accounts: %w", err)
		}
		if len(accounts) == 0 {
			return service.ErrAccountRequired
		}
		sawNeedsReview := false
		for _, account := range accounts {
			if account.SharingLimit > service.MaxSharingLimit {
				sawNeedsReview = true
				continue
			}
			var count int64
			if err := tx.Model(&domain.InventorySlot{}).
				Where("inventory_account_id = ?", account.ID).Count(&count).Error; err != nil {
				return fmt.Errorf("count inventory slots: %w", err)
			}
			if int(count)+len(slots) <= account.SharingLimit {
				chosen = account.ID
				return insertSlots(tx, account.ID, slots, now)
			}
		}
		if sawNeedsReview {
			return service.ErrInventoryAccountNeedsReview
		}
		return service.ErrSharingLimitExceeded
	})
	if err != nil {
		return "", err
	}
	return chosen, nil
}

func insertSlots(tx *gorm.DB, accountID string, slots []domain.InventorySlot, now time.Time) error {
	for i := range slots {
		slots[i].ID = uuid.NewString()
		slots[i].InventoryAccountID = accountID
		slots[i].CreatedAt = now
		slots[i].UpdatedAt = now
		if slots[i].Status == "" {
			slots[i].Status = "available"
		}
	}
	if err := tx.CreateInBatches(slots, 100).Error; err != nil {
		return fmt.Errorf("create inventory slots: %w", err)
	}
	return nil
}

func accountSellable(account domain.InventoryAccount, now time.Time) bool {
	if account.Status != "active" {
		return false
	}
	return account.ExpiresAt == nil || account.ExpiresAt.After(now)
}

func (s *Store) UpdateSlotStatus(ctx context.Context, slotID, status string, now time.Time, reservedBy *string, reservedUntil *time.Time, orderItemID *string) error {
	updates := map[string]any{"status": status, "updated_at": now}
	if reservedBy != nil {
		updates["reserved_by"] = *reservedBy
	}
	if reservedUntil != nil {
		updates["reserved_until"] = *reservedUntil
	}
	if orderItemID != nil {
		updates["order_item_id"] = *orderItemID
	}
	result := s.db.WithContext(ctx).Model(&domain.InventorySlot{}).
		Where("id = ?", slotID).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("update slot status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return service.ErrNotFound
	}
	return nil
}

func (s *Store) FindAvailableSlots(ctx context.Context, productPriceID string, quantity int, paymentDueAt time.Time, userID string, now time.Time) ([]domain.InventorySlot, error) {
	var slots []domain.InventorySlot
	err := s.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
		Where("product_price_id = ? AND status = ?", productPriceID, "available").
		Limit(quantity).
		Find(&slots).Error
	if err != nil {
		return nil, fmt.Errorf("find available slots: %w", err)
	}
	if len(slots) < quantity {
		return nil, service.ErrOutOfStock
	}
	return slots, nil
}
