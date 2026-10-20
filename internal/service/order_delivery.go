package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/platform/mailer"
)

// DeliveryError* are the stable, non-sensitive codes stored on a failed
// delivery row and shown to administrators. They never contain credentials,
// recipient addresses or mailer error text.
const (
	DeliveryErrorBuyerUnverified     = "buyer_email_unverified"
	DeliveryErrorBuyerUnavailable    = "buyer_unavailable"
	DeliveryErrorMailerDisabled      = "mailer_disabled"
	DeliveryErrorCipherUnavailable   = "credential_cipher_unavailable"
	DeliveryErrorInventoryMissing    = "inventory_unavailable"
	DeliveryErrorAccountUnavailable  = "inventory_account_unavailable"
	DeliveryErrorCredentialInvalid   = "credential_invalid"
	DeliveryErrorEmailFailed         = "email_failed"
	DeliveryErrorPersistenceFailed   = "persistence_failed"
	deliverySendTimeout              = 20 * time.Second
	deliverySendingLease             = 5 * time.Minute
	inventoryCredentialInstructions  = "Simpan data akun ini. Jangan mengubah kata sandi atau profil akun karena akun digunakan bersama sesuai paket yang dibeli."
	deliveryEmailUsageInstructions   = "Cara penggunaan:\n1. Buka layanan sesuai produk yang dibeli.\n2. Masuk menggunakan username dan password di atas.\n3. Jangan mengubah kata sandi, email, atau profil akun.\n4. Jika tidak dapat masuk, hubungi dukungan dengan menyertakan nomor pesanan."
	maxDeliveryAccountLabelLength    = 120
	maxDeliveryCredentialFieldLength = 512
)

// DeliverySlot is the credential material for one reserved seat. The ciphertext
// is loaded only by the delivery repository and is never serialized.
type DeliverySlot struct {
	SlotID               string
	InventoryAccountID   string
	AccountLabel         string
	AccountStatus        string
	AccountExpiresAt     *time.Time
	EncryptedCredentials []byte
}

// DeliveryMaterial groups all reserved slots for one order item. The number of
// slots must cover the item quantity; a sharing account may back several slots,
// in which case its credentials are listed once in the email.
type DeliveryMaterial struct {
	OrderItemID string
	Slots       []DeliverySlot
}

// OrderDeliveryRepository is the persistence contract for the delivery ledger.
// Credential-bearing reads are explicit and scoped to one order item.
type OrderDeliveryRepository interface {
	EnsureOrderDeliveries(ctx context.Context, orderID string, now time.Time) ([]domain.OrderDelivery, error)
	ListOrderDeliveries(ctx context.Context, orderID string) ([]domain.OrderDelivery, error)
	GetOrderDelivery(ctx context.Context, orderID, deliveryID string) (*domain.OrderDelivery, error)
	LoadDeliveryMaterial(ctx context.Context, orderID, orderItemID string) (*DeliveryMaterial, error)
	// ClaimOrderDelivery atomically moves an open row to sending and increments
	// attempts. It returns false when another worker already owns the row or the
	// row is already sent. A stale sending row older than staleBefore may be
	// reclaimed after a crash.
	ClaimOrderDelivery(ctx context.Context, deliveryID string, now, staleBefore time.Time) (bool, error)
	MarkOrderDeliverySent(ctx context.Context, deliveryID string, now time.Time) error
	MarkOrderDeliveryFailed(ctx context.Context, deliveryID, errorCode string, now time.Time) error
}

// OrderDeliveryService decrypts inventory credentials for approved orders and
// emails them to the verified buyer exactly once per order item. It is the only
// service allowed to call CredentialCipher.Decrypt for delivery.
type OrderDeliveryService struct {
	repo   OrderDeliveryRepository
	orders AdminOrderRepository
	cipher *CredentialCipher
	mail   MailSender
	audit  AuditWriter
	now    func() time.Time
}

func NewOrderDeliveryService(repo OrderDeliveryRepository, orders AdminOrderRepository, cipher *CredentialCipher, mail MailSender, audit AuditWriter) *OrderDeliveryService {
	return &OrderDeliveryService{
		repo: repo, orders: orders, cipher: cipher, mail: mail, audit: audit,
		now: func() time.Time { return time.Now().UTC() },
	}
}

// ListDeliveries returns the audit view for one order. It never includes
// credentials, ciphertext or rendered email content.
func (s *OrderDeliveryService) ListDeliveries(ctx context.Context, orderID string) ([]domain.OrderDelivery, error) {
	if _, err := uuid.Parse(orderID); err != nil {
		return nil, ErrNotFound
	}
	if _, err := s.orders.GetOrderByID(ctx, orderID); err != nil {
		return nil, err
	}
	return s.repo.ListOrderDeliveries(ctx, orderID)
}

