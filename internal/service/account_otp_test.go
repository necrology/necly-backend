package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/platform/otpinbox"
)

type fakeAccessStore struct {
	data map[string][]byte
	ttl  time.Duration
}

func newFakeAccessStore() *fakeAccessStore { return &fakeAccessStore{data: map[string][]byte{}} }

func (s *fakeAccessStore) Store(_ context.Context, key string, value []byte, ttl time.Duration) error {
	s.data[key] = append([]byte(nil), value...)
	s.ttl = ttl
	return nil
}

func (s *fakeAccessStore) Load(_ context.Context, key string) ([]byte, bool, error) {
	value, ok := s.data[key]
	return value, ok, nil
}

type fakeOTPInbox struct {
	enabled bool
	message *otpinbox.Message
	err     error
}

func (i *fakeOTPInbox) Enabled() bool { return i.enabled }

func (i *fakeOTPInbox) Latest(context.Context, string, time.Time) (*otpinbox.Message, error) {
	return i.message, i.err
}

type fakeAccessValidator struct {
	valid                        bool
	err                          error
	orderID, orderItemID, userID string
}

func (v *fakeAccessValidator) ValidateAccessCode(_ context.Context, orderID, orderItemID, userID string, _ time.Time) (bool, error) {
	v.orderID, v.orderItemID, v.userID = orderID, orderItemID, userID
	return v.valid, v.err
}

func accountOTPTestCipher(t *testing.T) *CredentialCipher {
	t.Helper()
	cipher, err := NewCredentialCipher(base64.StdEncoding.EncodeToString(bytesOf('k', 32)))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	return cipher
}

func bytesOf(value byte, count int) []byte {
	out := make([]byte, count)
	for i := range out {
		out[i] = value
	}
	return out
}

func accessFixture(t *testing.T) (*OrderDeliveryService, *deliveryFakeRepo, *deliveryFakeMail, *fakeAccessStore, *domain.Order) {
	t.Helper()
	cipher := accountOTPTestCipher(t)
	encrypted, err := cipher.Encrypt([]byte(`{"username":"family@example.test","password":"secret","instructions":"Pakai profil 2"}`))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	now := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	order := &domain.Order{
		ID: uuid.NewString(), Number: "NEC-20261021-1", UserID: uuid.NewString(),
		Status:   domain.OrderProcessing,
		Customer: &domain.User{ID: "buyer", Name: "Budi", Email: "budi@example.test", Active: true, EmailVerifiedAt: &now},
		Items: []domain.OrderItem{{
			ID: uuid.NewString(), OrderID: "order", ProductName: "Netflix Premium", DurationName: "1 Bulan", Quantity: 1,
		}},
	}
	repo := &deliveryFakeRepo{
		order: order,
		deliveries: []domain.OrderDelivery{{
			ID: uuid.NewString(), OrderID: order.ID, OrderItemID: order.Items[0].ID, Status: domain.DeliverySent,
		}},
		material: map[string]*DeliveryMaterial{
			order.Items[0].ID: {
				OrderItemID: order.Items[0].ID,
				Slots: []DeliverySlot{{
					SlotID: uuid.NewString(), InventoryAccountID: uuid.NewString(),
					AccountLabel: "Akun utama", AccountStatus: "active", EncryptedCredentials: encrypted,
				}},
			},
		},
	}
	mail := &deliveryFakeMail{enabled: true}
	svc := NewOrderDeliveryService(repo, repo, cipher, mail, nil)
	store := newFakeAccessStore()
	svc.SetAccessCodeSupport(store, "https://necly.test")
	return svc, repo, mail, store, order
}

