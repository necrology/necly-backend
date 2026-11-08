package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/platform/mailer"
)

type deliveryFakeMail struct {
	enabled   bool
	err       error
	sent      []mailer.Message
	attempted []mailer.Message
}

func (m *deliveryFakeMail) Send(_ context.Context, msg mailer.Message) error {
	m.attempted = append(m.attempted, msg)
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, msg)
	return nil
}

func (m *deliveryFakeMail) Enabled() bool { return m.enabled }

type deliveryFakeRepo struct {
	order             *domain.Order
	deliveries        []domain.OrderDelivery
	material          map[string]*DeliveryMaterial
	statusTransitions []domain.OrderStatus
}

func (r *deliveryFakeRepo) ListOrders(context.Context, OrderFilter, int, int) ([]domain.Order, int64, error) {
	return nil, 0, nil
}

func (r *deliveryFakeRepo) GetOrderByID(_ context.Context, orderID string) (*domain.Order, error) {
	if r.order == nil || r.order.ID != orderID {
		return nil, ErrNotFound
	}
	copyOrder := *r.order
	copyOrder.Items = append([]domain.OrderItem(nil), r.order.Items...)
	return &copyOrder, nil
}

func (r *deliveryFakeRepo) TransitionOrderStatus(_ context.Context, orderID string, status domain.OrderStatus, _ time.Time) (*domain.Order, error) {
	if r.order == nil || r.order.ID != orderID {
		return nil, ErrNotFound
	}
	r.statusTransitions = append(r.statusTransitions, status)
	r.order.Status = status
	return r.order, nil
}

func (r *deliveryFakeRepo) UpdateOrder(context.Context, *domain.Order) error { return nil }

func (r *deliveryFakeRepo) RevocationImpact(context.Context, string) ([]RevocationAccountImpact, error) {
	return nil, nil
}

func (r *deliveryFakeRepo) ReleaseOrderSlots(context.Context, string, time.Time) (int, error) {
	return 0, nil
}

func (r *deliveryFakeRepo) EnsureOrderDeliveries(_ context.Context, orderID string, now time.Time) ([]domain.OrderDelivery, error) {
	for _, item := range r.order.Items {
		found := false
		for _, delivery := range r.deliveries {
			if delivery.OrderItemID == item.ID {
				found = true
				break
			}
		}
		if !found {
			r.deliveries = append(r.deliveries, domain.OrderDelivery{
				ID: uuid.NewString(), OrderID: orderID, OrderItemID: item.ID,
				Status: domain.DeliveryPending, CreatedAt: now, UpdatedAt: now,
			})
		}
	}
	return r.ListOrderDeliveries(context.Background(), orderID)
}

func (r *deliveryFakeRepo) ListOrderDeliveries(_ context.Context, orderID string) ([]domain.OrderDelivery, error) {
	out := make([]domain.OrderDelivery, 0, len(r.deliveries))
	for _, delivery := range r.deliveries {
		if delivery.OrderID == orderID {
			out = append(out, delivery)
		}
	}
	return out, nil
}

func (r *deliveryFakeRepo) GetOrderDelivery(_ context.Context, orderID, deliveryID string) (*domain.OrderDelivery, error) {
	for i := range r.deliveries {
		if r.deliveries[i].ID == deliveryID && r.deliveries[i].OrderID == orderID {
			copyDelivery := r.deliveries[i]
			return &copyDelivery, nil
		}
	}
	return nil, ErrDeliveryNotFound
}

func (r *deliveryFakeRepo) LoadDeliveryMaterial(_ context.Context, orderID, orderItemID string) (*DeliveryMaterial, error) {
	if material, ok := r.material[orderItemID]; ok {
		return material, nil
	}
	return &DeliveryMaterial{OrderItemID: orderItemID}, nil
}

func (r *deliveryFakeRepo) LoadDeliveryOptions(context.Context, string) ([]DeliveryItemOptions, error) {
	return nil, nil
}

func (r *deliveryFakeRepo) PrepareDeliveryAssignment(context.Context, string, string, time.Time) error {
	return nil
}

