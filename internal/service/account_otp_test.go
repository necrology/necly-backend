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

type fakeAccessStatus struct {
	codeKey     string
	expiresUnix int64
}

type fakeAccessStore struct {
	data     map[string][]byte
	statuses map[string]fakeAccessStatus
	ttl      time.Duration
	err      error
}

func newFakeAccessStore() *fakeAccessStore {
	return &fakeAccessStore{data: map[string][]byte{}, statuses: map[string]fakeAccessStatus{}}
}

func (s *fakeAccessStore) StoreIssued(_ context.Context, codeKey, scopeKey string, value []byte, expiresUnix int64, ttl time.Duration) error {
	if s.err != nil {
		return s.err
	}
	if previous, ok := s.statuses[scopeKey]; ok {
		delete(s.data, previous.codeKey)
	}
	s.data[codeKey] = append([]byte(nil), value...)
	s.statuses[scopeKey] = fakeAccessStatus{codeKey: codeKey, expiresUnix: expiresUnix}
	s.ttl = ttl
	return nil
}

func (s *fakeAccessStore) Load(_ context.Context, key string) ([]byte, bool, error) {
	if s.err != nil {
		return nil, false, s.err
	}
	value, ok := s.data[key]
	return value, ok, nil
}

func (s *fakeAccessStore) LoadStatus(_ context.Context, scopeKey string) (int64, bool, error) {
	if s.err != nil {
		return 0, false, s.err
	}
	status, ok := s.statuses[scopeKey]
	return status.expiresUnix, ok, nil
}

type fakeOTPInbox struct {
	enabled      bool
	message      *otpinbox.Message
	err          error
	calls        int
	since        time.Time
	respectSince bool
}

func (i *fakeOTPInbox) Enabled() bool { return i.enabled }

