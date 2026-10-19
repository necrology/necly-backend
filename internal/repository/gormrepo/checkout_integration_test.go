package gormrepo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	priceB    string
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
		duration: uuid.NewString(), price: uuid.NewString(), priceB: uuid.NewString(), voucherID: uuid.NewString(),
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
	if err := db.Create(&domain.ProductPrice{ID: fixture.priceB, ProductID: fixture.product, DurationID: fixture.duration, Amount: 99000, Currency: "IDR", Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed second price: %v", err)
	}
	account := domain.InventoryAccount{ID: uuid.NewString(), ProductID: fixture.product, EncryptedCredentials: []byte("fixture"), Status: "active", SharingLimit: 3, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&account).Error; err != nil {
		t.Fatalf("seed inventory account: %v", err)
	}
	for index := 0; index < 2; index++ {
		slot := domain.InventorySlot{ID: uuid.NewString(), InventoryAccountID: account.ID, ProductPriceID: fixture.price, Status: "available", CreatedAt: now, UpdatedAt: now}
		if err := db.Create(&slot).Error; err != nil {
			t.Fatalf("seed slot: %v", err)
		}
	}
	if err := db.Create(&domain.InventorySlot{ID: uuid.NewString(), InventoryAccountID: account.ID, ProductPriceID: fixture.priceB, Status: "available", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed second slot: %v", err)
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
	calls atomic.Int32
	err   error
	name  string
	mu    sync.Mutex
	seen  []service.PaymentRequest
}

func (p *stubProvider) Name() string {
	if p.name != "" {
		return p.name
	}
	return "mock"
}

func (p *stubProvider) CreatePayment(ctx context.Context, request service.PaymentRequest) (*service.PaymentInitiation, error) {
	p.calls.Add(1)
	p.mu.Lock()
	p.seen = append(p.seen, request)
	p.mu.Unlock()
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

func (p *stubProvider) lastRequest() (service.PaymentRequest, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.seen) == 0 {
		return service.PaymentRequest{}, false
	}
	return p.seen[len(p.seen)-1], true
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

func (fixture *checkoutFixture) service(provider *stubProvider) *service.CheckoutService {
	return service.NewCheckoutService(fixture.store, provider, 15*time.Minute)
}

func (fixture *checkoutFixture) input(userID, key, voucher string) service.CheckoutInput {
	return service.CheckoutInput{
		UserID: userID, IdempotencyKey: key, VoucherCode: voucher,
		Items: []service.CheckoutItemInput{{ProductPriceID: fixture.price, Quantity: 1}},
	}
}

func countRows(t *testing.T, db *gorm.DB, model any) int64 {
	t.Helper()
	var count int64
	if err := db.Model(model).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
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
	assertCheckoutMutationCounts(t, fixture.db, 0, 0, 3)
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
	assertCheckoutMutationCounts(t, fixture.db, 0, 0, 3)
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

func TestSameUserRetryReturnsStoredResultWithoutProviderCall(t *testing.T) {
	fixture := newCheckoutFixture(t)
	provider := &stubProvider{}
	svc := fixture.service(provider)

	first, err := svc.Checkout(context.Background(), fixture.input(fixture.userA, "fixture-retry-0001", "WELCOME10"))
	if err != nil {
		t.Fatalf("first checkout: %v", err)
	}
	second, err := svc.Checkout(context.Background(), fixture.input(fixture.userA, "fixture-retry-0001", "WELCOME10"))
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !second.Replayed {
		t.Fatal("retry was not marked as a replay")
	}
	if second.Order.ID != first.Order.ID || second.Payment.ID != first.Payment.ID {
		t.Fatalf("retry returned different logical checkout: %#v / %#v", second, first)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls.Load())
	}
	if orders := countRows(t, fixture.db, &domain.Order{}); orders != 1 {
		t.Fatalf("orders = %d, want 1", orders)
	}
	if payments := countRows(t, fixture.db, &domain.Payment{}); payments != 1 {
		t.Fatalf("payments = %d, want 1", payments)
	}
	if reserved := countReservedSlots(t, fixture.db); reserved != 1 {
		t.Fatalf("reserved slots = %d, want 1", reserved)
	}
}

func TestSameUserChangedPayloadIsConflict(t *testing.T) {
	fixture := newCheckoutFixture(t)
	provider := &stubProvider{}
	svc := fixture.service(provider)
	if _, err := svc.Checkout(context.Background(), fixture.input(fixture.userA, "fixture-conflict-0001", "WELCOME10")); err != nil {
		t.Fatalf("first checkout: %v", err)
	}
	_, err := svc.Checkout(context.Background(), fixture.input(fixture.userA, "fixture-conflict-0001", ""))
	if !errors.Is(err, service.ErrIdempotencyConflict) {
		t.Fatalf("error = %v, want ErrIdempotencyConflict", err)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls.Load())
	}
	if orders := countRows(t, fixture.db, &domain.Order{}); orders != 1 {
		t.Fatalf("orders = %d, want 1", orders)
	}
}

func TestIdempotencyKeyIsOwnerScoped(t *testing.T) {
	fixture := newCheckoutFixture(t)
	provider := &stubProvider{}
	svc := fixture.service(provider)

	resultA, err := svc.Checkout(context.Background(), fixture.input(fixture.userA, "fixture-shared-key", ""))
	if err != nil {
		t.Fatalf("user A checkout: %v", err)
	}
	resultB, err := svc.Checkout(context.Background(), fixture.input(fixture.userB, "fixture-shared-key", ""))
	if err != nil {
		t.Fatalf("user B checkout: %v", err)
	}
	if resultA.Order.ID == resultB.Order.ID {
		t.Fatal("two owners received the same order")
	}
	if resultA.Order.UserID != fixture.userA || resultB.Order.UserID != fixture.userB {
		t.Fatalf("owner mismatch: %s / %s", resultA.Order.UserID, resultB.Order.UserID)
	}
	if provider.calls.Load() != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls.Load())
	}

	progressB, err := fixture.store.GetCheckoutProgress(context.Background(), fixture.userB, "fixture-shared-key")
	if err != nil {
		t.Fatal(err)
	}
	if progressB.Order == nil || progressB.Order.ID != resultB.Order.ID {
		t.Fatalf("user B resolved the wrong order: %#v", progressB)
	}
}

