package service

import (
	"context"
	"errors"
	"testing"

	"github.com/necly/services/internal/domain"
)

// stubInventoryRepo hanya mengisi method yang dipakai tes ini; sisanya dibiarkan
// nil lewat embed karena InventoryRepository punya banyak method.
type stubInventoryRepo struct {
	InventoryRepository
	productID string
	priceErr  error
	account   *domain.InventoryAccount
	acctErr   error
	created   []domain.InventorySlot
}

func (s *stubInventoryRepo) GetPriceProductID(context.Context, string) (string, error) {
	if s.priceErr != nil {
		return "", s.priceErr
	}
	return s.productID, nil
}

func (s *stubInventoryRepo) GetAccountByProductID(context.Context, string) (*domain.InventoryAccount, error) {
	if s.acctErr != nil {
		return nil, s.acctErr
	}
	return s.account, nil
}

func (s *stubInventoryRepo) CreateSlots(_ context.Context, slots []domain.InventorySlot) error {
	s.created = append(s.created, slots...)
	return nil
}

func TestCreateSlots_BindsEverySlotToTheProductAccountAndPrice(t *testing.T) {
	repo := &stubInventoryRepo{
		productID: "prd-1",
		account:   &domain.InventoryAccount{ID: "acc-1", ProductID: "prd-1"},
	}
	svc := NewAdminInventoryService(repo, nil)

	created, err := svc.CreateSlots(context.Background(), CreateSlotsInput{ProductPriceID: "price-1", Quantity: 10})

	if err != nil {
		t.Fatalf("CreateSlots: %v", err)
	}
	if created != 10 || len(repo.created) != 10 {
		t.Fatalf("membuat %d slot, tersimpan %d; ingin 10", created, len(repo.created))
	}
	for i, slot := range repo.created {
		if slot.InventoryAccountID != "acc-1" || slot.ProductPriceID != "price-1" {
			t.Fatalf("slot %d menunjuk akun %q harga %q", i, slot.InventoryAccountID, slot.ProductPriceID)
		}
		// Katalog menghitung ketersediaan dari baris berstatus available; status
		// lain tidak akan pernah terhitung sebagai stok yang bisa dijual.
		if slot.Status != "available" {
			t.Fatalf("slot %d berstatus %q, ingin available", i, slot.Status)
		}
	}
}

func TestCreateSlots_RejectsQuantitiesOutsideOneToHundred(t *testing.T) {
	repo := &stubInventoryRepo{productID: "prd-1"}
	svc := NewAdminInventoryService(repo, nil)

	for _, input := range []CreateSlotsInput{
		{ProductPriceID: "price-1", Quantity: 0},
		{ProductPriceID: "price-1", Quantity: -3},
		{ProductPriceID: "price-1", Quantity: 101},
		{ProductPriceID: "", Quantity: 5},
	} {
		if _, err := svc.CreateSlots(context.Background(), input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("input %+v menghasilkan %v, ingin ErrInvalidInput", input, err)
		}
	}
	if len(repo.created) != 0 {
		t.Fatalf("menyimpan %d slot untuk input yang tidak valid", len(repo.created))
	}
}

// Slot wajib menunjuk akun induk (kolomnya NOT NULL), jadi menambah slot sebelum
// akun produknya ada harus dijawab dengan petunjuk yang jelas, bukan galat server.
func TestCreateSlots_NeedsTheProductAccountFirst(t *testing.T) {
	repo := &stubInventoryRepo{
		productID: "prd-1",
		acctErr:   ErrNotFound,
	}
	svc := NewAdminInventoryService(repo, nil)

	_, err := svc.CreateSlots(context.Background(), CreateSlotsInput{ProductPriceID: "price-1", Quantity: 10})

	if !errors.Is(err, ErrAccountRequired) {
		t.Fatalf("galat %v, ingin ErrAccountRequired", err)
	}
	if len(repo.created) != 0 {
		t.Fatalf("menyimpan %d slot padahal akunnya belum ada", len(repo.created))
	}
}