func TestListAccountAccessOnlyReturnsDeliveredOwnedOrders(t *testing.T) {
	svc, _, _, _, order := accessFixture(t)

	items, err := svc.ListAccountAccess(context.Background(), order.UserID, order.ID)
	if err != nil {
		t.Fatalf("ListAccountAccess: %v", err)
	}
	if len(items) != 1 || items[0].AccountEmail != "family@example.test" || items[0].AccountLabel != "Akun utama" {
		t.Fatalf("unexpected access items: %+v", items)
	}

	if _, err := svc.ListAccountAccess(context.Background(), "someone-else", order.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other owner error = %v, want ErrNotFound", err)
	}

	order.Status = domain.OrderPaid
	if _, err := svc.ListAccountAccess(context.Background(), order.UserID, order.ID); !errors.Is(err, ErrOrderNotApproved) {
		t.Fatalf("paid order error = %v, want ErrOrderNotApproved", err)
	}
}

func TestIssueAccountAccessCodeStoresHashAndEmailsTheBuyer(t *testing.T) {
	svc, _, mail, store, order := accessFixture(t)

	result, err := svc.IssueAccountAccessCode(context.Background(), order.UserID, order.ID, order.Items[0].ID)
	if err != nil {
		t.Fatalf("IssueAccountAccessCode: %v", err)
	}
	if len(result.Code) != 10 || result.EmailSent != true {
		t.Fatalf("unexpected result: %+v", result)
	}
	if store.ttl != AccessCodeTTL {
		t.Fatalf("ttl = %v, want %v", store.ttl, AccessCodeTTL)
	}
	raw, ok := store.data[HashAccessCode(result.Code)]
	if !ok {
		t.Fatal("the code must be stored under its hash, never plaintext")
	}
	var payload AccessCodePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(payload.Emails) != 1 || payload.Emails[0] != "family@example.test" {
		t.Fatalf("emails = %v", payload.Emails)
	}
	if len(mail.sent) != 1 || !strings.Contains(mail.sent[0].Body, result.Code) {
		t.Fatal("the buyer must receive the access code by email")
	}
}

func TestLookupRequiresMatchingEmailAndLiveCode(t *testing.T) {
	store := newFakeAccessStore()
	expires := time.Now().Add(10 * time.Minute)
	payload, _ := json.Marshal(AccessCodePayload{
		OrderID: uuid.NewString(), OrderItemID: uuid.NewString(), UserID: uuid.NewString(),
		Emails: []string{"family@example.test"}, ExpiresAt: expires,
	})
	code := "ABCD234567"
	if err := store.Store(context.Background(), HashAccessCode(code), payload, time.Minute); err != nil {
		t.Fatal(err)
	}
	inbox := &fakeOTPInbox{enabled: true, message: &otpinbox.Message{Code: "445566", ReceivedAt: time.Now()}}
	svc := NewAccountOTPService(store, inbox, time.Minute)

	message, err := svc.Lookup(context.Background(), " Family@Example.test ", "abcd-234567")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if message.Code != "445566" {
		t.Fatalf("otp = %q", message.Code)
	}

	if _, err := svc.Lookup(context.Background(), "other@example.test", code); !errors.Is(err, ErrOTPAccessInvalid) {
		t.Fatalf("wrong email error = %v, want ErrOTPAccessInvalid", err)
	}
	if _, err := svc.Lookup(context.Background(), "family@example.test", "WRONGCODE1"); !errors.Is(err, ErrOTPAccessInvalid) {
		t.Fatalf("wrong code error = %v, want ErrOTPAccessInvalid", err)
	}
}

func TestLookupRejectsCodeAfterAccessRevoked(t *testing.T) {
	store := newFakeAccessStore()
	orderID, itemID, userID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	payload, _ := json.Marshal(AccessCodePayload{
		OrderID: orderID, OrderItemID: itemID, UserID: userID,
		Emails: []string{"family@example.test"}, ExpiresAt: time.Now().Add(10 * time.Minute),
	})
	code := "ABCD234567"
	_ = store.Store(context.Background(), HashAccessCode(code), payload, time.Minute)
	inbox := &fakeOTPInbox{enabled: true, message: &otpinbox.Message{Code: "445566", ReceivedAt: time.Now()}}
	svc := NewAccountOTPService(store, inbox, time.Minute)
	validator := &fakeAccessValidator{valid: false}
	svc.SetAccessValidator(validator)

	if _, err := svc.Lookup(context.Background(), "family@example.test", code); !errors.Is(err, ErrOTPAccessInvalid) {
		t.Fatalf("revoked code error = %v, want ErrOTPAccessInvalid", err)
	}
	if validator.orderID != orderID || validator.orderItemID != itemID || validator.userID != userID {
		t.Fatalf("validator called with %s/%s/%s", validator.orderID, validator.orderItemID, validator.userID)
	}
}