func TestConcurrentIdenticalRetriesCreateOneLogicalCheckout(t *testing.T) {
	fixture := newCheckoutFixture(t)
	provider := &stubProvider{}
	svc := fixture.service(provider)

	const workers = 5
	var waitGroup sync.WaitGroup
	results := make([]*service.CheckoutResult, workers)
	errs := make([]error, workers)
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func(slot int) {
			defer waitGroup.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			results[slot], errs[slot] = svc.Checkout(ctx, fixture.input(fixture.userA, "fixture-concurrent-0001", "WELCOME10"))
		}(index)
	}
	waitGroup.Wait()

	orderID := ""
	for index := range errs {
		if errs[index] != nil {
			t.Fatalf("worker %d failed: %v", index, errs[index])
		}
		if orderID == "" {
			orderID = results[index].Order.ID
		} else if results[index].Order.ID != orderID {
			t.Fatalf("worker %d returned a different order", index)
		}
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls.Load())
	}
	if orders := countRows(t, fixture.db, &domain.Order{}); orders != 1 {
		t.Fatalf("orders = %d, want 1", orders)
	}
	if payments := countRows(t, fixture.db, &domain.Payment{}); payments != 1 {
		t.Fatalf("payments = %d, want 1", payments)
	}
	if reserved := countReservedSlots(t, fixture.db); reserved != 1 {
		t.Fatalf("reserved slots = %d, want 1", reserved)
	}
}

func TestOutOfStockFailsAtomically(t *testing.T) {
	fixture := newCheckoutFixture(t)
	provider := &stubProvider{}
	svc := fixture.service(provider)
	input := fixture.input(fixture.userA, "fixture-oos-0001", "")
	input.Items = []service.CheckoutItemInput{{ProductPriceID: fixture.price, Quantity: 3}}
	if _, err := svc.Checkout(context.Background(), input); !errors.Is(err, service.ErrOutOfStock) {
		t.Fatalf("error = %v, want ErrOutOfStock", err)
	}
	if provider.calls.Load() != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls.Load())
	}
	assertCheckoutMutationCounts(t, fixture.db, 0, 0, 3)
	var attempts int64
	fixture.db.Model(&domain.CheckoutAttempt{}).Count(&attempts)
	if attempts != 0 {
		t.Fatalf("attempt rows = %d, want 0 after rollback", attempts)
	}
}

