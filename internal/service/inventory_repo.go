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

	// ExpireInventory performs one bounded, idempotent transition batch. Slots
	// whose own access window ended become expired; active accounts whose
	// provider window ended become expired. Slots of an expired account are
	// deliberately left untouched so a single lapsed user never rewrites the
	// access window of the other users on the same account.
	ExpireInventory(ctx context.Context, now time.Time, limit int) (InventoryExpiryResult, error)
	// Reminder queries return rows inside the H-N window. Deduplication happens
	// at insert time through notifications.dedupe_key.
	ListSlotExpiryReminders(ctx context.Context, now time.Time, windowDays int) ([]InventoryExpiryReminder, error)
	ListAccountExpiryReminders(ctx context.Context, now time.Time, windowDays int) ([]InventoryExpiryReminder, error)
	ListActiveAdminIDs(ctx context.Context) ([]string, error)
	CreateNotifications(ctx context.Context, notifications []domain.Notification) (int, error)
	// ExtendSlotAccess applies max(now, old expiry) + duration under row locks and
	// rejects a result that would outlive the parent account.
	ExtendSlotAccess(ctx context.Context, slotID string, now time.Time, durationDays int) (*domain.InventorySlot, error)
	// DeactivateSlot removes a seat from sale without deleting its order history.
	DeactivateSlot(ctx context.Context, slotID string, now time.Time) (*domain.InventorySlot, error)
}