func TestLookupReportsUnavailableAndMissingMailbox(t *testing.T) {
	store := newFakeAccessStore()
	payload, _ := json.Marshal(AccessCodePayload{Emails: []string{"family@example.test"}})
	code := "ABCD234567"
	_ = store.Store(context.Background(), HashAccessCode(code), payload, time.Minute)

	disabled := NewAccountOTPService(store, &fakeOTPInbox{enabled: false}, time.Minute)
	if _, err := disabled.Lookup(context.Background(), "family@example.test", code); !errors.Is(err, ErrOTPUnavailable) {
		t.Fatalf("disabled error = %v, want ErrOTPUnavailable", err)
	}

	empty := NewAccountOTPService(store, &fakeOTPInbox{enabled: true, err: otpinbox.ErrNotFound}, time.Minute)
	if _, err := empty.Lookup(context.Background(), "family@example.test", code); !errors.Is(err, ErrOTPNotFound) {
		t.Fatalf("empty error = %v, want ErrOTPNotFound", err)
	}

	noStore := NewAccountOTPService(nil, &fakeOTPInbox{enabled: true}, time.Minute)
	if _, err := noStore.Lookup(context.Background(), "family@example.test", code); !errors.Is(err, ErrOTPUnavailable) {
		t.Fatalf("no store error = %v, want ErrOTPUnavailable", err)
	}
}

type fakeAccountResolver struct {
	account *domain.InventoryAccount
	err     error
}

func (r *fakeAccountResolver) GetAccountByEmail(context.Context, string) (*domain.InventoryAccount, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.account, nil
}

type fakeAccountReaderFactory struct {
	reader      otpinbox.Reader
	appPassword string
	host        string
	email       string
	calls       int
}

func (f *fakeAccountReaderFactory) NewReader(email, appPassword, imapHost string) otpinbox.Reader {
	f.calls++
	f.email = email
	f.appPassword = appPassword
	f.host = imapHost
	return f.reader
}