func (r *deliveryFakeRepo) AssignDeliveryAccount(context.Context, string, string, string, time.Time) (*domain.OrderDelivery, error) {
	return nil, ErrConflict
}

func (r *deliveryFakeRepo) ReplaceDeliveryAccount(context.Context, string, string, string, string, bool, string, time.Time) (*domain.DeliveryReplacement, error) {
	return nil, ErrConflict
}

func (r *deliveryFakeRepo) MarkDeliveryReplacementSent(context.Context, string, time.Time) error {
	return nil
}

func (r *deliveryFakeRepo) MarkDeliveryReplacementFailed(context.Context, string, string, time.Time) error {
	return nil
}

func (r *deliveryFakeRepo) ActivateDeliveryAccess(context.Context, string, string, time.Time, time.Time) error {
	return nil
}

func (r *deliveryFakeRepo) ClaimOrderDelivery(_ context.Context, deliveryID string, now, staleBefore time.Time) (bool, error) {
	for i := range r.deliveries {
		delivery := &r.deliveries[i]
		if delivery.ID != deliveryID {
			continue
		}
		open := delivery.Status == domain.DeliveryPending || delivery.Status == domain.DeliveryFailed
		stale := delivery.Status == domain.DeliverySending && delivery.UpdatedAt.Before(staleBefore)
		if !open && !stale {
			return false, nil
		}
		delivery.Status = domain.DeliverySending
		delivery.Attempts++
		delivery.LastAttemptAt = &now
		delivery.UpdatedAt = now
		delivery.ErrorCode = nil
		return true, nil
	}
	return false, nil
}

func (r *deliveryFakeRepo) MarkOrderDeliverySent(_ context.Context, deliveryID string, now time.Time) error {
	for i := range r.deliveries {
		if r.deliveries[i].ID == deliveryID && r.deliveries[i].Status == domain.DeliverySending {
			r.deliveries[i].Status = domain.DeliverySent
			r.deliveries[i].SentAt = &now
			r.deliveries[i].UpdatedAt = now
			r.deliveries[i].ErrorCode = nil
			return nil
		}
	}
	return ErrConflict
}

func (r *deliveryFakeRepo) MarkOrderDeliveryFailed(_ context.Context, deliveryID, code string, now time.Time) error {
	for i := range r.deliveries {
		if r.deliveries[i].ID == deliveryID && r.deliveries[i].Status == domain.DeliverySending {
			r.deliveries[i].Status = domain.DeliveryFailed
			r.deliveries[i].ErrorCode = &code
			r.deliveries[i].UpdatedAt = now
			return nil
		}
	}
	return ErrConflict
}

func newDeliveryCipher(t *testing.T) *CredentialCipher {
	t.Helper()
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cipher, err := NewCredentialCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

type deliveryFixture struct {
	repo    *deliveryFakeRepo
	mail    *deliveryFakeMail
	audit   *fakeAudit
	cipher  *CredentialCipher
	service *OrderDeliveryService
	order   *domain.Order
	itemID  string
	account *domain.InventoryAccount
}

func newDeliveryFixture(t *testing.T, verified bool, credential string) *deliveryFixture {
	t.Helper()
	now := time.Now().UTC()
	cipher := newDeliveryCipher(t)
	sealed, err := cipher.Encrypt([]byte(credential))
	if err != nil {
		t.Fatal(err)
	}
	accountID := uuid.NewString()
	itemID := uuid.NewString()
	verifiedAt := &now
	if !verified {
		verifiedAt = nil
	}
	order := &domain.Order{
		ID: uuid.NewString(), Number: "ORD-DELIVERY-1", UserID: uuid.NewString(),
		Status: domain.OrderProcessing, Total: 150000, Currency: "IDR",
		Customer: &domain.User{
			ID: uuid.NewString(), Email: "buyer@example.test", Name: "Buyer",
			Role: domain.RoleCustomer, Active: true, EmailVerifiedAt: verifiedAt,
			CreatedAt: now, UpdatedAt: now,
		},
		Items: []domain.OrderItem{{
			ID: itemID, OrderID: "", ProductName: "Netflix Premium", DurationName: "1 Bulan",
			Quantity: 2, UnitPrice: 75000, LineTotal: 150000,
		}},
		CreatedAt: now, UpdatedAt: now,
	}
	repo := &deliveryFakeRepo{
		order: order,
		material: map[string]*DeliveryMaterial{itemID: {
			OrderItemID:  itemID,
			DurationDays: 30,
			Slots: []DeliverySlot{
				{SlotID: uuid.NewString(), InventoryAccountID: accountID, AccountLabel: "Akun utama", AccountStatus: "active", EncryptedCredentials: sealed},
				{SlotID: uuid.NewString(), InventoryAccountID: accountID, AccountLabel: "Akun utama", AccountStatus: "active", EncryptedCredentials: sealed},
			},
		}},
	}
	mail := &deliveryFakeMail{enabled: true}
	audit := &fakeAudit{}
	service := NewOrderDeliveryService(repo, repo, cipher, mail, audit)
	service.now = func() time.Time { return now }
	service.SetCentralOTPInboxReady(true)
	return &deliveryFixture{
		repo: repo, mail: mail, audit: audit, cipher: cipher, service: service,
		order: order, itemID: itemID,
	}
}

func deliveryStatus(t *testing.T, fixture *deliveryFixture) domain.OrderDelivery {
	t.Helper()
	deliveries, err := fixture.repo.ListOrderDeliveries(context.Background(), fixture.order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(deliveries))
	}
	return deliveries[0]
}

