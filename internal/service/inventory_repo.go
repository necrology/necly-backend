package service

import (
	"context"
	"time"

	"github.com/necly/services/internal/domain"
)

type InventoryRepository interface {
	ListAccounts(ctx context.Context, page, perPage int, productID string) ([]domain.InventoryAccount, int64, error)
	GetAccount(ctx context.Context, id string) (*domain.InventoryAccount, error)
	GetAccountByProductID(ctx context.Context, productID string) (*domain.InventoryAccount, error)
	CreateAccount(ctx context.Context, a *domain.InventoryAccount) error
	UpdateAccount(ctx context.Context, a *domain.InventoryAccount) error
	DeleteAccount(ctx context.Context, id string) error
	ListSlots(ctx context.Context, accountID, productPriceID, status string, page, perPage int) ([]domain.InventorySlot, int64, error)
	GetSlot(ctx context.Context, id string) (*domain.InventorySlot, error)
	CreateSlots(ctx context.Context, slots []domain.InventorySlot) error
	UpdateSlotStatus(ctx context.Context, slotID, status string, now time.Time, reservedBy *string, reservedUntil *time.Time, orderItemID *string) error
	FindAvailableSlots(ctx context.Context, productPriceID string, quantity int, paymentDueAt time.Time, userID string, now time.Time) ([]domain.InventorySlot, error)
}
