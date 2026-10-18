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

type InventoryRepository interface {
	ListAccounts(ctx context.Context, page, perPage int, productID string) ([]domain.InventoryAccount, int64, error)
	GetAccount(ctx context.Context, id string) (*domain.InventoryAccount, error)
	GetAccountByProductID(ctx context.Context, productID string) (*domain.InventoryAccount, error)
	CreateAccount(ctx context.Context, a *domain.InventoryAccount) error
	UpdateAccount(ctx context.Context, a *domain.InventoryAccount) error
	DeleteAccount(ctx context.Context, id string) error
	GetPriceProductID(ctx context.Context, priceID string) (string, error)
	ListSlots(ctx context.Context, accountID, productPriceID, status string, page, perPage int) ([]domain.InventorySlot, int64, error)
	GetSlot(ctx context.Context, id string) (*domain.InventorySlot, error)
	CreateSlots(ctx context.Context, slots []domain.InventorySlot) error
	UpdateSlotStatus(ctx context.Context, slotID, status string, now time.Time, reservedBy *string, reservedUntil *time.Time, orderItemID *string) error
	FindAvailableSlots(ctx context.Context, productPriceID string, quantity int, paymentDueAt time.Time, userID string, now time.Time) ([]domain.InventorySlot, error)
}

func (s *Store) ListAccounts(ctx context.Context, page, perPage int, productID string) ([]domain.InventoryAccount, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	query := s.db.WithContext(ctx).Model(&domain.InventoryAccount{}).Preload("Product")
	if productID != "" {
		query = query.Where("product_id = ?", productID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count inventory accounts: %w", err)
	}
	var accounts []domain.InventoryAccount
	offset := (page - 1) * perPage
	if err := query.Order("created_at DESC").Offset(offset).Limit(perPage).Find(&accounts).Error; err != nil {
		return nil, 0, fmt.Errorf("list inventory accounts: %w", err)
	}
	// Never return encrypted credentials
	for i := range accounts {
		accounts[i].EncryptedCredentials = nil
	}
	return accounts, total, nil
}

func (s *Store) GetAccount(ctx context.Context, id string) (*domain.InventoryAccount, error) {
	var a domain.InventoryAccount
	err := s.db.WithContext(ctx).Preload("Product").Where("id = ?", id).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	a.EncryptedCredentials = nil
	return &a, err
}

func (s *Store) GetAccountByProductID(ctx context.Context, productID string) (*domain.InventoryAccount, error) {
	var a domain.InventoryAccount
	err := s.db.WithContext(ctx).Where("product_id = ?", productID).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	a.EncryptedCredentials = nil
	return &a, err
}

func (s *Store) CreateAccount(ctx context.Context, a *domain.InventoryAccount) error {
	a.ID = uuid.NewString()
	a.CreatedAt = s.nowUTC()
	a.UpdatedAt = a.CreatedAt
	return s.db.WithContext(ctx).Create(a).Error
}

func (s *Store) UpdateAccount(ctx context.Context, a *domain.InventoryAccount) error {
	a.UpdatedAt = s.nowUTC()
	return s.db.WithContext(ctx).Where("id = ?", a.ID).Save(a).Error
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

func (s *Store) ListSlots(ctx context.Context, accountID, productPriceID, status string, page, perPage int) ([]domain.InventorySlot, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 50
	}
	query := s.db.WithContext(ctx).Model(&domain.InventorySlot{}).Preload("InventoryAccount")
	if accountID != "" {
		query = query.Where("inventory_account_id = ?", accountID)
	}
	if productPriceID != "" {
		query = query.Where("product_price_id = ?", productPriceID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count inventory slots: %w", err)
	}
	var slots []domain.InventorySlot
	offset := (page - 1) * perPage
	if err := query.Order("created_at DESC").Offset(offset).Limit(perPage).Find(&slots).Error; err != nil {
		return nil, 0, fmt.Errorf("list inventory slots: %w", err)
	}
	return slots, total, nil
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

func (s *Store) CreateSlots(ctx context.Context, slots []domain.InventorySlot) error {
	if len(slots) == 0 {
		return nil
	}
	now := s.nowUTC()
	for i := range slots {
		slots[i].ID = uuid.NewString()
		slots[i].CreatedAt = now
		slots[i].UpdatedAt = now
	}
	return s.db.WithContext(ctx).CreateInBatches(slots, 100).Error
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