func TestDeliverApprovedOrderSendsOneEmailPerItemWithCredentials(t *testing.T) {
	fixture := newDeliveryFixture(t, true, `{"username":"buyer-account@example.test","password":"rahasia-123","instructions":"Jangan logout dari perangkat utama."}`)

	deliveries, err := fixture.service.DeliverApprovedOrder(context.Background(), AdminActor{UserID: "admin-1"}, fixture.order.ID)
	if err != nil {
		t.Fatalf("DeliverApprovedOrder() error = %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].Status != domain.DeliverySent {
		t.Fatalf("deliveries = %#v, want one sent", deliveries)
	}
	if len(fixture.mail.sent) != 1 {
		t.Fatalf("sent emails = %d, want exactly 1", len(fixture.mail.sent))
	}
	message := fixture.mail.sent[0]
	if message.To != "buyer@example.test" {
		t.Errorf("recipient = %q", message.To)
	}
	for _, want := range []string{"ORD-DELIVERY-1", "Netflix Premium", "1 Bulan", "buyer-account@example.test", "rahasia-123", "Jangan logout", "Cara penggunaan"} {
		if !strings.Contains(message.Body, want) {
			t.Errorf("email body missing %q:\n%s", want, message.Body)
		}
	}
	if !fixture.audit.has(AuditOrderDeliverySent) {
		t.Error("sent delivery was not audited")
	}
	if strings.Contains(fixture.audit.entries[0].Metadata["order_id"].(string), "rahasia-123") {
		t.Fatal("audit metadata leaked a credential")
	}
}

func TestDeliverApprovedOrderSupportsLegacyUsernameColonPassword(t *testing.T) {
	fixture := newDeliveryFixture(t, true, "induk@necly.id:rahasia")

	if _, err := fixture.service.DeliverApprovedOrder(context.Background(), AdminActor{}, fixture.order.ID); err != nil {
		t.Fatalf("DeliverApprovedOrder() error = %v", err)
	}
	if len(fixture.mail.sent) != 1 {
		t.Fatalf("sent emails = %d, want 1", len(fixture.mail.sent))
	}
	if !strings.Contains(fixture.mail.sent[0].Body, "induk@necly.id") || !strings.Contains(fixture.mail.sent[0].Body, "Kata sandi     : rahasia") {
		t.Fatalf("email did not split the legacy credential:\n%s", fixture.mail.sent[0].Body)
	}
}

func TestDeliverApprovedOrderMovesTrackingToAccessReceived(t *testing.T) {
	fixture := newDeliveryFixture(t, true, "user:secret")

	if _, err := fixture.service.DeliverApprovedOrder(context.Background(), AdminActor{}, fixture.order.ID); err != nil {
		t.Fatalf("DeliverApprovedOrder() error = %v", err)
	}

	if fixture.order.Status != domain.OrderDelivered {
		t.Fatalf("order status = %q, ingin %q", fixture.order.Status, domain.OrderDelivered)
	}
	if len(fixture.repo.statusTransitions) != 2 || fixture.repo.statusTransitions[0] != domain.OrderShipped || fixture.repo.statusTransitions[1] != domain.OrderDelivered {
		t.Fatalf("status transitions = %#v, ingin [shipped delivered]", fixture.repo.statusTransitions)
	}
}

func TestDeliverApprovedOrderUsesComplaintPageAndNoReplyWording(t *testing.T) {
	fixture := newDeliveryFixture(t, true, "user:secret")
	fixture.service.SetAccessCodeSupport(newFakeAccessStore(), "https://necly.example")

	if _, err := fixture.service.DeliverApprovedOrder(context.Background(), AdminActor{}, fixture.order.ID); err != nil {
		t.Fatalf("DeliverApprovedOrder() error = %v", err)
	}
	body := fixture.mail.sent[0].Body
	if strings.Contains(body, "balas email ini") {
		t.Fatalf("email masih meminta balasan:\n%s", body)
	}
	if !strings.Contains(body, "https://necly.example/laporan-kendala") {
		t.Fatalf("email tidak memuat tautan laporan kendala:\n%s", body)
	}
	if !strings.Contains(body, "Email ini tidak menerima balasan") {
		t.Fatalf("email tidak menjelaskan larangan balasan:\n%s", body)
	}
	for _, want := range []string{
		"https://necly.example/otp",
		"KODE AKSES OTP",
		"Berlaku        : 5 menit",
		"berbeda dari kata sandi akun dan berbeda dari OTP platform",
		"kode akses OTP 5 menit yang tercantum di email ini",
		"Tunggu beberapa saat sampai kode OTP terbaru",
		"Kembali ke platform dan masukkan kode OTP",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("email tidak memuat langkah OTP %q:\n%s", want, body)
		}
	}
}