// DeliverApprovedOrder sends the account email for every order item that has
// not already been delivered. The caller must have committed the order to
// processing first: a paid webhook alone is deliberately insufficient.
func (s *OrderDeliveryService) DeliverApprovedOrder(ctx context.Context, actor AdminActor, orderID string) ([]domain.OrderDelivery, error) {
	if _, err := uuid.Parse(orderID); err != nil {
		return nil, ErrNotFound
	}
	order, err := s.orders.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if !deliveryApprovedStatus(order.Status) {
		return nil, ErrOrderNotApproved
	}

	now := s.now()
	if _, err := s.repo.EnsureOrderDeliveries(ctx, order.ID, now); err != nil {
		return nil, fmt.Errorf("prepare order deliveries: %w", err)
	}

	deliveries, err := s.repo.ListOrderDeliveries(ctx, order.ID)
	if err != nil {
		return nil, err
	}
	for _, delivery := range deliveries {
		if delivery.Status == domain.DeliverySent {
			continue
		}
		item := findOrderItem(order, delivery.OrderItemID)
		if item == nil {
			s.fail(ctx, actor, order, &delivery, DeliveryErrorInventoryMissing, false)
			continue
		}
		s.deliverOne(ctx, actor, order, item, &delivery, false)
	}
	return s.repo.ListOrderDeliveries(ctx, order.ID)
}

// RetryDelivery re-sends one failed or pending delivery. Calling it for a sent
// row is a no-op, so a repeated click cannot produce a second email.
func (s *OrderDeliveryService) RetryDelivery(ctx context.Context, actor AdminActor, orderID, deliveryID string) (*domain.OrderDelivery, error) {
	if _, err := uuid.Parse(orderID); err != nil {
		return nil, ErrNotFound
	}
	if _, err := uuid.Parse(deliveryID); err != nil {
		return nil, ErrDeliveryNotFound
	}
	order, err := s.orders.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if !deliveryApprovedStatus(order.Status) {
		return nil, ErrOrderNotApproved
	}
	delivery, err := s.repo.GetOrderDelivery(ctx, order.ID, deliveryID)
	if err != nil {
		return nil, err
	}
	if delivery.Status == domain.DeliverySent {
		return delivery, nil
	}
	item := findOrderItem(order, delivery.OrderItemID)
	if item == nil {
		s.fail(ctx, actor, order, delivery, DeliveryErrorInventoryMissing, true)
		return s.repo.GetOrderDelivery(ctx, order.ID, deliveryID)
	}
	s.deliverOne(ctx, actor, order, item, delivery, true)
	return s.repo.GetOrderDelivery(ctx, order.ID, deliveryID)
}

// deliverOne performs the claim/send/mark cycle. The claim is the idempotency
// gate: a concurrent approval or a duplicate retry sees "sending" (or "sent")
// and never reaches the mailer.
func (s *OrderDeliveryService) deliverOne(ctx context.Context, actor AdminActor, order *domain.Order, item *domain.OrderItem, delivery *domain.OrderDelivery, isRetry bool) {
	now := s.now()
	claimed, err := s.repo.ClaimOrderDelivery(ctx, delivery.ID, now, now.Add(-deliverySendingLease))
	if err != nil {
		log.Printf("delivery: claim %s failed: %v", delivery.ID, err)
		s.fail(ctx, actor, order, delivery, DeliveryErrorPersistenceFailed, isRetry)
		return
	}
	if !claimed {
		return
	}

	if reason := s.prepareFailureReason(order); reason != "" {
		s.fail(ctx, actor, order, delivery, reason, isRetry)
		return
	}

	material, err := s.repo.LoadDeliveryMaterial(ctx, order.ID, item.ID)
	if err != nil {
		log.Printf("delivery: load material for order %s item %s failed: %v", order.ID, item.ID, err)
		s.fail(ctx, actor, order, delivery, DeliveryErrorInventoryMissing, isRetry)
		return
	}
	if len(material.Slots) < item.Quantity {
		s.fail(ctx, actor, order, delivery, DeliveryErrorInventoryMissing, isRetry)
		return
	}

	accounts, reason := s.decryptAccounts(material)
	if reason != "" {
		s.fail(ctx, actor, order, delivery, reason, isRetry)
		return
	}

	msg := s.buildMessage(order, item, accounts)
	sendCtx, cancel := context.WithTimeout(ctx, deliverySendTimeout)
	defer cancel()
	if err := s.mail.Send(sendCtx, msg); err != nil {
		// Mailer errors are transport-level. Never log the body or the buyer
		// address; the delivery row carries the stable error code instead.
		log.Printf("delivery: sending order %s item %s failed: %v", order.ID, item.ID, err)
		s.fail(ctx, actor, order, delivery, DeliveryErrorEmailFailed, isRetry)
		return
	}

	if err := s.repo.MarkOrderDeliverySent(ctx, delivery.ID, s.now()); err != nil {
		log.Printf("delivery: mark sent %s failed: %v", delivery.ID, err)
		// The email left the process, so the row must not silently return to an
		// open state. Report the persistence problem for operator follow-up.
		s.fail(ctx, actor, order, delivery, DeliveryErrorPersistenceFailed, isRetry)
		return
	}
	s.record(ctx, actor, order, delivery, AuditOrderDeliverySent, map[string]any{
		"order_id": order.ID, "order_item_id": item.ID, "delivery_id": delivery.ID,
	})
}

