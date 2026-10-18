package gormrepo

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/migrate"
	"github.com/necly/services/internal/platform/testdb"
	"github.com/necly/services/internal/service"
	"gorm.io/gorm"
)

type checkoutFixture struct {
	db        *gorm.DB
	store     *Store
	userA     string
	userB     string
	category  string
	product   string
	duration  string
	price     string
	voucherID string
}

func newCheckoutFixture(t *testing.T) *checkoutFixture {
	t.Helper()
	if !testdb.Available() {
		t.Skip("DB_HOST/DB_USER not set; skipping PostgreSQL integration test")
	}
	db, cleanup, err := testdb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	migrations, err := filepath.Abs(filepath.Join("..", "..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Run(context.Background(), db, migrate.Options{Dir: migrations}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Now().UTC()
	fixture := &checkoutFixture{
		db: db, store: NewStore(db),
		userA: uuid.NewString(), userB: uuid.NewString(),
		category: uuid.NewString(), product: uuid.NewString(),
		duration: uuid.NewString(), price: uuid.NewString(), voucherID: uuid.NewString(),
	}
	users := []domain.User{
		{ID: fixture.userA, Email: "fixture.customer@example.test", Name: "Fixture Customer", PasswordHash: "x", Role: domain.RoleCustomer, Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: fixture.userB, Email: "fixture.other@example.test", Name: "Fixture Other", PasswordHash: "x", Role: domain.RoleCustomer, Active: true, CreatedAt: now, UpdatedAt: now},
	}
	for index := range users {
		if err := db.Create(&users[index]).Error; err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}
	if err := db.Create(&domain.Category{ID: fixture.category, Name: "Streaming Video", Slug: "video-streaming-" + fixture.category[:8], Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed category: %v", err)
	}
	if err := db.Create(&domain.Product{ID: fixture.product, CategoryID: fixture.category, Name: "Netflix Premium", Slug: "netflix-" + fixture.product[:8], Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed product: %v", err)
	}
	if err := db.Create(&domain.ProductDuration{ID: fixture.duration, Name: "1 Bulan", Days: 30, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed duration: %v", err)
	}
	if err := db.Create(&domain.ProductPrice{ID: fixture.price, ProductID: fixture.product, DurationID: fixture.duration, Amount: 186000, Currency: "IDR", Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed price: %v", err)
	}
	account := domain.InventoryAccount{ID: uuid.NewString(), ProductID: fixture.product, EncryptedCredentials: []byte("fixture"), Status: "active", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&account).Error; err != nil {
		t.Fatalf("seed inventory account: %v", err)
	}
	for index := 0; index < 2; index++ {
		slot := domain.InventorySlot{ID: uuid.NewString(), InventoryAccountID: account.ID, ProductPriceID: fixture.price, Status: "available", CreatedAt: now, UpdatedAt: now}
		if err := db.Create(&slot).Error; err != nil {
			t.Fatalf("seed slot: %v", err)
		}
	}
	voucher := domain.Voucher{
		ID: fixture.voucherID, Code: "WELCOME10", Type: domain.VoucherPercentage, Value: 10,
		MinOrder: 50000, MaxDiscount: 50000, UsageLimit: 100, PerUserLimit: 1,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), Active: true,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&voucher).Error; err != nil {
		t.Fatalf("seed voucher: %v", err)
	}
	return fixture
}

type stubProvider struct {
	calls int
	err   error
}

func (p *stubProvider) Name() string { return "mock" }

func (p *stubProvider) CreatePayment(ctx context.Context, request service.PaymentRequest) (*service.PaymentInitiation, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	return &service.PaymentInitiation{
		ProviderPaymentID: "pay_" + request.OrderID,
		Status:            "pending",
		CheckoutURL:       "https://pay.mock/" + request.OrderID,
	}, nil
}

func (p *stubProvider) VerifyWebhook(headers map[string]string, body []byte) (*service.PaymentEvent, error) {
	return nil, nil
}

func (fixture *checkoutFixture) checkout(t *testing.T, userID, key, voucher string) (*service.CheckoutResult, error) {
	t.Helper()
	provider := &stubProvider{}
	svc := service.NewCheckoutService(fixture.store, provider, 15*time.Minute)
	return svc.Checkout(context.Background(), service.CheckoutInput{
		UserID: userID, IdempotencyKey: key, VoucherCode: voucher,
		Items: []service.CheckoutItemInput{{ProductPriceID: fixture.price, Quantity: 1}},
	})
}

func TestCheckoutPersistsResolvedVoucherUUID(t *testing.T) {
	fixture := newCheckoutFixture(t)
	result, err := fixture.checkout(t, fixture.userA, "fixture-voucher-0001", "welcome10")
	if err != nil {
		t.Fatalf("checkout with voucher: %v", err)
	}
	if result.Order.VoucherID == nil {
		t.Fatal("voucher_id is nil; resolved voucher was not persisted")
	}
	if *result.Order.VoucherID != fixture.voucherID {
		t.Fatalf("voucher_id = %q, want voucher UUID %q", *result.Order.VoucherID, fixture.voucherID)
	}
	if result.Order.Discount != 18600 || result.Order.Total != 167400 {
		t.Fatalf("discount/total = %d/%d, want 18600/167400", result.Order.Discount, result.Order.Total)
	}

	var stored domain.Order
	if err := fixture.db.Where("id = ?", result.Order.ID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.VoucherID == nil || *stored.VoucherID != fixture.voucherID {
		t.Fatalf("stored voucher_id = %v, want %q", stored.VoucherID, fixture.voucherID)
	}
	var redemption domain.VoucherRedemption
	if err := fixture.db.Where("order_id = ?", result.Order.ID).First(&redemption).Error; err != nil {
		t.Fatalf("voucher redemption missing: %v", err)
	}
	if redemption.VoucherID != fixture.voucherID || redemption.Amount != 18600 {
		t.Fatalf("redemption = %#v", redemption)
	}
}

func TestCheckoutWithoutVoucherStoresNull(t *testing.T) {
	fixture := newCheckoutFixture(t)
	result, err := fixture.checkout(t, fixture.userA, "fixture-novoucher-0001", "")
	if err != nil {
		t.Fatalf("checkout without voucher: %v", err)
	}
	if result.Order.VoucherID != nil {
		t.Fatalf("voucher_id = %q, want NULL", *result.Order.VoucherID)
	}
	if result.Order.Discount != 0 || result.Order.Total != 186000 {
		t.Fatalf("discount/total = %d/%d, want 0/186000", result.Order.Discount, result.Order.Total)
	}
}

func TestInvalidVoucherReturnsNotFoundWithoutMutation(t *testing.T) {
	fixture := newCheckoutFixture(t)
	_, err := fixture.checkout(t, fixture.userA, "fixture-invalid-voucher", "DOES-NOT-EXIST")
	if !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
	assertCheckoutMutationCounts(t, fixture.db, 0, 0, 2)
}

func TestExpiredVoucherReturnsNotFoundWithoutMutation(t *testing.T) {
	fixture := newCheckoutFixture(t)
	if err := fixture.db.Model(&domain.Voucher{}).Where("id = ?", fixture.voucherID).
		Update("ends_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	_, err := fixture.checkout(t, fixture.userA, "fixture-expired-voucher", "WELCOME10")
	if !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
	assertCheckoutMutationCounts(t, fixture.db, 0, 0, 2)
}

func assertCheckoutMutationCounts(t *testing.T, db *gorm.DB, orders, payments, availableSlots int64) {
	t.Helper()
	var gotOrders, gotPayments, gotSlots int64
	db.Model(&domain.Order{}).Count(&gotOrders)
	db.Model(&domain.Payment{}).Count(&gotPayments)
	db.Model(&domain.InventorySlot{}).Where("status = ?", "available").Count(&gotSlots)
	if gotOrders != orders || gotPayments != payments || gotSlots != availableSlots {
		t.Fatalf("mutation counts orders=%d payments=%d available_slots=%d, want %d/%d/%d",
			gotOrders, gotPayments, gotSlots, orders, payments, availableSlots)
	}
}