func TestDeliverApprovedOrderExplainsChatGPTPasswordBeforeOptionalOTP(t *testing.T) {
	fixture := newDeliveryFixture(t, true, "chatgpt-account@example.test:account-password")
	fixture.order.Items[0].ProductName = "ChatGPT Plus"
	fixture.service.SetAccessCodeSupport(newFakeAccessStore(), "https://necly.example")

	if _, err := fixture.service.DeliverApprovedOrder(context.Background(), AdminActor{}, fixture.order.ID); err != nil {
		t.Fatalf("DeliverApprovedOrder() error = %v", err)
	}
	body := fixture.mail.sent[0].Body
	passwordStep := "masukkan email akun di atas, lalu lanjutkan dengan kata sandi akun di atas"
	optionalOTPStep := "Ambil OTP hanya jika ChatGPT benar-benar menampilkan permintaan kode verifikasi setelah langkah kata sandi"
	passwordIndex := strings.Index(body, passwordStep)
	otpIndex := strings.Index(body, optionalOTPStep)
	if passwordIndex < 0 || otpIndex < 0 {
		t.Fatalf("instruksi ChatGPT tidak lengkap:\n%s", body)
	}
	if passwordIndex >= otpIndex {
		t.Fatalf("instruksi OTP mendahului password:\n%s", body)
	}
}

func TestDeliverApprovedOrderAutomaticallyStoresOneFiveMinuteCode(t *testing.T) {
	fixture := newDeliveryFixture(t, true, "family@example.test:secret")
	store := newFakeAccessStore()
	fixture.service.SetAccessCodeSupport(store, "https://necly.example")

	if _, err := fixture.service.DeliverApprovedOrder(context.Background(), AdminActor{}, fixture.order.ID); err != nil {
		t.Fatalf("DeliverApprovedOrder() error = %v", err)
	}
	if len(store.data) != 1 || store.ttl != 5*time.Minute {
		t.Fatalf("stored codes = %d ttl = %s, want one 5-minute code", len(store.data), store.ttl)
	}
	if len(store.statuses) != 1 {
		t.Fatalf("scope statuses = %d, want 1", len(store.statuses))
	}
	body := fixture.mail.sent[0].Body
	var code string
	for key := range store.data {
		for _, candidate := range strings.Fields(body) {
			if len(candidate) == 10 && HashAccessCode(candidate) == key {
				code = candidate
				break
			}
		}
	}
	if code == "" {
		t.Fatalf("delivery email did not contain the hashed access code:\n%s", body)
	}
	if strings.Contains(string(store.data[HashAccessCode(code)]), code) {
		t.Fatal("Redis payload persisted the plaintext access code")
	}

	if _, err := fixture.service.DeliverApprovedOrder(context.Background(), AdminActor{}, fixture.order.ID); err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if len(fixture.mail.sent) != 1 || len(store.data) != 1 {
		t.Fatalf("replay sent %d emails and left %d codes", len(fixture.mail.sent), len(store.data))
	}
}

