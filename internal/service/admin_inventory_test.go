package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
