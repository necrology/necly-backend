package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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

	legacyCredentials []byte
	backfilledID      string
	backfilledEmail   string

	slot          *domain.InventorySlot
	slotErr       error
	extended      *domain.InventorySlot
	extendErr     error
	extendSlot    []string
	extendDays    []int
	deactivated   *domain.InventorySlot
	deactivateErr error

	expiry               InventoryExpiryResult
	expiryErr            error
	slotReminders        map[int][]InventoryExpiryReminder
	accountReminders     map[int][]InventoryExpiryReminder
	adminIDs             []string
	adminErr             error
	createdNotifications []domain.Notification
	notificationKeys     map[string]bool
	notificationErr      error
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
	// Copy the value: the service clears write-only ciphertext on the same
	// struct before returning, so keeping the pointer would hide what was saved.
	copied := *a
	copied.EncryptedCredentials = append([]byte(nil), a.EncryptedCredentials...)
	copied.OTPSecretEncrypted = append([]byte(nil), a.OTPSecretEncrypted...)
	s.updatedAccount = &copied
	return nil
}

func (s *stubInventoryRepo) GetAccountEncryptedCredentials(context.Context, string) ([]byte, error) {
	return s.legacyCredentials, nil
}

func (s *stubInventoryRepo) BackfillAccountEmail(_ context.Context, id, email string) error {
	s.backfilledID = id
	s.backfilledEmail = email
	return nil
}

func (s *stubInventoryRepo) GetSlot(context.Context, string) (*domain.InventorySlot, error) {
	if s.slotErr != nil {
		return nil, s.slotErr
	}
	return s.slot, nil
}

func (s *stubInventoryRepo) ExtendSlotAccess(_ context.Context, slotID string, _ time.Time, durationDays int) (*domain.InventorySlot, error) {
	s.extendSlot = append(s.extendSlot, slotID)
	s.extendDays = append(s.extendDays, durationDays)
	if s.extendErr != nil {
		return nil, s.extendErr
	}
	return s.extended, nil
}

func (s *stubInventoryRepo) DeactivateSlot(context.Context, string, time.Time) (*domain.InventorySlot, error) {
	if s.deactivateErr != nil {
		return nil, s.deactivateErr
	}
	return s.deactivated, nil
}

func (s *stubInventoryRepo) ExpireInventory(context.Context, time.Time, int) (InventoryExpiryResult, error) {
	if s.expiryErr != nil {
		return InventoryExpiryResult{}, s.expiryErr
	}
	result := s.expiry
	// A fake batch is consumed once; subsequent runs observe an empty table.
	s.expiry = InventoryExpiryResult{}
	return result, nil
}

func (s *stubInventoryRepo) ListSlotExpiryReminders(_ context.Context, _ time.Time, windowDays int) ([]InventoryExpiryReminder, error) {
	return s.slotReminders[windowDays], nil
}

func (s *stubInventoryRepo) ListAccountExpiryReminders(_ context.Context, _ time.Time, windowDays int) ([]InventoryExpiryReminder, error) {
	return s.accountReminders[windowDays], nil
}

func (s *stubInventoryRepo) ListActiveAdminIDs(context.Context) ([]string, error) {
	if s.adminErr != nil {
		return nil, s.adminErr
	}
	return s.adminIDs, nil
}

func (s *stubInventoryRepo) CreateNotifications(_ context.Context, notifications []domain.Notification) (int, error) {
	if s.notificationErr != nil {
		return 0, s.notificationErr
	}
	if s.notificationKeys == nil {
		s.notificationKeys = map[string]bool{}
	}
	created := 0
	for _, notification := range notifications {
		if notification.DedupeKey != nil {
			if s.notificationKeys[*notification.DedupeKey] {
				continue
			}
			s.notificationKeys[*notification.DedupeKey] = true
		}
		s.createdNotifications = append(s.createdNotifications, notification)
		created++
	}
	return created, nil
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

func TestSharingLimitEnforcesFiveUserRule(t *testing.T) {
	if MaxSharingLimit != 5 {
		t.Fatalf("MaxSharingLimit = %d, want 5", MaxSharingLimit)
	}
	if limit, err := normalizeSharingLimit(5); err != nil || limit != 5 {
		t.Fatalf("normalizeSharingLimit(5) = %d, %v", limit, err)
	}
	if _, err := normalizeSharingLimit(6); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("limit 6 yielded %v, want ErrInvalidInput", err)
	}
}

