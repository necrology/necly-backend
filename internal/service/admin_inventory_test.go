package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
)

// stubInventoryRepo hanya mengisi method yang dipakai tes ini; sisanya dibiarkan
// nil lewat embed karena InventoryRepository punya banyak method.
type stubInventoryRepo struct {
	InventoryRepository

	productID string
	priceErr  error

	productExists bool
	productErr    error

	accountWithCredentials *domain.InventoryAccount
	getCredentialsErr      error

	slotCount int64
	countErr  error

	account    *domain.InventoryAccount
	accountErr error

	createdAccount *domain.InventoryAccount
	createErr      error

	updatedAccount *domain.InventoryAccount
	updateErr      error

	explicitAccountID string
	explicitProductID string
	explicitSlots     []domain.InventorySlot
	explicitErr       error

	autoProductID string
	autoSlots     []domain.InventorySlot
	autoAccountID string
	autoErr       error

	assignments []domain.InventorySlotAssignment
	assignTotal int64
	assignErr   error
}

func (s *stubInventoryRepo) GetPriceProductID(context.Context, string) (string, error) {
	if s.priceErr != nil {
		return "", s.priceErr
	}
	return s.productID, nil
}

func (s *stubInventoryRepo) ProductExists(context.Context, string) (bool, error) {
	if s.productErr != nil {
		return false, s.productErr
	}
	return s.productExists, nil
}

func (s *stubInventoryRepo) CreateAccount(_ context.Context, a *domain.InventoryAccount) error {
	if s.createErr != nil {
		return s.createErr
	}
	// Salin nilai: service menihilkan ciphertext pada struct yang sama sebelum
	// mengembalikannya, jadi menyimpan pointer akan kehilangan buktinya.
	copied := *a
	s.createdAccount = &copied
	return nil
}

func (s *stubInventoryRepo) GetAccountWithCredentials(context.Context, string) (*domain.InventoryAccount, error) {
	if s.getCredentialsErr != nil {
		return nil, s.getCredentialsErr
	}
	return s.accountWithCredentials, nil
}

func (s *stubInventoryRepo) CountSlotsForAccount(context.Context, string) (int64, error) {
	if s.countErr != nil {
		return 0, s.countErr
	}
	return s.slotCount, nil
}

func (s *stubInventoryRepo) UpdateAccount(_ context.Context, a *domain.InventoryAccount) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	s.updatedAccount = a
	return nil
}

func (s *stubInventoryRepo) GetAccount(context.Context, string) (*domain.InventoryAccount, error) {
	if s.accountErr != nil {
		return nil, s.accountErr
	}
	return s.account, nil
}

func (s *stubInventoryRepo) CreateSlotsForAccount(_ context.Context, accountID, productID string, slots []domain.InventorySlot) error {
	if s.explicitErr != nil {
		return s.explicitErr
	}
	s.explicitAccountID = accountID
	s.explicitProductID = productID
	s.explicitSlots = append(s.explicitSlots, slots...)
	return nil
}

func (s *stubInventoryRepo) CreateSlotsAuto(_ context.Context, productID string, slots []domain.InventorySlot) (string, error) {
	if s.autoErr != nil {
		return "", s.autoErr
	}
	s.autoProductID = productID
	s.autoSlots = append(s.autoSlots, slots...)
	return s.autoAccountID, nil
}

func (s *stubInventoryRepo) ListSlotAssignments(context.Context, string, string, string, int, int) ([]domain.InventorySlotAssignment, int64, error) {
	if s.assignErr != nil {
		return nil, 0, s.assignErr
	}
	return s.assignments, s.assignTotal, nil
}

func testCipher(t *testing.T) *CredentialCipher {
	t.Helper()
	cipher, err := NewCredentialCipher("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatalf("credential cipher: %v", err)
	}
	return cipher
}