func TestExpectedSubtotalMismatchReturnsReviewableConflict(t *testing.T) {
	fixture := newCheckoutFixture(t)
	provider := &stubProvider{}
	svc := fixture.service(provider)
	input := fixture.input(fixture.userA, "fixture-price-0001", "")
	wrong := int64(150000)
	input.ExpectedSubtotal = &wrong
	_, err := svc.Checkout(context.Background(), input)
	var priceChanged *service.PriceChangedError
	if !errors.As(err, &priceChanged) {
		t.Fatalf("error = %v, want PriceChangedError", err)
	}
	if priceChanged.CurrentSubtotal != 186000 || priceChanged.Currency != "IDR" {
		t.Fatalf("price conflict = %#v", priceChanged)
	}
	assertCheckoutMutationCounts(t, fixture.db, 0, 0, 3)
	if provider.calls.Load() != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls.Load())
	}
}

func TestInterruptedProviderSuccessIsRecoveredWithoutSecondCharge(t *testing.T) {
	fixture := newCheckoutFixture(t)
	provider := &stubProvider{}
	svc := fixture.service(provider)
	first, err := svc.Checkout(context.Background(), fixture.input(fixture.userA, "fixture-interrupted-a", ""))
	if err != nil {
		t.Fatalf("first checkout: %v", err)
	}

	// Simulate a crash after the provider accepted the payment but before the
	// payment row was committed.
	if err := fixture.db.Where("order_id = ?", first.Order.ID).Delete(&domain.Payment{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&domain.CheckoutAttempt{}).
		Where("user_id = ? AND idempotency_key = ?", fixture.userA, "fixture-interrupted-a").
		Updates(map[string]any{
			"state": "provider_created", "provider": "mock",
			"provider_payment_id": "pay_" + first.Order.ID,
			"checkout_url":        "https://pay.mock/" + first.Order.ID,
		}).Error; err != nil {
		t.Fatal(err)
	}

	recovered, err := svc.Checkout(context.Background(), fixture.input(fixture.userA, "fixture-interrupted-a", ""))
	if err != nil {
		t.Fatalf("recovery checkout: %v", err)
	}
	if !recovered.Replayed {
		t.Fatal("recovered checkout was not marked as replay")
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1 (no second charge)", provider.calls.Load())
	}
	if payments := countRows(t, fixture.db, &domain.Payment{}); payments != 1 {
		t.Fatalf("payments = %d, want 1", payments)
	}
}

func TestInterruptedReservationIsReconciledByRetry(t *testing.T) {
	fixture := newCheckoutFixture(t)
	provider := &stubProvider{}
	svc := fixture.service(provider)
	first, err := svc.Checkout(context.Background(), fixture.input(fixture.userA, "fixture-interrupted-b", ""))
	if err != nil {
		t.Fatalf("first checkout: %v", err)
	}

	// Simulate a crash after the reservation but before the provider call. The
	// attempt is stale beyond the recovery lease, so the retry may take over.
	if err := fixture.db.Where("order_id = ?", first.Order.ID).Delete(&domain.Payment{}).Error; err != nil {
		t.Fatal(err)
	}
	stale := time.Now().UTC().Add(-2 * time.Minute)
	if err := fixture.db.Model(&domain.CheckoutAttempt{}).
		Where("user_id = ? AND idempotency_key = ?", fixture.userA, "fixture-interrupted-b").
		Updates(map[string]any{"state": "reserved", "updated_at": stale, "provider": nil, "provider_payment_id": nil, "checkout_url": nil}).Error; err != nil {
		t.Fatal(err)
	}

	recovered, err := svc.Checkout(context.Background(), fixture.input(fixture.userA, "fixture-interrupted-b", ""))
	if err != nil {
		t.Fatalf("recovery checkout: %v", err)
	}
	if recovered.Order.ID != first.Order.ID {
		t.Fatalf("recovery created a new order: %s != %s", recovered.Order.ID, first.Order.ID)
	}
	if provider.calls.Load() != 2 {
		t.Fatalf("provider calls = %d, want 2 (first attempt + recovered attempt)", provider.calls.Load())
	}
	if payments := countRows(t, fixture.db, &domain.Payment{}); payments != 1 {
		t.Fatalf("payments = %d, want 1", payments)
	}
}

func TestCancelAndReleaseNeverCancelsPaidOrder(t *testing.T) {
	fixture := newCheckoutFixture(t)
	result, err := fixture.checkout(t, fixture.userA, "fixture-paid-cancel", "")
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if err := fixture.db.Model(&domain.Order{}).Where("id = ?", result.Order.ID).Update("status", domain.OrderPaid).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&domain.Payment{}).Where("order_id = ?", result.Order.ID).Update("status", "paid").Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.CancelAndRelease(context.Background(), result.Order.ID, time.Now().UTC()); err != nil {
		t.Fatalf("cancel paid order: %v", err)
	}
	var order domain.Order
	if err := fixture.db.Where("id = ?", result.Order.ID).First(&order).Error; err != nil {
		t.Fatal(err)
	}
	if order.Status != domain.OrderPaid {
		t.Fatalf("paid order status = %s, want paid", order.Status)
	}
	if reserved := countReservedSlots(t, fixture.db); reserved != 1 {
		t.Fatalf("reserved slots = %d, want 1 (stock must stay allocated)", reserved)
	}
}