type deliveryAccount struct {
	Label        string
	Username     string
	Password     string
	Instructions string
}

// prepareFailureReason checks preconditions that do not depend on credential
// content. Returning a code (instead of an error) keeps the outcome visible in
// the delivery ledger while the order itself stays in processing.
func (s *OrderDeliveryService) prepareFailureReason(order *domain.Order) string {
	if order.Customer == nil || order.Customer.DeletedAt != nil || !order.Customer.Active {
		return DeliveryErrorBuyerUnavailable
	}
	if order.Customer.EmailVerifiedAt == nil {
		return DeliveryErrorBuyerUnverified
	}
	if s.mail == nil || !s.mail.Enabled() {
		return DeliveryErrorMailerDisabled
	}
	if s.cipher == nil {
		return DeliveryErrorCipherUnavailable
	}
	return ""
}

func (s *OrderDeliveryService) decryptAccounts(material *DeliveryMaterial) ([]deliveryAccount, string) {
	now := s.now()
	seen := make(map[string]bool, len(material.Slots))
	accounts := make([]deliveryAccount, 0, len(material.Slots))
	for _, slot := range material.Slots {
		if slot.AccountStatus != "active" {
			return nil, DeliveryErrorAccountUnavailable
		}
		if slot.AccountExpiresAt != nil && !slot.AccountExpiresAt.After(now) {
			return nil, DeliveryErrorAccountUnavailable
		}
		if seen[slot.InventoryAccountID] {
			continue
		}
		seen[slot.InventoryAccountID] = true

		plain, err := s.cipher.Decrypt(slot.EncryptedCredentials)
		if err != nil {
			return nil, DeliveryErrorCredentialInvalid
		}
		username, password, instructions, ok := parseInventoryCredential(plain)
		if !ok || username == "" || password == "" {
			return nil, DeliveryErrorCredentialInvalid
		}
		label := strings.TrimSpace(slot.AccountLabel)
		if label == "" {
			label = "Akun langganan"
		}
		if utf8.RuneCountInString(label) > maxDeliveryAccountLabelLength {
			label = string([]rune(label)[:maxDeliveryAccountLabelLength])
		}
		if instructions == "" {
			instructions = inventoryCredentialInstructions
		}
		accounts = append(accounts, deliveryAccount{
			Label: label, Username: username, Password: password, Instructions: instructions,
		})
	}
	if len(accounts) == 0 {
		return nil, DeliveryErrorInventoryMissing
	}
	return accounts, ""
}

// parseInventoryCredential accepts the two formats administrators use in the
// dashboard:
//
//	username:password
//	{"username":"...","password":"...","instructions":"..."}
//
// The JSON form is preferred because it can carry per-account usage notes. A
// legacy free-form placeholder (for example the seed text) is rejected rather
// than guessed, so no buyer ever receives an unusable "credential" email.
func parseInventoryCredential(raw []byte) (username, password, instructions string, ok bool) {
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return "", "", "", false
	}
	if strings.HasPrefix(value, "{") {
		var payload struct {
			Username     string `json:"username"`
			Email        string `json:"email"`
			Password     string `json:"password"`
			Instructions string `json:"instructions"`
		}
		if err := json.Unmarshal([]byte(value), &payload); err != nil {
			return "", "", "", false
		}
		username = strings.TrimSpace(payload.Username)
		if username == "" {
			username = strings.TrimSpace(payload.Email)
		}
		password = strings.TrimSpace(payload.Password)
		instructions = strings.TrimSpace(payload.Instructions)
		if !validDeliveryCredentialField(username) || !validDeliveryCredentialField(password) {
			return "", "", "", false
		}
		return username, password, instructions, username != "" && password != ""
	}

	lines := strings.Split(value, "\n")
	first := strings.TrimSpace(lines[0])
	separator := strings.Index(first, ":")
	if separator <= 0 || separator == len(first)-1 {
		return "", "", "", false
	}
	username = strings.TrimSpace(first[:separator])
	password = strings.TrimSpace(first[separator+1:])
	if len(lines) > 1 {
		instructions = strings.TrimSpace(strings.Join(lines[1:], "\n"))
	}
	if !validDeliveryCredentialField(username) || !validDeliveryCredentialField(password) {
		return "", "", "", false
	}
	return username, password, instructions, true
}

