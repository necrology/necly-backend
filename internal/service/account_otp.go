package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/platform/mailer"
	"github.com/necly/services/internal/platform/otpinbox"
)

var (
	// ErrOTPAccessInvalid is deliberately identical for an unknown code and a
	// code that belongs to another email. The public page must not reveal which
	// half of the pair was wrong.
	ErrOTPAccessInvalid = errors.New("Kode akses tidak valid atau sudah kedaluwarsa")
	// ErrOTPUnavailable means retrieval is not configured (no IMAP mailbox or no
	// Redis). It is an operator problem, not a wrong customer input.
	ErrOTPUnavailable = errors.New("Layanan ambil OTP belum tersedia")
	// ErrOTPNotFound means no recent OTP email arrived for the account yet.
	ErrOTPNotFound = errors.New("Belum ada kode OTP masuk untuk akun ini")
)

// AccessCodeTTL is how long a code generated from the account dashboard stays
// usable. It is short on purpose: the code is a bearer credential for reading
// someone else's OTP.
const AccessCodeTTL = 15 * time.Minute

// AccessCodeStore persists short-lived access codes outside the database. The
// key is a SHA-256 digest; the plaintext code is never stored.
type AccessCodeStore interface {
	Store(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Load(ctx context.Context, key string) (value []byte, found bool, err error)
}

// AccessCodePayload is the data bound to one code. Emails is a list because a
// shared product item may contain slots from more than one inventory account.
type AccessCodePayload struct {
	OrderID     string    `json:"order_id"`
	OrderItemID string    `json:"order_item_id"`
	UserID      string    `json:"user_id"`
	Emails      []string  `json:"emails"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// AccountAccess is one purchased account visible to its owner on the order
// page. It intentionally exposes only the mailbox/username, never the password
// or the encrypted inventory blob.
type AccountAccess struct {
	OrderItemID  string `json:"order_item_id"`
	ProductName  string `json:"product_name"`
	DurationName string `json:"duration_name"`
	AccountLabel string `json:"account_label"`
	AccountEmail string `json:"account_email"`
}

// IssuedAccessCode is returned once, to the authenticated owner. The dashboard
// shows it and the service also emails it to the verified buyer.
type IssuedAccessCode struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
	Emails    []string  `json:"emails"`
	EmailSent bool      `json:"email_sent"`
}

// SetAccessCodeSupport wires the email-OTP feature into the existing delivery
// service, which is the only component allowed to decrypt inventory
// credentials. A nil store leaves the feature disabled.
func (s *OrderDeliveryService) SetAccessCodeSupport(store AccessCodeStore, frontendURL string) {
	s.accessCodes = store
	s.frontendURL = strings.TrimRight(frontendURL, "/")
}

// InboxReady reports whether account OTP retrieval is configured. The frontend
// uses it to explain an unavailable state before the customer types anything.
func (s *OrderDeliveryService) InboxReady() bool { return s.accessCodes != nil }

// ListAccountAccess returns the delivered accounts for one order owned by
// userID. It refuses orders that are not yet approved, so a paid-but-unreviewed
// order never leaks the account email.
func (s *OrderDeliveryService) ListAccountAccess(ctx context.Context, userID, orderID string) ([]AccountAccess, error) {
	if _, err := uuid.Parse(orderID); err != nil {
		return nil, ErrNotFound
	}
	order, err := s.orders.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order.UserID != userID {
		// Do not reveal that another user's order exists.
		return nil, ErrNotFound
	}
	if !deliveryApprovedStatus(order.Status) {
		return nil, ErrOrderNotApproved
	}

	deliveries, err := s.repo.ListOrderDeliveries(ctx, order.ID)
	if err != nil {
		return nil, err
	}
	sent := make(map[string]bool, len(deliveries))
	for _, delivery := range deliveries {
		if delivery.Status == domain.DeliverySent {
			sent[delivery.OrderItemID] = true
		}
	}

	out := make([]AccountAccess, 0, len(order.Items))
	for i := range order.Items {
		item := &order.Items[i]
		if !sent[item.ID] {
			continue
		}
		accounts, err := s.deliveredAccounts(ctx, order.ID, item.ID)
		if err != nil {
			// A missing row after a successful delivery is an operator problem,
			// not a reason to fail the whole order page.
			log.Printf("account access: load material for order %s item %s failed: %v", order.ID, item.ID, err)
			continue
		}
		for _, account := range accounts {
			out = append(out, AccountAccess{
				OrderItemID: item.ID, ProductName: item.ProductName, DurationName: item.DurationName,
				AccountLabel: account.Label, AccountEmail: account.Username,
			})
		}
	}
	return out, nil
}

// IssueAccountAccessCode creates a short-lived code for one delivered item.
// Only the authenticated owner can request it. The code is returned once and
// also emailed to the buyer's verified address; the mailbox password is never
// involved.
func (s *OrderDeliveryService) IssueAccountAccessCode(ctx context.Context, userID, orderID, itemID string) (*IssuedAccessCode, error) {
	if s.accessCodes == nil {
		return nil, ErrOTPUnavailable
	}
	if _, err := uuid.Parse(orderID); err != nil {
		return nil, ErrNotFound
	}
	if _, err := uuid.Parse(itemID); err != nil {
		return nil, ErrNotFound
	}
	order, err := s.orders.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order.UserID != userID {
		return nil, ErrNotFound
	}
	if !deliveryApprovedStatus(order.Status) {
		return nil, ErrOrderNotApproved
	}

	deliveries, err := s.repo.ListOrderDeliveries(ctx, order.ID)
	if err != nil {
		return nil, err
	}
	item := findOrderItem(order, itemID)
	if item == nil {
		return nil, ErrNotFound
	}
	delivered := false
	for _, delivery := range deliveries {
		if delivery.OrderItemID == itemID && delivery.Status == domain.DeliverySent {
			delivered = true
			break
		}
	}
	if !delivered {
		return nil, ErrOrderNotApproved
	}

	accounts, err := s.deliveredAccounts(ctx, order.ID, itemID)
	if err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, ErrNotFound
	}
	emails := make([]string, 0, len(accounts))
	seen := make(map[string]bool, len(accounts))
	for _, account := range accounts {
		email := strings.TrimSpace(account.Username)
		key := strings.ToLower(email)
		if email == "" || seen[key] {
			continue
		}
		seen[key] = true
		emails = append(emails, email)
	}
	if len(emails) == 0 {
		return nil, ErrNotFound
	}

	code, err := newAccessCode()
	if err != nil {
		return nil, fmt.Errorf("generate access code: %w", err)
	}
	expiresAt := s.now().Add(AccessCodeTTL)
	payload := AccessCodePayload{
		OrderID: order.ID, OrderItemID: itemID, UserID: userID,
		Emails: emails, ExpiresAt: expiresAt,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode access code: %w", err)
	}
	if err := s.accessCodes.Store(ctx, HashAccessCode(code), encoded, AccessCodeTTL); err != nil {
		return nil, fmt.Errorf("store access code: %w", err)
	}

	result := &IssuedAccessCode{Code: code, ExpiresAt: expiresAt, Emails: emails}
	if reason := s.prepareFailureReason(order); reason == "" {
		msg := s.buildAccessCodeMessage(order, item, emails, code, expiresAt)
		sendCtx, cancel := context.WithTimeout(ctx, deliverySendTimeout)
		defer cancel()
		if err := s.mail.Send(sendCtx, msg); err != nil {
			// The code is already stored; the customer can still read it on the
			// page. Log without the code or the buyer address.
			log.Printf("account access: send code email for order %s item %s failed: %v", order.ID, itemID, err)
		} else {
			result.EmailSent = true
		}
	}
	return result, nil
}

// deliveredAccounts decrypts the reserved slots for one delivered item and
// returns only label + username. Any malformed credential is skipped rather
// than partially exposed.
func (s *OrderDeliveryService) deliveredAccounts(ctx context.Context, orderID, itemID string) ([]deliveryAccount, error) {
	if s.cipher == nil {
		return nil, ErrOTPUnavailable
	}
	material, err := s.repo.LoadDeliveryMaterial(ctx, orderID, itemID)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(material.Slots))
	out := make([]deliveryAccount, 0, len(material.Slots))
	for _, slot := range material.Slots {
		if seen[slot.InventoryAccountID] {
			continue
		}
		seen[slot.InventoryAccountID] = true
		plain, err := s.cipher.Decrypt(slot.EncryptedCredentials)
		if err != nil {
			continue
		}
		username, _, instructions, ok := parseInventoryCredential(plain)
		if !ok || username == "" {
			continue
		}
		label := strings.TrimSpace(slot.AccountLabel)
		if label == "" {
			label = "Akun langganan"
		}
		out = append(out, deliveryAccount{Label: label, Username: username, Instructions: instructions})
	}
	return out, nil
}

func (s *OrderDeliveryService) buildAccessCodeMessage(order *domain.Order, item *domain.OrderItem, emails []string, code string, expiresAt time.Time) mailer.Message {
	var body strings.Builder
	body.WriteString("Halo " + order.Customer.Name + ",\n\n")
	body.WriteString("Anda meminta kode akses untuk mengambil OTP login akun berikut:\n\n")
	fmt.Fprintf(&body, "Pesanan : %s\n", order.Number)
	fmt.Fprintf(&body, "Produk  : %s (%s)\n", item.ProductName, item.DurationName)
	for _, email := range emails {
		fmt.Fprintf(&body, "Email akun : %s\n", email)
	}
	fmt.Fprintf(&body, "\nKode akses : %s\n", code)
	fmt.Fprintf(&body, "Berlaku sampai : %s WIB\n\n", expiresAt.In(time.FixedZone("WIB", 7*3600)).Format("02 Jan 2006 15:04"))
	body.WriteString("Cara mengambil OTP:\n")
	if s.frontendURL != "" {
		fmt.Fprintf(&body, "1. Buka %s/otp\n", s.frontendURL)
	} else {
		body.WriteString("1. Buka halaman Ambil OTP di situs Necly.\n")
	}
	body.WriteString("2. Masukkan email akun dan kode akses di atas.\n")
	body.WriteString("3. Kode OTP dari platform akan ditampilkan dan dapat langsung disalin.\n\n")
	body.WriteString("Jangan bagikan kode akses ini. Jika Anda tidak meminta kode, abaikan email ini dan hubungi dukungan.\n\n")
	body.WriteString("— NECLY Services\n")
	return mailer.Message{
		To:      order.Customer.Email,
		Subject: "Kode akses OTP pesanan " + order.Number,
		Body:    body.String(),
	}
}

const accessCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func newAccessCode() (string, error) {
	buf := make([]byte, 10)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, len(buf))
	for i, value := range buf {
		out[i] = accessCodeAlphabet[int(value)%len(accessCodeAlphabet)]
	}
	return string(out), nil
}

// HashAccessCode is the only representation stored at rest. Lowercase input is
// accepted because customers often type the code from an email.
func HashAccessCode(code string) string {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	digest := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(digest[:])
}

// NormalizeAccessCode accepts the separators customers naturally type.
func NormalizeAccessCode(code string) string {
	replacer := strings.NewReplacer(" ", "", "-", "", "_", "")
	return strings.ToUpper(replacer.Replace(strings.TrimSpace(code)))
}

// AccountOTPService resolves a code+email pair into the newest OTP in the
// shared mailbox. It never logs the email, code or OTP.
type AccountOTPService struct {
	codes    AccessCodeStore
	inbox    otpinbox.Reader
	lookback time.Duration
	now      func() time.Time
}

func NewAccountOTPService(codes AccessCodeStore, inbox otpinbox.Reader, lookback time.Duration) *AccountOTPService {
	if lookback <= 0 {
		lookback = 15 * time.Minute
	}
	return &AccountOTPService{codes: codes, inbox: inbox, lookback: lookback, now: func() time.Time { return time.Now().UTC() }}
}

// InboxReady tells the UI whether OTP retrieval can work at all.
func (s *AccountOTPService) InboxReady() bool {
	return s != nil && s.codes != nil && s.inbox != nil && s.inbox.Enabled()
}

// Lookup verifies the bearer code and returns the newest OTP addressed to the
// supplied account email. Errors are intentionally coarse.
func (s *AccountOTPService) Lookup(ctx context.Context, email, code string) (*otpinbox.Message, error) {
	if s == nil || s.codes == nil || s.inbox == nil || !s.inbox.Enabled() {
		return nil, ErrOTPUnavailable
	}
	email = strings.ToLower(strings.TrimSpace(email))
	code = NormalizeAccessCode(code)
	if email == "" || len(code) < 8 || len(code) > 16 {
		return nil, ErrOTPAccessInvalid
	}
	raw, found, err := s.codes.Load(ctx, HashAccessCode(code))
	if err != nil {
		return nil, fmt.Errorf("load access code: %w", err)
	}
	if !found {
		return nil, ErrOTPAccessInvalid
	}
	var payload AccessCodePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, ErrOTPAccessInvalid
	}
	if !payload.ExpiresAt.IsZero() && !payload.ExpiresAt.After(s.now()) {
		return nil, ErrOTPAccessInvalid
	}
	matched := false
	for _, candidate := range payload.Emails {
		if strings.EqualFold(strings.TrimSpace(candidate), email) {
			matched = true
			break
		}
	}
	if !matched {
		return nil, ErrOTPAccessInvalid
	}

	message, err := s.inbox.Latest(ctx, email, s.now().Add(-s.lookback))
	if errors.Is(err, otpinbox.ErrNotFound) {
		return nil, ErrOTPNotFound
	}
	if errors.Is(err, otpinbox.ErrNotConfigured) {
		return nil, ErrOTPUnavailable
	}
	if err != nil {
		// Transport errors must not leak mailbox details to the public page.
		return nil, fmt.Errorf("read otp inbox: %w", err)
	}
	return message, nil
}