func countReservedSlots(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	if err := db.Model(&domain.InventorySlot{}).Where("status = ?", "reserved").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func TestReplayBodyIsByteIdenticalToFirstResponse(t *testing.T) {
	fixture := newCheckoutFixture(t)
	provider := &stubProvider{}
	svc := fixture.service(provider)
	first, err := svc.Checkout(context.Background(), fixture.input(fixture.userA, "fixture-body-0001", "WELCOME10"))
	if err != nil {
		t.Fatalf("first checkout: %v", err)
	}
	second, err := svc.Checkout(context.Background(), fixture.input(fixture.userA, "fixture-body-0001", "WELCOME10"))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	firstBody, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBody, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBody, secondBody) {
		t.Fatalf("replay body differs:\nfirst:  %s\nsecond: %s", firstBody, secondBody)
	}
	if strings.Contains(string(firstBody), `"payments"`) {
		t.Fatalf("order.payments leaked into the checkout body: %s", firstBody)
	}
}

func TestCheckoutItemsHaveDeterministicOrder(t *testing.T) {
	fixture := newCheckoutFixture(t)
	provider := &stubProvider{}
	svc := fixture.service(provider)
	input := fixture.input(fixture.userA, "fixture-multi-item", "")
	input.Items = []service.CheckoutItemInput{
		{ProductPriceID: fixture.priceB, Quantity: 1},
		{ProductPriceID: fixture.price, Quantity: 1},
	}
	result, err := svc.Checkout(context.Background(), input)
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if len(result.Order.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(result.Order.Items))
	}
	if result.Order.Items[0].ProductPriceID > result.Order.Items[1].ProductPriceID {
		t.Fatalf("items not ordered by price id: %s, %s", result.Order.Items[0].ProductPriceID, result.Order.Items[1].ProductPriceID)
	}
}

func TestCancelAndReleaseWaitsForConcurrentPaidUpdate(t *testing.T) {
	fixture := newCheckoutFixture(t)
	result, err := fixture.checkout(t, fixture.userA, "fixture-paid-race", "")
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}

	admin := fixture.db.Begin()
	if admin.Error != nil {
		t.Fatal(admin.Error)
	}
	defer admin.Rollback()
	if err := admin.Model(&domain.Order{}).Where("id = ?", result.Order.ID).Update("status", domain.OrderPaid).Error; err != nil {
		t.Fatalf("admin update: %v", err)
	}

	cancelled := make(chan error, 1)
	go func() {
		cancelled <- fixture.store.CancelAndRelease(context.Background(), result.Order.ID, time.Now().UTC())
	}()

	// Give CancelAndRelease time to block on the row lock held by the admin
	// transaction, then let the paid update commit.
	time.Sleep(150 * time.Millisecond)
	if err := admin.Commit().Error; err != nil {
		t.Fatalf("admin commit: %v", err)
	}
	select {
	case err := <-cancelled:
		if err != nil {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CancelAndRelease did not finish")
	}

	var order domain.Order
	if err := fixture.db.Where("id = ?", result.Order.ID).First(&order).Error; err != nil {
		t.Fatal(err)
	}
	if order.Status != domain.OrderPaid {
		t.Fatalf("concurrent paid update was overwritten: status=%s", order.Status)
	}
	if reserved := countReservedSlots(t, fixture.db); reserved != 1 {
		t.Fatalf("reserved slots = %d, want 1 (paid stock must stay allocated)", reserved)
	}
}