func validDeliveryCredentialField(value string) bool {
	return value != "" && utf8.RuneCountInString(value) <= maxDeliveryCredentialFieldLength && !strings.ContainsAny(value, "\r\n")
}

func (s *OrderDeliveryService) buildMessage(order *domain.Order, item *domain.OrderItem, accounts []deliveryAccount) mailer.Message {
	var body strings.Builder
	body.WriteString("Halo " + order.Customer.Name + ",\n\n")
	body.WriteString("Pembayaran dan pesanan Anda telah disetujui. Berikut akun yang dapat digunakan:\n\n")
	fmt.Fprintf(&body, "Nomor pesanan : %s\n", order.Number)
	fmt.Fprintf(&body, "Produk        : %s\n", item.ProductName)
	fmt.Fprintf(&body, "Durasi        : %s\n", item.DurationName)
	fmt.Fprintf(&body, "Jumlah        : %d\n\n", item.Quantity)
	for index, account := range accounts {
		fmt.Fprintf(&body, "Akun %d (%s)\n", index+1, account.Label)
		fmt.Fprintf(&body, "  Username : %s\n", account.Username)
		fmt.Fprintf(&body, "  Password : %s\n", account.Password)
		if strings.TrimSpace(account.Instructions) != "" {
			fmt.Fprintf(&body, "  Catatan  : %s\n", account.Instructions)
		}
		body.WriteString("\n")
	}
	body.WriteString(deliveryEmailUsageInstructions)
	body.WriteString("\n\nJika ada kendala, balas email ini atau hubungi dukungan dengan menyertakan nomor pesanan.\n")
	body.WriteString("— NECLY Services\n")
	return mailer.Message{
		To:      order.Customer.Email,
		Subject: "Akun pesanan " + order.Number + " — " + item.ProductName,
		Body:    body.String(),
	}
}

func (s *OrderDeliveryService) fail(ctx context.Context, actor AdminActor, order *domain.Order, delivery *domain.OrderDelivery, code string, isRetry bool) {
	if err := s.repo.MarkOrderDeliveryFailed(ctx, delivery.ID, code, s.now()); err != nil {
		log.Printf("delivery: mark failed %s failed: %v", delivery.ID, err)
	}
	action := AuditOrderDeliveryFailed
	if isRetry {
		action = AuditOrderDeliveryRetried
	}
	s.record(ctx, actor, order, delivery, action, map[string]any{
		"order_id": order.ID, "order_item_id": delivery.OrderItemID,
		"delivery_id": delivery.ID, "error_code": code,
	})
}

func (s *OrderDeliveryService) record(ctx context.Context, actor AdminActor, order *domain.Order, delivery *domain.OrderDelivery, action string, metadata map[string]any) {
	if s.audit == nil {
		return
	}
	s.audit.Record(ctx, AuditEntry{
		ActorUserID: actor.UserID,
		Action:      action,
		Resource:    "order",
		ResourceID:  order.ID,
		IPAddress:   actor.IPAddress,
		Metadata:    metadata,
	})
}

func deliveryApprovedStatus(status domain.OrderStatus) bool {
	switch status {
	case domain.OrderProcessing, domain.OrderShipped, domain.OrderDelivered, domain.OrderCompleted:
		return true
	default:
		return false
	}
}

func findOrderItem(order *domain.Order, itemID string) *domain.OrderItem {
	for i := range order.Items {
		if order.Items[i].ID == itemID {
			return &order.Items[i]
		}
	}
	return nil
}

// IsDeliveryApprovedStatus is exported for the handler so it can distinguish a
// missing approval from an infrastructure failure without duplicating the list.
func IsDeliveryApprovedStatus(status domain.OrderStatus) bool {
	return deliveryApprovedStatus(status)
}