func TestCreateSlots_ExplicitAccountBindsEverySlotToTheChosenAccountAndPrice(t *testing.T) {
	accountID := uuid.NewString()
	repo := &stubInventoryRepo{productID: "prd-1"}
	svc := NewAdminInventoryService(repo, nil)

	result, err := svc.CreateSlots(context.Background(), CreateSlotsInput{
		ProductPriceID:     "price-1",
		InventoryAccountID: accountID,
		Quantity:           10,
	})

	if err != nil {
		t.Fatalf("CreateSlots: %v", err)
	}
	if result.Created != 10 || result.InventoryAccountID != accountID {
		t.Fatalf("result = %+v, want created=10 account=%s", result, accountID)
	}
	if repo.explicitProductID != "prd-1" {
		t.Fatalf("product id = %q, want prd-1", repo.explicitProductID)
	}
	if len(repo.explicitSlots) != 10 {
		t.Fatalf("membuat %d slot; ingin 10", len(repo.explicitSlots))
	}
	for i, slot := range repo.explicitSlots {
		if slot.ProductPriceID != "price-1" {
			t.Fatalf("slot %d menunjuk harga %q", i, slot.ProductPriceID)
		}
		// Katalog menghitung ketersediaan dari baris berstatus available; status
		// lain tidak akan pernah terhitung sebagai stok yang bisa dijual.
		if slot.Status != "available" {
			t.Fatalf("slot %d berstatus %q, ingin available", i, slot.Status)
		}
	}
}

func TestCreateSlots_AutoSelectsAnAccountAndReturnsItsID(t *testing.T) {
	repo := &stubInventoryRepo{productID: "prd-1", autoAccountID: "acc-auto"}
	svc := NewAdminInventoryService(repo, nil)

	result, err := svc.CreateSlots(context.Background(), CreateSlotsInput{ProductPriceID: "price-1", Quantity: 3})

	if err != nil {
		t.Fatalf("CreateSlots: %v", err)
	}
	if result.Created != 3 || result.InventoryAccountID != "acc-auto" {
		t.Fatalf("result = %+v, want created=3 account=acc-auto", result)
	}
	if len(repo.autoSlots) != 3 {
		t.Fatalf("menyimpan %d slot, ingin 3", len(repo.autoSlots))
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
	if len(repo.explicitSlots) != 0 || len(repo.autoSlots) != 0 {
		t.Fatalf("menyimpan slot untuk input yang tidak valid")
	}
}

func TestCreateSlots_ExplicitMalformedAccountIsNotFound(t *testing.T) {
	repo := &stubInventoryRepo{productID: "prd-1"}
	svc := NewAdminInventoryService(repo, nil)

	if _, err := svc.CreateSlots(context.Background(), CreateSlotsInput{
		ProductPriceID:     "price-1",
		InventoryAccountID: "not-a-uuid",
		Quantity:           1,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("galat %v, ingin ErrNotFound", err)
	}
}

// Slot wajib menunjuk akun induk, jadi menambah slot tanpa akun aktif harus
// dijawab dengan petunjuk yang jelas, bukan galat server.
func TestCreateSlots_AutoNeedsAnAccount(t *testing.T) {
	repo := &stubInventoryRepo{productID: "prd-1", autoErr: ErrAccountRequired}
	svc := NewAdminInventoryService(repo, nil)

	_, err := svc.CreateSlots(context.Background(), CreateSlotsInput{ProductPriceID: "price-1", Quantity: 10})

	if !errors.Is(err, ErrAccountRequired) {
		t.Fatalf("galat %v, ingin ErrAccountRequired", err)
	}
}

func TestCreateSlots_AutoPropagatesSharingLimitExceeded(t *testing.T) {
	repo := &stubInventoryRepo{productID: "prd-1", autoErr: ErrSharingLimitExceeded}
	svc := NewAdminInventoryService(repo, nil)

	_, err := svc.CreateSlots(context.Background(), CreateSlotsInput{ProductPriceID: "price-1", Quantity: 10})

	if !errors.Is(err, ErrSharingLimitExceeded) {
		t.Fatalf("galat %v, ingin ErrSharingLimitExceeded", err)
	}
}

func TestCreateInventoryAccount_DefaultsLabelAndSharingLimit(t *testing.T) {
	productID := uuid.NewString()
	repo := &stubInventoryRepo{productExists: true}
	svc := NewAdminInventoryService(repo, testCipher(t))

	account, err := svc.CreateInventoryAccount(context.Background(), CreateInventoryAccountInput{
		ProductID:   productID,
		Credentials: "user:pass",
	})

	if err != nil {
		t.Fatalf("CreateInventoryAccount: %v", err)
	}
	if account.Label != "Akun inventaris" {
		t.Fatalf("label = %q, ingin default", account.Label)
	}
	if account.SharingLimit != DefaultSharingLimit {
		t.Fatalf("sharing_limit = %d, ingin %d", account.SharingLimit, DefaultSharingLimit)
	}
	if account.Status != InventoryStatusActive {
		t.Fatalf("status = %q, ingin active", account.Status)
	}
	// Kredensial tidak boleh kembali ke pemanggil.
	if account.EncryptedCredentials != nil {
		t.Fatal("EncryptedCredentials ikut terisi pada hasil create")
	}
	if repo.createdAccount == nil || len(repo.createdAccount.EncryptedCredentials) == 0 {
		t.Fatal("kredensial tidak dienkripsi sebelum disimpan")
	}
}

func TestCreateInventoryAccount_UnknownProductIsNotFound(t *testing.T) {
	repo := &stubInventoryRepo{productExists: false}
	svc := NewAdminInventoryService(repo, testCipher(t))

	_, err := svc.CreateInventoryAccount(context.Background(), CreateInventoryAccountInput{
		ProductID:   uuid.NewString(),
		Credentials: "user:pass",
	})

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("galat %v, ingin ErrNotFound", err)
	}
}

func TestCreateInventoryAccount_RejectsOverlongLabel(t *testing.T) {
	repo := &stubInventoryRepo{productExists: true}
	svc := NewAdminInventoryService(repo, testCipher(t))

	_, err := svc.CreateInventoryAccount(context.Background(), CreateInventoryAccountInput{
		ProductID:   uuid.NewString(),
		Credentials: "user:pass",
		Label:       string(make([]byte, MaxInventoryLabelLength+1)),
	})

	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("galat %v, ingin ErrInvalidInput", err)
	}
}