func TestConcurrentProviderCreatedRecoveryReturnsReplay(t *testing.T) {
	fixture := newCheckoutFixture(t)
	provider := &stubProvider{}
	svc := fixture.service(provider)
	first, err := svc.Checkout(context.Background(), fixture.input(fixture.userA, "fixture-recovery-race", "WELCOME10"))
	if err != nil {
		t.Fatalf("first checkout: %v", err)
	}

	// Simulate the provider reference being recorded but the payment row never
	// committed, then hit recovery with several concurrent retries.
	if err := fixture.db.Where("order_id = ?", first.Order.ID).Delete(&domain.Payment{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&domain.CheckoutAttempt{}).
		Where("user_id = ? AND idempotency_key = ?", fixture.userA, "fixture-recovery-race").
		Updates(map[string]any{
			"state": "provider_created", "provider": "mock",
			"provider_payment_id": "pay_" + first.Order.ID,
			"checkout_url":        "https://pay.mock/" + first.Order.ID,
		}).Error; err != nil {
		t.Fatal(err)
	}

	const workers = 8
	var waitGroup sync.WaitGroup
	results := make([]*service.CheckoutResult, workers)
	errs := make([]error, workers)
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func(slot int) {
			defer waitGroup.Done()
			results[slot], errs[slot] = svc.Checkout(context.Background(), fixture.input(fixture.userA, "fixture-recovery-race", "WELCOME10"))
		}(index)
	}
	waitGroup.Wait()

	for index := range errs {
		if errs[index] != nil {
			t.Fatalf("worker %d failed: %v", index, errs[index])
		}
		if !results[index].Replayed {
			t.Fatalf("worker %d returned status 201 instead of replay", index)
		}
		if results[index].Order.ID != first.Order.ID {
			t.Fatalf("worker %d returned a different order", index)
		}
	}
	if payments := countRows(t, fixture.db, &domain.Payment{}); payments != 1 {
		t.Fatalf("payments = %d, want 1", payments)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1 (no second charge during recovery)", provider.calls.Load())
	}
}

func TestCheckoutSendsSelectedChannelToProviderAndFingerprint(t *testing.T) {
	fixture := newCheckoutFixture(t)
	provider := &stubProvider{}
	svc := fixture.service(provider)

	input := fixture.input(fixture.userA, "fixture-channel-qris", "")
	input.PaymentChannel = " QRIS "
	first, err := svc.Checkout(context.Background(), input)
	if err != nil {
		t.Fatalf("checkout with qris: %v", err)
	}
	request, ok := provider.lastRequest()
	if !ok || request.Channel != "qris" {
		t.Fatalf("provider request channel = %q, want qris", request.Channel)
	}

	replay := input
	replay.PaymentChannel = "qris"
	second, err := svc.Checkout(context.Background(), replay)
	if err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if !second.Replayed || second.Order.ID != first.Order.ID {
		t.Fatalf("replay = %#v, want stored order", second)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls after replay = %d, want 1", provider.calls.Load())
	}

	changed := input
	changed.PaymentChannel = "ovo"
	if _, err := svc.Checkout(context.Background(), changed); !errors.Is(err, service.ErrIdempotencyConflict) {
		t.Fatalf("same key with different channel error = %v, want ErrIdempotencyConflict", err)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls after conflict = %d, want 1", provider.calls.Load())
	}
}

func TestCheckoutRejectsUnknownChannelWithoutMutation(t *testing.T) {
	fixture := newCheckoutFixture(t)
	provider := &stubProvider{}
	svc := fixture.service(provider)
	input := fixture.input(fixture.userA, "fixture-channel-unknown", "")
	input.PaymentChannel = "bitcoin"

	if _, err := svc.Checkout(context.Background(), input); !errors.Is(err, service.ErrUnsupportedPaymentChannel) {
		t.Fatalf("error = %v, want ErrUnsupportedPaymentChannel", err)
	}
	if provider.calls.Load() != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls.Load())
	}
	assertCheckoutMutationCounts(t, fixture.db, 0, 0, 3)
}