func (i *fakeOTPInbox) Latest(_ context.Context, _ string, since time.Time) (*otpinbox.Message, error) {
	i.calls++
	i.since = since
	if i.respectSince && i.message != nil && !i.message.ReceivedAt.IsZero() && i.message.ReceivedAt.Before(since.Add(-2*time.Minute)) {
		return nil, otpinbox.ErrNotFound
	}
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
	// Most tests exercise the central-mailbox path; the readiness test turns
	// this off to model a deployment without OTP_IMAP_*.
	svc.SetCentralOTPInboxReady(true)
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

func TestListAccountAccessReturnsAutomaticCodeStatusWithoutPlaintext(t *testing.T) {
	svc, _, _, store, order := accessFixture(t)
	svc.now = func() time.Time { return time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC) }
	expiresAt := svc.now().Add(AccessCodeTTL)
	payload, _ := json.Marshal(AccessCodePayload{
		OrderID: order.ID, OrderItemID: order.Items[0].ID, UserID: order.UserID,
		Emails: []string{"family@example.test"}, ExpiresAt: expiresAt,
	})
	if err := store.StoreIssued(context.Background(), HashAccessCode("ABCD234567"), AccessCodeScopeKey(order.UserID, order.ID, order.Items[0].ID), payload, expiresAt.Unix(), AccessCodeTTL); err != nil {
		t.Fatal(err)
	}

	items, err := svc.ListAccountAccess(context.Background(), order.UserID, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].AccessCodeStatus != "valid" || items[0].AccessCodeExpiresAt == nil || !items[0].AccessCodeExpiresAt.Equal(expiresAt) {
		t.Fatalf("access code metadata = %+v", items)
	}
	encoded, err := json.Marshal(items[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "ABCD234567") || strings.Contains(string(encoded), HashAccessCode("ABCD234567")) {
		t.Fatalf("account access leaked code material: %s", encoded)
	}
}

func TestAccountAccessReportsOTPReadinessAndBlocksUselessCodes(t *testing.T) {
	svc, repo, _, _, order := accessFixture(t)
	svc.SetCentralOTPInboxReady(false)
	material := repo.material[order.Items[0].ID]
	material.Slots[0].MailboxAppPasswordSet = false

	items, err := svc.ListAccountAccess(context.Background(), order.UserID, order.ID)
	if err != nil {
		t.Fatalf("ListAccountAccess: %v", err)
	}
	if len(items) != 1 || items[0].OTPReady {
		t.Fatalf("otp_ready = %+v, want false without any mailbox", items)
	}
	if _, err := svc.IssueAccountAccessCode(context.Background(), order.UserID, order.ID, order.Items[0].ID); !errors.Is(err, ErrOTPUnavailable) {
		t.Fatalf("code without mailbox error = %v, want ErrOTPUnavailable", err)
	}

	// An account-level Gmail app password is enough even without the central
	// fallback, so the page may offer the button again.
	material.Slots[0].MailboxAppPasswordSet = true
	items, err = svc.ListAccountAccess(context.Background(), order.UserID, order.ID)
	if err != nil || len(items) != 1 || !items[0].OTPReady {
		t.Fatalf("direct mailbox readiness = %+v, %v", items, err)
	}
	if _, err := svc.IssueAccountAccessCode(context.Background(), order.UserID, order.ID, order.Items[0].ID); err != nil {
		t.Fatalf("IssueAccountAccessCode with direct mailbox: %v", err)
	}
}

func TestLookupUsesThirtyMinuteLookbackForOlderOTP(t *testing.T) {
	store := newFakeAccessStore()
	code := storeOTPAccessCode(t, store, "family@example.test")
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	inbox := &fakeOTPInbox{enabled: true, respectSince: true, message: &otpinbox.Message{
		Code: "445566", ReceivedAt: now.Add(-20 * time.Minute),
	}}
	svc := NewAccountOTPService(store, inbox, 0)
	svc.now = func() time.Time { return now }

	message, err := svc.Lookup(context.Background(), "family@example.test", code)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if message.Code != "445566" {
		t.Fatalf("otp = %q, want the 20-minute-old code", message.Code)
	}
	if want := now.Add(-DefaultOTPLookback); !inbox.since.Equal(want) {
		t.Fatalf("lookback start = %s, want %s", inbox.since, want)
	}
	if DefaultOTPLookback < 30*time.Minute {
		t.Fatalf("DefaultOTPLookback = %s, want at least 30m", DefaultOTPLookback)
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
	if err := store.StoreIssued(context.Background(), HashAccessCode(code), AccessCodeScopeKey("user", "order", "item"), payload, expires.Unix(), time.Minute); err != nil {
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
	_ = store.StoreIssued(context.Background(), HashAccessCode(code), AccessCodeScopeKey(userID, orderID, itemID), payload, time.Now().Add(time.Minute).Unix(), time.Minute)
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
	_ = store.StoreIssued(context.Background(), HashAccessCode(code), AccessCodeScopeKey("user", "order", "item"), payload, time.Now().Add(time.Minute).Unix(), time.Minute)

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
	if err := store.StoreIssued(context.Background(), HashAccessCode(code), AccessCodeScopeKey("user", "order", email), payload, time.Now().Add(time.Minute).Unix(), time.Minute); err != nil {
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
	if central.calls != 0 {
		t.Fatalf("central mailbox was read %d times before the direct mailbox succeeded", central.calls)
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

func TestAccountOTPLookupObservabilityDistinguishesMissingMailboxAndReadableInbox(t *testing.T) {
	cipher := accountOTPTestCipher(t)
	store := newFakeAccessStore()
	code := storeOTPAccessCode(t, store, "family@example.test")

	t.Run("missing direct configuration falls back without PII", func(t *testing.T) {
		central := &fakeOTPInbox{enabled: true, err: otpinbox.ErrNotFound}
		svc := NewAccountOTPService(store, central, time.Minute)
		events := make([]AccountOTPLookupEvent, 0, 2)
		svc.observe = func(event AccountOTPLookupEvent) { events = append(events, event) }
		svc.SetPerAccountInbox(&fakeAccountResolver{account: &domain.InventoryAccount{
			AccountEmail: "family@example.test",
		}}, cipher, &fakeAccountReaderFactory{})

		_, err := svc.Lookup(context.Background(), "family@example.test", code)
		if !errors.Is(err, ErrOTPNotFound) {
			t.Fatalf("error = %v, want ErrOTPNotFound from readable fallback", err)
		}
		want := []AccountOTPLookupEvent{AccountOTPMailboxNotConfigured, AccountOTPCentralNotFound}
		if len(events) != len(want) || events[0] != want[0] || events[1] != want[1] {
			t.Fatalf("events = %v, want %v", events, want)
		}
		joined := string(events[0]) + string(events[1])
		for _, secret := range []string{"family@example.test", code, "111222"} {
			if strings.Contains(joined, secret) {
				t.Fatalf("observability leaked %q in %q", secret, joined)
			}
		}
	})

	t.Run("readable direct inbox has a distinct not-found outcome", func(t *testing.T) {
		sealed, err := cipher.Encrypt([]byte("gmail-app-password"))
		if err != nil {
			t.Fatal(err)
		}
		direct := &fakeOTPInbox{enabled: true, err: otpinbox.ErrNotFound}
		svc := NewAccountOTPService(store, &fakeOTPInbox{enabled: false}, time.Minute)
		events := make([]AccountOTPLookupEvent, 0, 1)
		svc.observe = func(event AccountOTPLookupEvent) { events = append(events, event) }
		svc.SetPerAccountInbox(&fakeAccountResolver{account: &domain.InventoryAccount{
			AccountEmail: "family@example.test", MailboxAppPasswordEncrypted: sealed,
		}}, cipher, &fakeAccountReaderFactory{reader: direct})

		_, err = svc.Lookup(context.Background(), "family@example.test", code)
		if !errors.Is(err, ErrOTPNotFound) {
			t.Fatalf("error = %v, want ErrOTPNotFound", err)
		}
		if len(events) != 1 || events[0] != AccountOTPDirectNotFound {
			t.Fatalf("events = %v, want [%s]", events, AccountOTPDirectNotFound)
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