func storeOTPAccessCode(t *testing.T, store *fakeAccessStore, email string) string {
	t.Helper()
	payload, err := json.Marshal(AccessCodePayload{
		Emails: []string{email}, ExpiresAt: time.Now().Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	code := "ABCD234567"
	if err := store.Store(context.Background(), HashAccessCode(code), payload, time.Minute); err != nil {
		t.Fatal(err)
	}
	return code
}

func TestAccountOTPLookupUsesDirectMailboxWhenAppPasswordExists(t *testing.T) {
	cipher := accountOTPTestCipher(t)
	sealed, err := cipher.Encrypt([]byte("gmail-app-password"))
	if err != nil {
		t.Fatal(err)
	}
	store := newFakeAccessStore()
	code := storeOTPAccessCode(t, store, "family@example.test")
	direct := &fakeOTPInbox{enabled: true, message: &otpinbox.Message{Code: "111222"}}
	central := &fakeOTPInbox{enabled: true, message: &otpinbox.Message{Code: "999888"}}
	factory := &fakeAccountReaderFactory{reader: direct}
	svc := NewAccountOTPService(store, central, time.Minute)
	svc.SetPerAccountInbox(&fakeAccountResolver{account: &domain.InventoryAccount{
		AccountEmail: "family@example.test", MailboxAppPasswordEncrypted: sealed, MailboxIMAPHost: "imap.gmail.com:993",
	}}, cipher, factory)

	message, err := svc.Lookup(context.Background(), "family@example.test", code)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if message.Code != "111222" {
		t.Fatalf("otp = %q, want the direct mailbox code", message.Code)
	}
	if factory.calls != 1 || factory.appPassword != "gmail-app-password" || factory.host != "imap.gmail.com:993" || factory.email != "family@example.test" {
		t.Fatalf("factory call = %+v", factory)
	}
}

func TestAccountOTPLookupFallsBackToCentralMailbox(t *testing.T) {
	cipher := accountOTPTestCipher(t)
	store := newFakeAccessStore()
	code := storeOTPAccessCode(t, store, "family@example.test")
	central := &fakeOTPInbox{enabled: true, message: &otpinbox.Message{Code: "333444"}}

	t.Run("no app password on account", func(t *testing.T) {
		factory := &fakeAccountReaderFactory{reader: &fakeOTPInbox{enabled: true}}
		svc := NewAccountOTPService(store, central, time.Minute)
		svc.SetPerAccountInbox(&fakeAccountResolver{account: &domain.InventoryAccount{AccountEmail: "family@example.test"}}, cipher, factory)
		message, err := svc.Lookup(context.Background(), "family@example.test", code)
		if err != nil || message.Code != "333444" {
			t.Fatalf("message=%+v err=%v", message, err)
		}
		if factory.calls != 0 {
			t.Fatal("direct reader was built without an app password")
		}
	})

	t.Run("direct reader returns an error", func(t *testing.T) {
		sealed, err := cipher.Encrypt([]byte("gmail-app-password"))
		if err != nil {
			t.Fatal(err)
		}
		factory := &fakeAccountReaderFactory{reader: &fakeOTPInbox{enabled: true, err: errors.New("imap login failed")}}
		svc := NewAccountOTPService(store, central, time.Minute)
		svc.SetPerAccountInbox(&fakeAccountResolver{account: &domain.InventoryAccount{
			AccountEmail: "family@example.test", MailboxAppPasswordEncrypted: sealed,
		}}, cipher, factory)
		message, err := svc.Lookup(context.Background(), "family@example.test", code)
		if err != nil || message.Code != "333444" {
			t.Fatalf("message=%+v err=%v", message, err)
		}
		if factory.calls != 1 {
			t.Fatal("direct reader was not attempted")
		}
	})

	t.Run("account lookup is not found", func(t *testing.T) {
		factory := &fakeAccountReaderFactory{reader: &fakeOTPInbox{enabled: true}}
		svc := NewAccountOTPService(store, central, time.Minute)
		svc.SetPerAccountInbox(&fakeAccountResolver{err: ErrNotFound}, cipher, factory)
		message, err := svc.Lookup(context.Background(), "family@example.test", code)
		if err != nil || message.Code != "333444" {
			t.Fatalf("message=%+v err=%v", message, err)
		}
		if factory.calls != 0 {
			t.Fatalf("factory called %d times for a missing account", factory.calls)
		}
	})
}

func TestAccountOTPLookupFailsClosedWithoutAnyMailbox(t *testing.T) {
	store := newFakeAccessStore()
	code := storeOTPAccessCode(t, store, "family@example.test")
	svc := NewAccountOTPService(store, &fakeOTPInbox{enabled: false}, time.Minute)
	svc.SetPerAccountInbox(&fakeAccountResolver{err: ErrNotFound}, accountOTPTestCipher(t), &fakeAccountReaderFactory{})

	if _, err := svc.Lookup(context.Background(), "family@example.test", code); !errors.Is(err, ErrOTPUnavailable) {
		t.Fatalf("error = %v, want ErrOTPUnavailable", err)
	}
}

func TestAccountOTPLookupWrongCodeStaysInvalidEvenWithDirectMailbox(t *testing.T) {
	store := newFakeAccessStore()
	_ = storeOTPAccessCode(t, store, "family@example.test")
	svc := NewAccountOTPService(store, &fakeOTPInbox{enabled: true}, time.Minute)
	svc.SetPerAccountInbox(&fakeAccountResolver{err: ErrNotFound}, accountOTPTestCipher(t), &fakeAccountReaderFactory{})

	if _, err := svc.Lookup(context.Background(), "family@example.test", "WRONGCODE1"); !errors.Is(err, ErrOTPAccessInvalid) {
		t.Fatalf("error = %v, want ErrOTPAccessInvalid", err)
	}
}