func TestUpdateInventoryAccount_RejectsLimitBelowProvisionedSlots(t *testing.T) {
	id := uuid.NewString()
	repo := &stubInventoryRepo{
		accountWithCredentials: &domain.InventoryAccount{ID: id, SharingLimit: 5},
		slotCount:              5,
	}
	svc := NewAdminInventoryService(repo, testCipher(t))

	limit := 3
	_, err := svc.UpdateInventoryAccount(context.Background(), id, UpdateInventoryAccountInput{SharingLimit: &limit})

	if !errors.Is(err, ErrSharingLimitExceeded) {
		t.Fatalf("galat %v, ingin ErrSharingLimitExceeded", err)
	}
	if repo.updatedAccount != nil {
		t.Fatal("akun tersimpan padahal limit di bawah jumlah slot")
	}
}

func TestUpdateInventoryAccount_AcceptsLimitAtOrAboveProvisionedSlots(t *testing.T) {
	id := uuid.NewString()
	repo := &stubInventoryRepo{
		accountWithCredentials: &domain.InventoryAccount{ID: id, SharingLimit: 2},
		slotCount:              2,
	}
	svc := NewAdminInventoryService(repo, testCipher(t))

	limit := 4
	account, err := svc.UpdateInventoryAccount(context.Background(), id, UpdateInventoryAccountInput{SharingLimit: &limit})

	if err != nil {
		t.Fatalf("UpdateInventoryAccount: %v", err)
	}
	if account.SharingLimit != 4 {
		t.Fatalf("sharing_limit = %d, ingin 4", account.SharingLimit)
	}
	if account.EncryptedCredentials != nil {
		t.Fatal("EncryptedCredentials ikut terisi pada hasil update")
	}
}

func TestUpdateInventoryAccount_RejectsInvalidStatusAndEmptyLabel(t *testing.T) {
	id := uuid.NewString()
	svc := NewAdminInventoryService(&stubInventoryRepo{
		accountWithCredentials: &domain.InventoryAccount{ID: id, SharingLimit: 1},
	}, testCipher(t))

	badStatus := "paused"
	if _, err := svc.UpdateInventoryAccount(context.Background(), id, UpdateInventoryAccountInput{Status: &badStatus}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("status tidak valid menghasilkan %v, ingin ErrInvalidInput", err)
	}

	empty := "   "
	if _, err := svc.UpdateInventoryAccount(context.Background(), id, UpdateInventoryAccountInput{Label: &empty}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("label kosong menghasilkan %v, ingin ErrInvalidInput", err)
	}
}

