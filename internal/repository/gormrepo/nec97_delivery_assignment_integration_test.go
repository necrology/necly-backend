package gormrepo

import (
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/migrate"
	"github.com/necly/services/internal/platform/mailer"
	"github.com/necly/services/internal/platform/testdb"
	"github.com/necly/services/internal/service"
	"gorm.io/gorm"
)

// nec97Fixture seeds a real catalog with one product, one 30-day price and as
// many confirmed buyers/accounts as a test needs.
type nec97Fixture struct {
	db         *gorm.DB
	store      *Store
	productID  string
	priceID    string
	durationID string
}

func newNEC97Fixture(t *testing.T) *nec97Fixture {
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
	fixture := &nec97Fixture{
		db: db, store: NewStore(db),
		productID: uuid.NewString(), priceID: uuid.NewString(), durationID: uuid.NewString(),
	}
	categoryID := uuid.NewString()
	if err := db.Create(&domain.Category{ID: categoryID, Name: "Streaming", Slug: "stream-" + categoryID[:8], Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.Product{ID: fixture.productID, CategoryID: categoryID, Name: "Netflix Premium", Slug: "netflix-" + fixture.productID[:8], Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.ProductDuration{ID: fixture.durationID, Name: "1 Bulan", Days: 30, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.ProductPrice{ID: fixture.priceID, ProductID: fixture.productID, DurationID: fixture.durationID, Amount: 150000, Currency: "IDR", Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (f *nec97Fixture) user(t *testing.T, label string) string {
	t.Helper()
	now := time.Now().UTC()
	userID := uuid.NewString()
	verified := now
	user := &domain.User{
		ID: userID, Email: label + "-" + userID[:8] + "@example.test", Name: label,
		PasswordHash: "x", Role: domain.RoleCustomer, Active: true,
		EmailVerifiedAt: &verified, CreatedAt: now, UpdatedAt: now,
	}
	if err := f.db.Create(user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return userID
}

func (f *nec97Fixture) account(t *testing.T, label string, sharingLimit int, expiresAt *time.Time) string {
	t.Helper()
	now := time.Now().UTC()
	cipher, err := service.NewCredentialCipher(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.Encrypt([]byte(label + "-login@example.test:secret-" + label))
	if err != nil {
		t.Fatal(err)
	}
	accountID := uuid.NewString()
	if err := f.db.Create(&domain.InventoryAccount{
		ID: accountID, ProductID: f.productID, Label: label, EncryptedCredentials: sealed,
		Status: "active", SharingLimit: sharingLimit, ExpiresAt: expiresAt, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed account: %v", err)
	}
	return accountID
}

// order seeds a paid order with one item and reserved slot(s). When accountID
// is empty the slots are created on a throwaway account so the caller can test
// moving them.
func (f *nec97Fixture) order(t *testing.T, userID, accountID string, quantity int, status domain.OrderStatus) *domain.Order {
	t.Helper()
	now := time.Now().UTC()
	orderID, itemID := uuid.NewString(), uuid.NewString()
	if accountID == "" {
		accountID = f.account(t, "temp", 5, nil)
	}
	if err := f.db.Create(&domain.Order{
		ID: orderID, Number: "NEC97-" + orderID[:8], UserID: userID, Status: status,
		Subtotal: int64(quantity) * 150000, Total: int64(quantity) * 150000, Currency: "IDR",
		PaymentDueAt: now.Add(time.Hour), IdempotencyKey: "nec97-" + orderID[:8],
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed order: %v", err)
	}
	if err := f.db.Create(&domain.OrderItem{
		ID: itemID, OrderID: orderID, ProductPriceID: f.priceID, ProductName: "Netflix Premium",
		DurationName: "1 Bulan", UnitPrice: 150000, Quantity: quantity, DeviceCount: 1,
		LineTotal: int64(quantity) * 150000,
	}).Error; err != nil {
		t.Fatalf("seed order item: %v", err)
	}
	for i := 0; i < quantity; i++ {
		if err := f.db.Create(&domain.InventorySlot{
			ID: uuid.NewString(), InventoryAccountID: accountID, ProductPriceID: f.priceID,
			Status: "reserved", ReservedBy: &userID, OrderItemID: &itemID, CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatalf("seed slot: %v", err)
		}
	}
	if _, err := f.store.EnsureOrderDeliveries(context.Background(), orderID, now); err != nil {
		t.Fatalf("ensure deliveries: %v", err)
	}
	var order domain.Order
	if err := f.db.Preload("Items").Preload("Payments").Where("id = ?", orderID).First(&order).Error; err != nil {
		t.Fatal(err)
	}
	return &order
}

func (f *nec97Fixture) delivery(t *testing.T, orderID string) domain.OrderDelivery {
	t.Helper()
	rows, err := f.store.ListOrderDeliveries(context.Background(), orderID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(rows))
	}
	return rows[0]
}

// markSent makes a delivery count as a completed previous purchase.
func (f *nec97Fixture) markSent(t *testing.T, delivery domain.OrderDelivery) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	claimed, err := f.store.ClaimOrderDelivery(ctx, delivery.ID, now, now.Add(-time.Minute))
	if err != nil || !claimed {
		t.Fatalf("claim delivery = %t, %v", claimed, err)
	}
	if err := f.store.MarkOrderDeliverySent(ctx, delivery.ID, now); err != nil {
		t.Fatalf("mark sent: %v", err)
	}
}

func (f *nec97Fixture) slotAccounts(t *testing.T, orderItemID string) []string {
	t.Helper()
	var slots []domain.InventorySlot
	if err := f.db.Where("order_item_id = ? AND status <> ?", orderItemID, "released").Find(&slots).Error; err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(slots))
	for _, slot := range slots {
		out = append(out, slot.InventoryAccountID)
	}
	return out
}

func TestNEC97AssignRejectsSixthUserAndReusedAccount(t *testing.T) {
	fixture := newNEC97Fixture(t)
	ctx := context.Background()

	main := fixture.account(t, "utama", 5, nil)
	other := fixture.account(t, "cadangan", 5, nil)
	for i := 0; i < 5; i++ {
		userID := fixture.user(t, "user")
		fixture.order(t, userID, main, 1, domain.OrderProcessing)
	}
	sixthUser := fixture.user(t, "sixth")
	sixth := fixture.order(t, sixthUser, other, 1, domain.OrderProcessing)
	if _, err := fixture.store.AssignDeliveryAccount(ctx, sixth.ID, fixture.delivery(t, sixth.ID).ID, main, time.Now().UTC()); !errors.Is(err, service.ErrDeliveryAccountFull) {
		t.Fatalf("assign sixth user to full account error = %v, want ErrDeliveryAccountFull", err)
	}

	// Repeat purchase: the buyer already received "main" for this product, so
	// the API must offer "other" and reject the reused account.
	repeatUser := fixture.user(t, "repeat")
	previous := fixture.order(t, repeatUser, main, 1, domain.OrderProcessing)
	fixture.markSent(t, fixture.delivery(t, previous.ID))
	repeat := fixture.order(t, repeatUser, main, 1, domain.OrderProcessing)

	options, err := fixture.store.LoadDeliveryOptions(ctx, repeat.ID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]service.DeliveryAccountOption{}
	for _, option := range options[0].Accounts {
		seen[option.InventoryAccountID] = option
	}
	if !seen[main].UsedByCustomer {
		t.Fatal("the previously delivered account was not marked used_by_customer")
	}
	if seen[other].UsedByCustomer || !seen[other].CanAssign {
		t.Fatalf("unused account option = %#v, want assignable and unused", seen[other])
	}
	if _, err := fixture.store.AssignDeliveryAccount(ctx, repeat.ID, fixture.delivery(t, repeat.ID).ID, main, time.Now().UTC()); !errors.Is(err, service.ErrAccountUsedByCustomer) {
		t.Fatalf("reused manual assignment error = %v, want ErrAccountUsedByCustomer", err)
	}
	delivery, err := fixture.store.AssignDeliveryAccount(ctx, repeat.ID, fixture.delivery(t, repeat.ID).ID, other, time.Now().UTC())
	if err != nil {
		t.Fatalf("assign alternative: %v", err)
	}
	for _, accountID := range fixture.slotAccounts(t, delivery.OrderItemID) {
		if accountID != other {
			t.Fatalf("slot account = %s, want the selected alternative %s", accountID, other)
		}
	}

	// Once both accounts have been used, there is no alternative left.
	fixture.markSent(t, *delivery)
	third := fixture.order(t, repeatUser, main, 1, domain.OrderProcessing)
	if _, err := fixture.store.AssignDeliveryAccount(ctx, third.ID, fixture.delivery(t, third.ID).ID, main, time.Now().UTC()); !errors.Is(err, service.ErrNoAlternativeAccount) {
		t.Fatalf("all-used assignment error = %v, want ErrNoAlternativeAccount", err)
	}
}

func TestNEC97PrepareDeliveryAssignmentMovesAwayFromReusedAccount(t *testing.T) {
	fixture := newNEC97Fixture(t)
	ctx := context.Background()
	userID := fixture.user(t, "auto")
	first := fixture.account(t, "pertama", 5, nil)
	second := fixture.account(t, "kedua", 5, nil)

	previous := fixture.order(t, userID, first, 1, domain.OrderProcessing)
	fixture.markSent(t, fixture.delivery(t, previous.ID))

	current := fixture.order(t, userID, first, 1, domain.OrderProcessing)
	if err := fixture.store.PrepareDeliveryAssignment(ctx, current.ID, current.Items[0].ID, time.Now().UTC()); err != nil {
		t.Fatalf("prepare assignment: %v", err)
	}
	for _, accountID := range fixture.slotAccounts(t, current.Items[0].ID) {
		if accountID != second {
			t.Fatalf("auto-selected account = %s, want %s", accountID, second)
		}
	}

	// Make the second account count as used too, then the next purchase must
	// fail closed instead of silently reusing either account.
	fixture.markSent(t, fixture.delivery(t, current.ID))
	third := fixture.order(t, userID, first, 1, domain.OrderProcessing)
	if err := fixture.store.PrepareDeliveryAssignment(ctx, third.ID, third.Items[0].ID, time.Now().UTC()); !errors.Is(err, service.ErrNoAlternativeAccount) {
		t.Fatalf("prepare with no alternative = %v, want ErrNoAlternativeAccount", err)
	}
}

type nec97Mailer struct {
	mu   sync.Mutex
	sent []mailer.Message
}

func (m *nec97Mailer) Enabled() bool { return true }
func (m *nec97Mailer) Send(_ context.Context, msg mailer.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}
func (m *nec97Mailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

func TestNEC97DeliveryAutoSelectsDifferentAccountAndFailsClosed(t *testing.T) {
	fixture := newNEC97Fixture(t)
	cipher, err := service.NewCredentialCipher(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	if err != nil {
		t.Fatal(err)
	}
	mail := &nec97Mailer{}
	svc := service.NewOrderDeliveryService(fixture.store, fixture.store, cipher, mail, fixture.store)
	actor := service.AdminActor{UserID: fixture.user(t, "admin")}
	userID := fixture.user(t, "mailbuyer")
	first := fixture.account(t, "mail-one", 5, nil)
	second := fixture.account(t, "mail-two", 5, nil)

	orderOne := fixture.order(t, userID, first, 1, domain.OrderProcessing)
	if _, err := svc.DeliverApprovedOrder(context.Background(), actor, orderOne.ID); err != nil {
		t.Fatal(err)
	}
	if mail.count() != 1 {
		t.Fatalf("first delivery emails = %d, want 1", mail.count())
	}

	orderTwo := fixture.order(t, userID, first, 1, domain.OrderProcessing)
	deliveries, err := svc.DeliverApprovedOrder(context.Background(), actor, orderTwo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 || deliveries[0].Status != domain.DeliverySent {
		t.Fatalf("second delivery = %#v, want sent", deliveries)
	}
	if mail.count() != 2 {
		t.Fatalf("second delivery emails = %d, want 2", mail.count())
	}
	if strings.Contains(mail.sent[1].Body, "secret-mail-one") {
		t.Fatal("repeat purchase reused the first account")
	}
	if !strings.Contains(mail.sent[1].Body, "secret-mail-two") {
		t.Fatalf("second email did not use the unused account:\n%s", mail.sent[1].Body)
	}
	var sentSlots []domain.InventorySlot
	if err := fixture.db.Where("order_item_id = ? AND status <> ?", orderTwo.Items[0].ID, "released").Find(&sentSlots).Error; err != nil {
		t.Fatal(err)
	}
	if len(sentSlots) != 1 || sentSlots[0].ActivatedAt == nil || sentSlots[0].AccessExpiresAt == nil {
		t.Fatalf("delivered slot has no access window: %#v", sentSlots)
	}
	if !sentSlots[0].AccessExpiresAt.After(time.Now().UTC().Add(29 * 24 * time.Hour)) {
		t.Fatalf("access window = %s, want about 30 days", sentSlots[0].AccessExpiresAt)
	}

	// Only used accounts remain for this buyer: the third purchase must be
	// recorded as no_alternative_account without sending another email.
	thirdUser := fixture.user(t, "nomore")
	firstUsed := fixture.order(t, thirdUser, first, 1, domain.OrderProcessing)
	fixture.markSent(t, fixture.delivery(t, firstUsed.ID))
	secondUsed := fixture.order(t, thirdUser, second, 1, domain.OrderProcessing)
	fixture.markSent(t, fixture.delivery(t, secondUsed.ID))
	orderThree := fixture.order(t, thirdUser, first, 1, domain.OrderProcessing)
	deliveries, err = svc.DeliverApprovedOrder(context.Background(), actor, orderThree.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 || deliveries[0].Status != domain.DeliveryFailed || deliveries[0].ErrorCode == nil || *deliveries[0].ErrorCode != service.DeliveryErrorNoAlternativeAccount {
		t.Fatalf("no-alternative delivery = %#v", deliveries)
	}
	if mail.count() != 2 {
		t.Fatalf("no-alternative delivery sent email; count = %d", mail.count())
	}
}

func TestNEC97ServiceReplaceSendsOneEmailAndKeepsOrder(t *testing.T) {
	fixture := newNEC97Fixture(t)
	ctx := context.Background()
	cipher, err := service.NewCredentialCipher(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	if err != nil {
		t.Fatal(err)
	}
	mail := &nec97Mailer{}
	svc := service.NewOrderDeliveryService(fixture.store, fixture.store, cipher, mail, fixture.store)
	actor := service.AdminActor{UserID: fixture.user(t, "admin")}
	userID := fixture.user(t, "replace-mail")
	oldAccount := fixture.account(t, "replace-old", 5, nil)
	newAccount := fixture.account(t, "replace-new", 5, nil)
	order := fixture.order(t, userID, oldAccount, 1, domain.OrderProcessing)
	delivery := fixture.delivery(t, order.ID)
	fixture.markSent(t, delivery)
	past := time.Now().UTC().Add(-24 * time.Hour)
	if err := fixture.db.Model(&domain.InventorySlot{}).
		Where("order_item_id = ?", order.Items[0].ID).
		Updates(map[string]any{"activated_at": past.Add(-30 * 24 * time.Hour), "access_expires_at": past}).Error; err != nil {
		t.Fatal(err)
	}

	replaced, err := svc.ReplaceAccount(ctx, actor, order.ID, delivery.ID, newAccount, "akun lama expired", false)
	if err != nil {
		t.Fatalf("ReplaceAccount: %v", err)
	}
	if mail.count() != 1 {
		t.Fatalf("replacement emails = %d, want 1", mail.count())
	}
	if !strings.Contains(mail.sent[0].Body, "secret-replace-new") || !strings.Contains(mail.sent[0].Body, "Masa akses") {
		t.Fatalf("replacement email missing new account/access window:\n%s", mail.sent[0].Body)
	}
	if len(replaced.Replacements) != 1 || replaced.Replacements[0].Status != "sent" || replaced.Replacements[0].EmailSentAt == nil {
		t.Fatalf("replacement audit = %#v", replaced.Replacements)
	}
	if len(replaced.AssignedAccounts) != 1 || replaced.AssignedAccounts[0].InventoryAccountID != newAccount {
		t.Fatalf("assigned accounts = %#v", replaced.AssignedAccounts)
	}

	if _, err := svc.ReplaceAccount(ctx, actor, order.ID, delivery.ID, oldAccount, "coba lagi", false); !errors.Is(err, service.ErrAccountNotExpired) {
		t.Fatalf("second replacement error = %v, want ErrAccountNotExpired", err)
	}
	if mail.count() != 1 {
		t.Fatalf("second replacement sent another email; count = %d", mail.count())
	}
}

func TestNEC97ReplaceExpiredAccountMovesAccessAndKeepsOrder(t *testing.T) {
	fixture := newNEC97Fixture(t)
	ctx := context.Background()
	userID := fixture.user(t, "replace")
	oldAccount := fixture.account(t, "lama", 5, nil)
	newAccount := fixture.account(t, "baru", 5, nil)
	order := fixture.order(t, userID, oldAccount, 1, domain.OrderProcessing)
	delivery := fixture.delivery(t, order.ID)
	fixture.markSent(t, delivery)

	past := time.Now().UTC().Add(-24 * time.Hour)
	if err := fixture.db.Model(&domain.InventorySlot{}).
		Where("order_item_id = ?", order.Items[0].ID).
		Updates(map[string]any{"activated_at": past.Add(-30 * 24 * time.Hour), "access_expires_at": past}).Error; err != nil {
		t.Fatal(err)
	}
	// The newly assigned access window starts now, so a second replacement is
	// rejected as account_not_expired.
	replacement, err := fixture.store.ReplaceDeliveryAccount(ctx, order.ID, delivery.ID, newAccount, "akun lama expired", false, fixture.user(t, "admin"), time.Now().UTC())
	if err != nil {
		t.Fatalf("replace account: %v", err)
	}
	if replacement == nil || replacement.InventoryAccountID != newAccount || len(replacement.OldSlotIDs) != 1 || len(replacement.NewSlotIDs) != 1 {
		t.Fatalf("replacement = %#v", replacement)
	}
	if _, err := fixture.store.ReplaceDeliveryAccount(ctx, order.ID, delivery.ID, oldAccount, "coba lagi", false, fixture.user(t, "admin"), time.Now().UTC()); !errors.Is(err, service.ErrAccountNotExpired) {
		t.Fatalf("second replacement error = %v, want ErrAccountNotExpired", err)
	}

	var oldSlots []domain.InventorySlot
	if err := fixture.db.Where("id IN ?", []string(replacement.OldSlotIDs)).Find(&oldSlots).Error; err != nil {
		t.Fatal(err)
	}
	if len(oldSlots) != 1 || oldSlots[0].Status != "released" || oldSlots[0].OrderItemID != nil {
		t.Fatalf("old slot = %#v, want released and detached", oldSlots)
	}
	var newSlots []domain.InventorySlot
	if err := fixture.db.Where("order_item_id = ?", order.Items[0].ID).Find(&newSlots).Error; err != nil {
		t.Fatal(err)
	}
	if len(newSlots) != 1 || newSlots[0].InventoryAccountID != newAccount {
		t.Fatalf("new slot = %#v", newSlots)
	}
	if newSlots[0].ActivatedAt == nil || newSlots[0].AccessExpiresAt == nil || !newSlots[0].AccessExpiresAt.After(time.Now().UTC().Add(29*24*time.Hour)) {
		t.Fatalf("new access window = %#v", newSlots[0])
	}
	var orders, payments int64
	fixture.db.Model(&domain.Order{}).Where("user_id = ?", userID).Count(&orders)
	fixture.db.Model(&domain.Payment{}).Where("order_id = ?", order.ID).Count(&payments)
	if orders != 1 || payments != 0 {
		t.Fatalf("replacement created order/payment rows: orders=%d payments=%d", orders, payments)
	}
}