func TestDeliverApprovedOrderRedisFailureIsRetryable(t *testing.T) {
	fixture := newDeliveryFixture(t, true, "family@example.test:secret")
	store := newFakeAccessStore()
	store.err = errors.New("redis unavailable")
	fixture.service.SetAccessCodeSupport(store, "https://necly.example")

	if _, err := fixture.service.DeliverApprovedOrder(context.Background(), AdminActor{}, fixture.order.ID); err != nil {
		t.Fatalf("DeliverApprovedOrder() error = %v", err)
	}
	failed := deliveryStatus(t, fixture)
	if failed.Status != domain.DeliveryFailed || failed.ErrorCode == nil || *failed.ErrorCode != DeliveryErrorAccessCodeFailed {
		t.Fatalf("delivery after Redis failure = %#v", failed)
	}
	if len(fixture.mail.sent) != 0 {
		t.Fatal("credentials were emailed without a persisted access code")
	}

	store.err = nil
	if _, err := fixture.service.RetryDelivery(context.Background(), AdminActor{}, fixture.order.ID, failed.ID); err != nil {
		t.Fatalf("RetryDelivery() error = %v", err)
	}
	if got := deliveryStatus(t, fixture); got.Status != domain.DeliverySent {
		t.Fatalf("retried delivery = %#v", got)
	}
	if len(fixture.mail.sent) != 1 || len(store.data) != 1 {
		t.Fatalf("retry sent %d emails and stored %d codes", len(fixture.mail.sent), len(store.data))
	}
}

func TestDeliverApprovedOrderEmailFailureReplacesOrphanedCodeOnRetry(t *testing.T) {
	fixture := newDeliveryFixture(t, true, "family@example.test:secret")
	store := newFakeAccessStore()
	fixture.service.SetAccessCodeSupport(store, "https://necly.example")
	fixture.mail.err = errors.New("smtp unavailable")

	if _, err := fixture.service.DeliverApprovedOrder(context.Background(), AdminActor{}, fixture.order.ID); err != nil {
		t.Fatal(err)
	}
	if len(store.data) != 1 {
		t.Fatalf("codes after email failure = %d, want 1", len(store.data))
	}
	var oldKey string
	for key := range store.data {
		oldKey = key
	}
	failed := deliveryStatus(t, fixture)

	fixture.mail.err = nil
	if _, err := fixture.service.RetryDelivery(context.Background(), AdminActor{}, fixture.order.ID, failed.ID); err != nil {
		t.Fatal(err)
	}
	if len(store.data) != 1 {
		t.Fatalf("retry left %d valid codes, want 1", len(store.data))
	}
	if _, stillValid := store.data[oldKey]; stillValid {
		t.Fatal("retry left the orphaned pre-email code valid")
	}
}

func TestDeliverApprovedOrderDoesNotLogAccessCodeOrCredentials(t *testing.T) {
	fixture := newDeliveryFixture(t, true, "family@example.test:super-secret-password")
	fixture.service.SetAccessCodeSupport(newFakeAccessStore(), "https://necly.example")
	fixture.mail.err = errors.New("smtp unavailable")

	var logs bytes.Buffer
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	})

	if _, err := fixture.service.DeliverApprovedOrder(context.Background(), AdminActor{}, fixture.order.ID); err != nil {
		t.Fatal(err)
	}
	if len(fixture.mail.attempted) != 1 {
		t.Fatalf("mail attempts = %d, want 1", len(fixture.mail.attempted))
	}
	body := fixture.mail.attempted[0].Body
	var code string
	for _, candidate := range strings.Fields(body) {
		if len(candidate) == 10 {
			code = candidate
			break
		}
	}
	for _, secret := range []string{"family@example.test", "super-secret-password", code} {
		if secret != "" && strings.Contains(logs.String(), secret) {
			t.Fatalf("delivery log leaked secret %q: %s", secret, logs.String())
		}
	}
}