func TestUpdateInventoryAccountRejectsSharingLimitAboveFive(t *testing.T) {
	id := uuid.NewString()
	repo := &stubInventoryRepo{
		accountWithCredentials: &domain.InventoryAccount{ID: id, SharingLimit: 5},
	}
	svc := NewAdminInventoryService(repo, testCipher(t))

	for _, limit := range []int{0, 6, 1000} {
		value := limit
		if _, err := svc.UpdateInventoryAccount(context.Background(), id, UpdateInventoryAccountInput{SharingLimit: &value}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("limit %d yielded %v, want ErrInvalidInput", limit, err)
		}
	}
}

func TestGetInventoryAccountBackfillsLegacyAccountEmail(t *testing.T) {
	cipher := testCipher(t)
	sealed, err := cipher.Encrypt([]byte(`{"username":"legacy@example.test","password":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	repo := &stubInventoryRepo{
		account:           &domain.InventoryAccount{ID: id, SharingLimit: 1},
		legacyCredentials: sealed,
	}
	svc := NewAdminInventoryService(repo, cipher)

	account, err := svc.GetInventoryAccount(context.Background(), id)
	if err != nil {
		t.Fatalf("GetInventoryAccount: %v", err)
	}
	if account.AccountEmail != "legacy@example.test" {
		t.Fatalf("account_email = %q", account.AccountEmail)
	}
	if repo.backfilledID != id || repo.backfilledEmail != "legacy@example.test" {
		t.Fatalf("backfill = %q/%q", repo.backfilledID, repo.backfilledEmail)
	}
	if account.EncryptedCredentials != nil {
		t.Fatal("response kept the credential ciphertext")
	}
}

func TestGetInventoryAccountIgnoresBrokenLegacyCredential(t *testing.T) {
	id := uuid.NewString()
	repo := &stubInventoryRepo{
		account:           &domain.InventoryAccount{ID: id, SharingLimit: 1},
		legacyCredentials: []byte("not-a-valid-ciphertext"),
	}
	svc := NewAdminInventoryService(repo, testCipher(t))

	account, err := svc.GetInventoryAccount(context.Background(), id)
	if err != nil {
		t.Fatalf("GetInventoryAccount: %v", err)
	}
	if account.AccountEmail != "" || repo.backfilledID != "" {
		t.Fatalf("broken row was backfilled: email=%q id=%q", account.AccountEmail, repo.backfilledID)
	}
}

func TestPresentAccountFlagsLegacyLimitAndStripsOTPSecret(t *testing.T) {
	cipher := testCipher(t)
	sealed, err := cipher.Encrypt([]byte("JBSWY3DPEHPK3PXP"))
	if err != nil {
		t.Fatal(err)
	}
	svc := NewAdminInventoryService(&stubInventoryRepo{}, cipher)
	account := &domain.InventoryAccount{
		ID:                   uuid.NewString(),
		SharingLimit:         10,
		TotalSlots:           2,
		OTPSecretEncrypted:   sealed,
		EncryptedCredentials: []byte("cipher"),
	}

	svc.presentAccount(account)

	if !account.NeedsReview {
		t.Fatal("legacy account above five users must be flagged needs_review")
	}
	if !account.OTPConfigured {
		t.Fatal("an encrypted secret must be reported as otp_configured")
	}
	if account.OTPSecretEncrypted != nil || account.EncryptedCredentials != nil {
		t.Fatal("presentAccount left write-only ciphertext on the response")
	}
	payload, err := json.Marshal(account)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte("otp_secret")) || bytes.Contains(payload, []byte("JBSWY3DPEHPK3PXP")) {
		t.Fatalf("account JSON leaked OTP material: %s", payload)
	}
}

func TestGenerateOTPUsesRFC6238AtFixedTime(t *testing.T) {
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" // base32("12345678901234567890")
	id := uuid.NewString()
	cipher := testCipher(t)
	sealed, err := cipher.Encrypt([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	repo := &stubInventoryRepo{accountWithCredentials: &domain.InventoryAccount{ID: id, OTPSecretEncrypted: sealed}}
	svc := NewAdminInventoryService(repo, cipher)
	svc.now = func() time.Time { return time.Unix(59, 0).UTC() }

	code, err := svc.GenerateOTP(context.Background(), id, "admin-1")
	if err != nil {
		t.Fatalf("GenerateOTP: %v", err)
	}
	if code.Code != "287082" || code.Period != 30 || code.ExpiresIn != 1 {
		t.Fatalf("code = %+v, want 287082 period 30 expires_in 1", code)
	}
	payload, err := json.Marshal(code)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(secret)) || bytes.Contains(payload, []byte("secret")) {
		t.Fatalf("OTP response leaked the secret: %s", payload)
	}
}

func TestGenerateOTPWithoutSecretIsNotConfigured(t *testing.T) {
	id := uuid.NewString()
	svc := NewAdminInventoryService(&stubInventoryRepo{
		accountWithCredentials: &domain.InventoryAccount{ID: id},
	}, testCipher(t))

	if _, err := svc.GenerateOTP(context.Background(), id, "admin-1"); !errors.Is(err, ErrOTPNotConfigured) {
		t.Fatalf("error = %v, want ErrOTPNotConfigured", err)
	}
}

func TestGenerateOTPIsRateLimitedPerAccountAndActor(t *testing.T) {
	id := uuid.NewString()
	cipher := testCipher(t)
	sealed, err := cipher.Encrypt([]byte("JBSWY3DPEHPK3PXP"))
	if err != nil {
		t.Fatal(err)
	}
	svc := NewAdminInventoryService(&stubInventoryRepo{
		accountWithCredentials: &domain.InventoryAccount{ID: id, OTPSecretEncrypted: sealed},
	}, cipher)
	svc.now = func() time.Time { return time.Unix(59, 0).UTC() }
	svc.otp.now = svc.now

	for attempt := 0; attempt < otpRateLimit; attempt++ {
		if _, err := svc.GenerateOTP(context.Background(), id, "admin-1"); err != nil {
			t.Fatalf("attempt %d: %v", attempt+1, err)
		}
	}
	if _, err := svc.GenerateOTP(context.Background(), id, "admin-1"); !errors.Is(err, ErrOTPRateLimited) {
		t.Fatalf("error after budget = %v, want ErrOTPRateLimited", err)
	}
	// Another admin has an independent budget for the same account.
	if _, err := svc.GenerateOTP(context.Background(), id, "admin-2"); err != nil {
		t.Fatalf("other actor: %v", err)
	}
}

func TestCreateInventoryAccountSealsOptionalOTPSecret(t *testing.T) {
	productID := uuid.NewString()
	repo := &stubInventoryRepo{productExists: true}
	svc := NewAdminInventoryService(repo, testCipher(t))
	secret := "jbswy3dpehpk3pxp"

	account, err := svc.CreateInventoryAccount(context.Background(), CreateInventoryAccountInput{
		ProductID:   productID,
		Credentials: "user:pass",
		OTPSecret:   &secret,
	})
	if err != nil {
		t.Fatalf("CreateInventoryAccount: %v", err)
	}
	if repo.createdAccount == nil || len(repo.createdAccount.OTPSecretEncrypted) == 0 {
		t.Fatal("OTP secret was not encrypted before persisting")
	}
	if !account.OTPConfigured || account.OTPSecretEncrypted != nil {
		t.Fatalf("created account OTPConfigured=%t secret=%v", account.OTPConfigured, account.OTPSecretEncrypted)
	}
	if bytes.Contains(mustJSON(t, account), []byte(secret)) || bytes.Contains(mustJSON(t, account), []byte("otp_secret")) {
		t.Fatal("create response leaked OTP material")
	}
}

func TestUpdateInventoryAccountOTPSecretOmittedPreservesEmptyRemoves(t *testing.T) {
	id := uuid.NewString()
	cipher := testCipher(t)
	sealed, err := cipher.Encrypt([]byte("JBSWY3DPEHPK3PXP"))
	if err != nil {
		t.Fatal(err)
	}
	repo := &stubInventoryRepo{
		accountWithCredentials: &domain.InventoryAccount{ID: id, SharingLimit: 5, OTPSecretEncrypted: sealed},
	}
	svc := NewAdminInventoryService(repo, cipher)

	label := "Baru"
	if _, err := svc.UpdateInventoryAccount(context.Background(), id, UpdateInventoryAccountInput{Label: &label}); err != nil {
		t.Fatalf("update label: %v", err)
	}
	if len(repo.updatedAccount.OTPSecretEncrypted) == 0 {
		t.Fatal("omitting otp_secret removed the stored secret")
	}

	empty := ""
	if _, err := svc.UpdateInventoryAccount(context.Background(), id, UpdateInventoryAccountInput{OTPSecret: &empty}); err != nil {
		t.Fatalf("clear otp secret: %v", err)
	}
	if repo.updatedAccount.OTPSecretEncrypted != nil {
		t.Fatal("otp_secret empty string did not remove the stored secret")
	}
}

func TestUpdateInventoryAccountRejectsMalformedOTPSecret(t *testing.T) {
	id := uuid.NewString()
	repo := &stubInventoryRepo{accountWithCredentials: &domain.InventoryAccount{ID: id, SharingLimit: 5}}
	svc := NewAdminInventoryService(repo, testCipher(t))
	bad := "not a base32 secret!"

	if _, err := svc.UpdateInventoryAccount(context.Background(), id, UpdateInventoryAccountInput{OTPSecret: &bad}); !errors.Is(err, ErrOTPSecretInvalid) {
		t.Fatalf("error = %v, want ErrOTPSecretInvalid", err)
	}
}

func TestCreateInventoryAccountAcceptsSeparateLoginAndMailboxSecrets(t *testing.T) {
	productID := uuid.NewString()
	repo := &stubInventoryRepo{productExists: true}
	cipher := testCipher(t)
	svc := NewAdminInventoryService(repo, cipher)
	appPassword := "abcd efgh ijkl mnop"

	account, err := svc.CreateInventoryAccount(context.Background(), CreateInventoryAccountInput{
		ProductID:          productID,
		AccountEmail:       "Family@Example.test ",
		AccountPassword:    "login-secret",
		MailboxAppPassword: &appPassword,
		MailboxIMAPHost:    "imap.gmail.com:993",
	})
	if err != nil {
		t.Fatalf("CreateInventoryAccount: %v", err)
	}
	if repo.createdAccount == nil {
		t.Fatal("account was not persisted")
	}
	if repo.createdAccount.AccountEmail != "Family@Example.test" {
		t.Fatalf("account_email = %q", repo.createdAccount.AccountEmail)
	}
	username, password, _, ok := decryptInventoryCredential(t, cipher, repo.createdAccount.EncryptedCredentials)
	if !ok || username != "Family@Example.test" || password != "login-secret" {
		t.Fatalf("stored login = %q/%q ok=%t", username, password, ok)
	}
	if len(repo.createdAccount.MailboxAppPasswordEncrypted) == 0 {
		t.Fatal("mailbox app password was not encrypted")
	}
	plainApp, err := cipher.Decrypt(repo.createdAccount.MailboxAppPasswordEncrypted)
	if err != nil || string(plainApp) != appPassword {
		t.Fatalf("stored app password = %q, err=%v", plainApp, err)
	}
	if account.OTPInboxSource != "direct" || !account.OTPInboxConfigured {
		t.Fatalf("otp inbox = %s/%t, want direct/true", account.OTPInboxSource, account.OTPInboxConfigured)
	}
	if account.MailboxIMAPHost != "imap.gmail.com" {
		t.Fatalf("mailbox host = %q", account.MailboxIMAPHost)
	}
	payload := string(mustJSON(t, account))
	for _, secret := range []string{"login-secret", appPassword, "mailbox_app_password", "account_password", "encrypted_credentials"} {
		if bytes.Contains([]byte(payload), []byte(secret)) {
			t.Fatalf("response leaked %q: %s", secret, payload)
		}
	}
}

func TestCreateInventoryAccountWithoutMailboxUsesCentralSource(t *testing.T) {
	repo := &stubInventoryRepo{productExists: true}
	svc := NewAdminInventoryService(repo, testCipher(t))
	svc.SetCentralOTPInbox(true)

	account, err := svc.CreateInventoryAccount(context.Background(), CreateInventoryAccountInput{
		ProductID:       uuid.NewString(),
		AccountEmail:    "family@example.test",
		AccountPassword: "secret",
	})
	if err != nil {
		t.Fatalf("CreateInventoryAccount: %v", err)
	}
	if account.MailboxIMAPHost != DefaultMailboxIMAPHost {
		t.Fatalf("mailbox host = %q, want default", account.MailboxIMAPHost)
	}
	if !account.OTPInboxConfigured || account.OTPInboxSource != "central" {
		t.Fatalf("otp inbox = %s/%t, want central/true", account.OTPInboxSource, account.OTPInboxConfigured)
	}
}

func TestUpdateInventoryAccountMergesLoginFieldsIndependently(t *testing.T) {
	cipher := testCipher(t)
	old := inventoryLoginPayload{Username: "old@example.test", Password: "old-pass", Instructions: "Pakai profil 2"}
	oldEncrypted, err := cipher.Encrypt(mustJSON(t, old))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("email only keeps password and instructions", func(t *testing.T) {
		repo := &stubInventoryRepo{accountWithCredentials: &domain.InventoryAccount{
			ID: uuid.NewString(), SharingLimit: 5, AccountEmail: old.Username,
			EncryptedCredentials: oldEncrypted,
		}}
		svc := NewAdminInventoryService(repo, cipher)
		email := "new@example.test"
		if _, err := svc.UpdateInventoryAccount(context.Background(), repo.accountWithCredentials.ID, UpdateInventoryAccountInput{AccountEmail: &email}); err != nil {
			t.Fatalf("update email: %v", err)
		}
		username, password, instructions, ok := decryptInventoryCredential(t, cipher, repo.updatedAccount.EncryptedCredentials)
		if !ok || username != email || password != old.Password || instructions != old.Instructions {
			t.Fatalf("merged login = %q/%q/%q ok=%t", username, password, instructions, ok)
		}
		if repo.updatedAccount.AccountEmail != email {
			t.Fatalf("account_email = %q", repo.updatedAccount.AccountEmail)
		}
	})

	t.Run("password only keeps email and instructions", func(t *testing.T) {
		repo := &stubInventoryRepo{accountWithCredentials: &domain.InventoryAccount{
			ID: uuid.NewString(), SharingLimit: 5, AccountEmail: old.Username,
			EncryptedCredentials: oldEncrypted,
		}}
		svc := NewAdminInventoryService(repo, cipher)
		password := "new-pass"
		if _, err := svc.UpdateInventoryAccount(context.Background(), repo.accountWithCredentials.ID, UpdateInventoryAccountInput{AccountPassword: &password}); err != nil {
			t.Fatalf("update password: %v", err)
		}
		username, storedPassword, instructions, ok := decryptInventoryCredential(t, cipher, repo.updatedAccount.EncryptedCredentials)
		if !ok || username != old.Username || storedPassword != password || instructions != old.Instructions {
			t.Fatalf("merged login = %q/%q/%q ok=%t", username, storedPassword, instructions, ok)
		}
		if repo.updatedAccount.AccountEmail != old.Username {
			t.Fatalf("account_email = %q", repo.updatedAccount.AccountEmail)
		}
	})

	t.Run("both fields replace login and empty app password returns to central", func(t *testing.T) {
		sealedApp, err := cipher.Encrypt([]byte("old-app-password"))
		if err != nil {
			t.Fatal(err)
		}
		repo := &stubInventoryRepo{accountWithCredentials: &domain.InventoryAccount{
			ID: uuid.NewString(), SharingLimit: 5, AccountEmail: old.Username,
			EncryptedCredentials: oldEncrypted, MailboxAppPasswordEncrypted: sealedApp,
			MailboxIMAPHost: "imap.gmail.com",
		}}
		svc := NewAdminInventoryService(repo, cipher)
		svc.SetCentralOTPInbox(true)
		email := "both@example.test"
		password := "both-pass"
		empty := ""
		account, err := svc.UpdateInventoryAccount(context.Background(), repo.accountWithCredentials.ID, UpdateInventoryAccountInput{
			AccountEmail: &email, AccountPassword: &password, MailboxAppPassword: &empty,
		})
		if err != nil {
			t.Fatalf("update both: %v", err)
		}
		username, storedPassword, _, ok := decryptInventoryCredential(t, cipher, repo.updatedAccount.EncryptedCredentials)
		if !ok || username != email || storedPassword != password {
			t.Fatalf("merged login = %q/%q ok=%t", username, storedPassword, ok)
		}
		if repo.updatedAccount.MailboxAppPasswordEncrypted != nil {
			t.Fatal("explicit empty app password did not clear the stored value")
		}
		if account.OTPInboxSource != "central" || !account.OTPInboxConfigured {
			t.Fatalf("otp inbox = %s/%t, want central/true", account.OTPInboxSource, account.OTPInboxConfigured)
		}
	})

	t.Run("omitted app password is preserved", func(t *testing.T) {
		sealedApp, err := cipher.Encrypt([]byte("keep-app-password"))
		if err != nil {
			t.Fatal(err)
		}
		repo := &stubInventoryRepo{accountWithCredentials: &domain.InventoryAccount{
			ID: uuid.NewString(), SharingLimit: 5, AccountEmail: old.Username,
			EncryptedCredentials: oldEncrypted, MailboxAppPasswordEncrypted: sealedApp,
		}}
		svc := NewAdminInventoryService(repo, cipher)
		label := "Baru"
		if _, err := svc.UpdateInventoryAccount(context.Background(), repo.accountWithCredentials.ID, UpdateInventoryAccountInput{Label: &label}); err != nil {
			t.Fatalf("update label: %v", err)
		}
		if string(repo.updatedAccount.MailboxAppPasswordEncrypted) != string(sealedApp) {
			t.Fatal("omitting mailbox_app_password changed the stored value")
		}
	})
}

func TestInventoryAccountRejectsInvalidEmailPasswordAndHost(t *testing.T) {
	cipher := testCipher(t)
	productID := uuid.NewString()
	cases := []CreateInventoryAccountInput{
		{ProductID: productID, AccountEmail: "not-an-email", AccountPassword: "pass"},
		{ProductID: productID, AccountEmail: "family@example.test", AccountPassword: "   "},
		{ProductID: productID, AccountEmail: "family@example.test", AccountPassword: "pass", MailboxIMAPHost: "https://imap.gmail.com"},
		{ProductID: productID, AccountEmail: "family@example.test", AccountPassword: "pass", MailboxIMAPHost: "imap.gmail.com:notaport"},
		{ProductID: productID, AccountEmail: "family@example.test", AccountPassword: "pass", MailboxIMAPHost: "imap.gmail.com:443"},
		{ProductID: productID, AccountEmail: "family@example.test", AccountPassword: "pass", MailboxIMAPHost: "imap.gmail.com:143"},
		{ProductID: productID, AccountEmail: "family@example.test", AccountPassword: "pass", MailboxIMAPHost: "localhost"},
		{ProductID: productID, AccountEmail: "family@example.test", AccountPassword: "pass", MailboxIMAPHost: "127.0.0.1"},
		{ProductID: productID, AccountEmail: "family@example.test", AccountPassword: "pass", MailboxIMAPHost: "169.254.169.254"},
		{ProductID: productID, AccountEmail: "family@example.test", AccountPassword: "pass", MailboxIMAPHost: "metadata.google.internal"},
		{ProductID: productID, AccountEmail: "family@example.test", AccountPassword: "pass", MailboxIMAPHost: "[::1]"},
		{ProductID: productID, AccountEmail: "family@example.test", AccountPassword: "pass", MailboxIMAPHost: "imap.gmail.com.evil.test"},
		{ProductID: productID, AccountEmail: "family@example.test", AccountPassword: "pass", MailboxIMAPHost: "user@imap.gmail.com"},
		{ProductID: productID, AccountEmail: "family@example.test", AccountPassword: "pass", MailboxIMAPHost: strings.Repeat("a", maxMailboxIMAPHostLength+1)},
	}
	for _, input := range cases {
		repo := &stubInventoryRepo{productExists: true}
		svc := NewAdminInventoryService(repo, cipher)
		if _, err := svc.CreateInventoryAccount(context.Background(), input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("input %+v yielded %v, want ErrInvalidInput", input, err)
		}
	}
}

func TestUpdateInventoryAccountRejectsDisallowedMailboxHost(t *testing.T) {
	cipher := testCipher(t)
	sealedApp, err := cipher.Encrypt([]byte("app-password-secret"))
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{
		"localhost",
		"127.0.0.1:993",
		"[::1]",
		"169.254.169.254",
		"metadata.google.internal:993",
		"imap.gmail.com:443",
		"imap.gmail.com:143",
		"imap.gmail.com.evil.test",
		"https://imap.gmail.com",
	} {
		repo := &stubInventoryRepo{accountWithCredentials: &domain.InventoryAccount{
			ID: uuid.NewString(), SharingLimit: 5, AccountEmail: "family@example.test",
			MailboxAppPasswordEncrypted: sealedApp, MailboxIMAPHost: "imap.gmail.com",
		}}
		svc := NewAdminInventoryService(repo, cipher)
		hostValue := host
		appPassword := "new-app-password"
		_, err := svc.UpdateInventoryAccount(context.Background(), repo.accountWithCredentials.ID, UpdateInventoryAccountInput{
			MailboxIMAPHost: &hostValue, MailboxAppPassword: &appPassword,
		})
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("host %q yielded %v, want ErrInvalidInput", host, err)
		}
		if repo.updatedAccount != nil {
			t.Fatalf("host %q was persisted", host)
		}
		for _, secret := range []string{appPassword, "app-password-secret"} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("host %q error leaked a credential", host)
			}
		}
	}
}

func decryptInventoryCredential(t *testing.T, cipher *CredentialCipher, sealed []byte) (string, string, string, bool) {
	t.Helper()
	plain, err := cipher.Decrypt(sealed)
	if err != nil {
		t.Fatalf("decrypt credential: %v", err)
	}
	return parseInventoryCredential(plain)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
