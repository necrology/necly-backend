package service

import (
	"context"
	"time"

	"github.com/necly/services/internal/domain"
)

// InventoryRepository is the persistence contract for admin inventory
// management. Credential-bearing reads are explicit: GetAccount and
// GetAccountByProductID strip the ciphertext, while GetAccountWithCredentials is
// only for the update path that must preserve or replace it.
type InventoryRepository interface {
	ListAccounts(ctx context.Context, page, perPage int, productID, status string) ([]domain.InventoryAccount, int64, error)
	GetAccount(ctx context.Context, id string) (*domain.InventoryAccount, error)
	GetAccountWithCredentials(ctx context.Context, id string) (*domain.InventoryAccount, error)
	GetAccountByProductID(ctx context.Context, productID string) (*domain.InventoryAccount, error)
	CreateAccount(ctx context.Context, a *domain.InventoryAccount) error
	UpdateAccount(ctx context.Context, a *domain.InventoryAccount) error
	DeleteAccount(ctx context.Context, id string) error
	// ProductExists keeps a malformed or stale product id from surfacing as a
	// foreign-key 500 when an account is created.
	ProductExists(ctx context.Context, productID string) (bool, error)
	CountSlotsForAccount(ctx context.Context, accountID string) (int64, error)
	// Produk pemilik sebuah harga. Slot dicatat per harga sedangkan akun
	// induknya per produk, jadi pemetaan ini dibutuhkan sebelum slot dibuat.
	GetPriceProductID(ctx context.Context, priceID string) (string, error)
	ListSlotAssignments(ctx context.Context, accountID, productPriceID, status string, page, perPage int) ([]domain.InventorySlotAssignment, int64, error)
	GetSlot(ctx context.Context, id string) (*domain.InventorySlot, error)
	// CreateSlotsForAccount re-checks ownership, availability and sharing_limit
	// under a row lock before inserting.
	CreateSlotsForAccount(ctx context.Context, accountID, productID string, slots []domain.InventorySlot) error
	// CreateSlotsAuto picks an eligible active account with spare capacity and
	// inserts the slots under the same transaction. It returns the chosen
	// account id so the caller can report where capacity was added.
	CreateSlotsAuto(ctx context.Context, productID string, slots []domain.InventorySlot) (string, error)
	UpdateSlotStatus(ctx context.Context, slotID, status string, now time.Time, reservedBy *string, reservedUntil *time.Time, orderItemID *string) error
	FindAvailableSlots(ctx context.Context, productPriceID string, quantity int, paymentDueAt time.Time, userID string, now time.Time) ([]domain.InventorySlot, error)
}