func TestDeliverApprovedOrderMarksEmailFailureAndRetrySendsOnce(t *testing.T) {
	fixture := newDeliveryFixture(t, true, "user:secret")
	fixture.mail.err = errors.New("smtp unavailable")

	if _, err := fixture.service.DeliverApprovedOrder(context.Background(), AdminActor{}, fixture.order.ID); err != nil {
		t.Fatalf("DeliverApprovedOrder() error = %v", err)
	}
	first := deliveryStatus(t, fixture)
	if first.Status != domain.DeliveryFailed || first.ErrorCode == nil || *first.ErrorCode != DeliveryErrorEmailFailed {
		t.Fatalf("delivery after mailer failure = %#v", first)
	}
	if first.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", first.Attempts)
	}

	fixture.mail.err = nil
	retried, err := fixture.service.RetryDelivery(context.Background(), AdminActor{}, fixture.order.ID, first.ID)
	if err != nil {
		t.Fatalf("RetryDelivery() error = %v", err)
	}
	if retried.Status != domain.DeliverySent {
		t.Fatalf("retried status = %s, want sent", retried.Status)
	}
	if len(fixture.mail.sent) != 1 {
		t.Fatalf("sent emails after one failure + retry = %d, want 1", len(fixture.mail.sent))
	}

	// Replay of a successful delivery must not send anything again.
	if _, err := fixture.service.DeliverApprovedOrder(context.Background(), AdminActor{}, fixture.order.ID); err != nil {
		t.Fatalf("replay error = %v", err)
	}
	if _, err := fixture.service.RetryDelivery(context.Background(), AdminActor{}, fixture.order.ID, first.ID); err != nil {
		t.Fatalf("retry after sent error = %v", err)
	}
	if len(fixture.mail.sent) != 1 {
		t.Fatalf("replay produced %d emails, want still 1", len(fixture.mail.sent))
	}
	if !fixture.audit.has(AuditOrderDeliveryFailed) || !fixture.audit.has(AuditOrderDeliverySent) {
		t.Errorf("audit actions = %v", fixture.audit.actions())
	}
}

func TestDeliverApprovedOrderRequiresVerifiedBuyer(t *testing.T) {
	fixture := newDeliveryFixture(t, false, "user:secret")

	if _, err := fixture.service.DeliverApprovedOrder(context.Background(), AdminActor{}, fixture.order.ID); err != nil {
		t.Fatalf("DeliverApprovedOrder() error = %v", err)
	}
	delivery := deliveryStatus(t, fixture)
	if delivery.Status != domain.DeliveryFailed || delivery.ErrorCode == nil || *delivery.ErrorCode != DeliveryErrorBuyerUnverified {
		t.Fatalf("delivery for unverified buyer = %#v", delivery)
	}
	if len(fixture.mail.sent) != 0 {
		t.Fatal("unverified buyer received an email")
	}
}