func TestUpdateInventoryAccount_RejectsZeroSharingLimit(t *testing.T) {
	id := uuid.NewString()
	svc := NewAdminInventoryService(&stubInventoryRepo{
		accountWithCredentials: &domain.InventoryAccount{ID: id, SharingLimit: 3},
	}, testCipher(t))

	zero := 0
	if _, err := svc.UpdateInventoryAccount(context.Background(), id, UpdateInventoryAccountInput{SharingLimit: &zero}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("limit nol menghasilkan %v, ingin ErrInvalidInput", err)
	}
}

func TestListInventoryAccounts_RejectsMalformedProductFilter(t *testing.T) {
	svc := NewAdminInventoryService(&stubInventoryRepo{}, nil)

	if _, _, err := svc.ListInventoryAccounts(context.Background(), InventoryAccountFilter{ProductID: "not-a-uuid"}, 1, 20); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("galat %v, ingin ErrInvalidInput", err)
	}
}

func TestListInventoryAccounts_RejectsUnknownStatusFilter(t *testing.T) {
	svc := NewAdminInventoryService(&stubInventoryRepo{}, nil)

	if _, _, err := svc.ListInventoryAccounts(context.Background(), InventoryAccountFilter{Status: "paused"}, 1, 20); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("galat %v, ingin ErrInvalidInput", err)
	}
}

func TestListSlots_UnknownAccountIsNotFound(t *testing.T) {
	svc := NewAdminInventoryService(&stubInventoryRepo{}, nil)

	if _, _, err := svc.ListSlots(context.Background(), "not-a-uuid", "", "", 1, 50); !errors.Is(err, ErrNotFound) {
		t.Fatalf("galat %v, ingin ErrNotFound", err)
	}
}

func TestListSlots_RejectsMalformedPriceFilter(t *testing.T) {
	svc := NewAdminInventoryService(&stubInventoryRepo{}, nil)

	if _, _, err := svc.ListSlots(context.Background(), uuid.NewString(), "not-a-uuid", "", 1, 50); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("galat %v, ingin ErrInvalidInput", err)
	}
}

func TestListSlots_ReturnsAssignments(t *testing.T) {
	accountID := uuid.NewString()
	repo := &stubInventoryRepo{
		account:     &domain.InventoryAccount{ID: accountID},
		assignments: []domain.InventorySlotAssignment{{ID: "slot-1", Status: "reserved"}},
		assignTotal: 1,
	}
	svc := NewAdminInventoryService(repo, nil)

	slots, total, err := svc.ListSlots(context.Background(), accountID, "", "reserved", 1, 50)

	if err != nil {
		t.Fatalf("ListSlots: %v", err)
	}
	if total != 1 || len(slots) != 1 || slots[0].ID != "slot-1" {
		t.Fatalf("slots=%v total=%d, ingin satu assignment slot-1", slots, total)
	}
}

func TestGetInventoryAccount_StripsCredentials(t *testing.T) {
	id := uuid.NewString()
	repo := &stubInventoryRepo{
		account: &domain.InventoryAccount{ID: id, EncryptedCredentials: []byte("cipher")},
	}
	svc := NewAdminInventoryService(repo, nil)

	account, err := svc.GetInventoryAccount(context.Background(), id)

	if err != nil {
		t.Fatalf("GetInventoryAccount: %v", err)
	}
	if account.EncryptedCredentials != nil {
		t.Fatal("GetInventoryAccount mengembalikan ciphertext")
	}
}

func TestNormalizeSharingLimitAndStatus(t *testing.T) {
	if limit, err := normalizeSharingLimit(0); err != nil || limit != DefaultSharingLimit {
		t.Fatalf("normalizeSharingLimit(0) = %d, %v", limit, err)
	}
	if _, err := normalizeSharingLimit(MaxSharingLimit + 1); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("limit terlalu besar menghasilkan %v", err)
	}
	if status, ok := normalizeInventoryStatus(" EXPIRED "); !ok || status != InventoryStatusExpired {
		t.Fatalf("normalizeInventoryStatus(expired) = %q, %t", status, ok)
	}
	if _, ok := normalizeInventoryStatus("paused"); ok {
		t.Fatal("status tak dikenal diterima")
	}
}