func TestDeliverApprovedOrderFailsClosedWhenMailerOrInventoryIsUnavailable(t *testing.T) {
	t.Run("mailer disabled", func(t *testing.T) {
		fixture := newDeliveryFixture(t, true, "user:secret")
		fixture.mail.enabled = false
		if _, err := fixture.service.DeliverApprovedOrder(context.Background(), AdminActor{}, fixture.order.ID); err != nil {
			t.Fatalf("error = %v", err)
		}
		delivery := deliveryStatus(t, fixture)
		if delivery.ErrorCode == nil || *delivery.ErrorCode != DeliveryErrorMailerDisabled {
			t.Fatalf("delivery = %#v, want mailer_disabled", delivery)
		}
		if len(fixture.mail.sent) != 0 {
			t.Fatal("disabled mailer still recorded a send")
		}
	})

	t.Run("no reserved slots", func(t *testing.T) {
		fixture := newDeliveryFixture(t, true, "user:secret")
		fixture.repo.material[fixture.itemID] = &DeliveryMaterial{OrderItemID: fixture.itemID}
		if _, err := fixture.service.DeliverApprovedOrder(context.Background(), AdminActor{}, fixture.order.ID); err != nil {
			t.Fatalf("error = %v", err)
		}
		delivery := deliveryStatus(t, fixture)
		if delivery.ErrorCode == nil || *delivery.ErrorCode != DeliveryErrorInventoryMissing {
			t.Fatalf("delivery = %#v, want inventory_unavailable", delivery)
		}
	})

	t.Run("invalid stored credential", func(t *testing.T) {
		fixture := newDeliveryFixture(t, true, "BELUM DIISI - ganti dari Dashboard > Inventaris")
		if _, err := fixture.service.DeliverApprovedOrder(context.Background(), AdminActor{}, fixture.order.ID); err != nil {
			t.Fatalf("error = %v", err)
		}
		delivery := deliveryStatus(t, fixture)
		if delivery.ErrorCode == nil || *delivery.ErrorCode != DeliveryErrorCredentialInvalid {
			t.Fatalf("delivery = %#v, want credential_invalid", delivery)
		}
		if len(fixture.mail.sent) != 0 {
			t.Fatal("placeholder credential was emailed")
		}
	})
}

func TestDeliverApprovedOrderRejectsUnapprovedOrder(t *testing.T) {
	fixture := newDeliveryFixture(t, true, "user:secret")
	fixture.order.Status = domain.OrderPaid
	fixture.repo.order.Status = domain.OrderPaid

	_, err := fixture.service.DeliverApprovedOrder(context.Background(), AdminActor{}, fixture.order.ID)
	if !errors.Is(err, ErrOrderNotApproved) {
		t.Fatalf("error = %v, want ErrOrderNotApproved", err)
	}
	if len(fixture.mail.sent) != 0 {
		t.Fatal("paid order released credentials before admin approval")
	}
	if len(fixture.repo.deliveries) != 0 {
		t.Fatal("unapproved order created delivery rows")
	}
}

func TestRetryDeliveryDoesNotCrossOrderBoundary(t *testing.T) {
	fixture := newDeliveryFixture(t, true, "user:secret")
	if _, err := fixture.service.DeliverApprovedOrder(context.Background(), AdminActor{}, fixture.order.ID); err != nil {
		t.Fatal(err)
	}
	delivery := deliveryStatus(t, fixture)

	_, err := fixture.service.RetryDelivery(context.Background(), AdminActor{}, uuid.NewString(), delivery.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-order retry error = %v, want ErrNotFound", err)
	}
	if len(fixture.mail.sent) != 1 {
		t.Fatal("cross-order retry caused another send")
	}
}

func TestParseInventoryCredentialFormats(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		username     string
		password     string
		instructions string
		ok           bool
	}{
		{"json", `{"username":"alice","password":"s3cret","instructions":"catatan"}`, "alice", "s3cret", "catatan", true},
		{"json email alias", `{"email":"alice@example.test","password":"s3cret"}`, "alice@example.test", "s3cret", "", true},
		{"colon", "alice:s3:cret", "alice", "s3:cret", "", true},
		{"colon with instructions", "alice:s3cret\nGunakan profil 1", "alice", "s3cret", "Gunakan profil 1", true},
		{"placeholder", "BELUM DIISI - ganti dari Dashboard", "", "", "", false},
		{"missing password", "alice:", "", "", "", false},
		{"json missing password", `{"username":"alice"}`, "", "", "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			username, password, instructions, ok := parseInventoryCredential([]byte(test.raw))
			if ok != test.ok || username != test.username || password != test.password || instructions != test.instructions {
				t.Fatalf("parse(%q) = %q %q %q %t, want %q %q %q %t", test.raw, username, password, instructions, ok, test.username, test.password, test.instructions, test.ok)
			}
		})
	}
}
