package service

import (
	"context"
	"encoding/json"
	"errors"
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
	DeliveryErrorBuyerUnverified    = "buyer_email_unverified"
	DeliveryErrorBuyerUnavailable   = "buyer_unavailable"
	DeliveryErrorMailerDisabled     = "mailer_disabled"
	DeliveryErrorCipherUnavailable  = "credential_cipher_unavailable"
	DeliveryErrorInventoryMissing   = "inventory_unavailable"
	DeliveryErrorAccountUnavailable = "inventory_account_unavailable"
	DeliveryErrorCredentialInvalid  = "credential_invalid"
	DeliveryErrorEmailFailed        = "email_failed"
	DeliveryErrorPersistenceFailed  = "persistence_failed"
	// DeliveryErrorNoAlternativeAccount is recorded when every candidate
	// account has already been used by this buyer for the same product. The
	// delivery must not silently reuse one of them.
	DeliveryErrorNoAlternativeAccount = "no_alternative_account"
	// DeliveryErrorDurationUnavailable is recorded when the purchased item has
	// no usable duration. The access window is never guessed from the account
	// expiry or a default value.
	DeliveryErrorDurationUnavailable = "duration_unavailable"
	deliverySendTimeout              = 20 * time.Second
	deliverySendingLease             = 5 * time.Minute
	maxDeliveryReasonLength          = 500
	inventoryCredentialInstructions  = "Simpan data akun ini. Jangan mengubah kata sandi atau profil akun karena akun digunakan bersama sesuai paket yang dibeli."
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
	// DurationDays is the purchased access length in days. It is loaded from
	// product_durations through the order item's price. Zero means the item has
	// no usable duration and the delivery must fail instead of guessing.
	DurationDays int
	Slots        []DeliverySlot
}

// DeliveryAccountOption is the credential-free eligibility shape returned to the
// admin dashboard before an account is assigned. RemainingSlots already accounts
// for the five-user hard cap, not only the stored sharing_limit.
type DeliveryAccountOption struct {
	InventoryAccountID string     `json:"inventory_account_id"`
	Label              string     `json:"label"`
	Status             string     `json:"status"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	DaysRemaining      *int       `json:"days_remaining"`
	SharingLimit       int        `json:"sharing_limit"`
	AssignedUsers      int        `json:"assigned_users"`
	UsedSlots          int        `json:"used_slots"`
	RemainingSlots     int        `json:"remaining_slots"`
	UsedByCustomer     bool       `json:"used_by_customer"`
	AssignedToItem     bool       `json:"assigned_to_item"`
	CanAssign          bool       `json:"can_assign"`
}

// DeliveryItemOptions groups the candidate accounts for one order item. The
// admin picks one account for the delivery; a quantity above the five-user cap
// is reported as not assignable rather than silently split.
type DeliveryItemOptions struct {
	DeliveryID         string                  `json:"delivery_id"`
	OrderItemID        string                  `json:"order_item_id"`
	ProductID          string                  `json:"product_id"`
	ProductPriceID     string                  `json:"product_price_id"`
	ProductName        string                  `json:"product_name"`
	DurationName       string                  `json:"duration_name"`
	Quantity           int                     `json:"quantity"`
	AssignedAccountIDs []string                `json:"assigned_account_ids"`
	Accounts           []DeliveryAccountOption `json:"accounts"`
}

// OrderDeliveryRepository is the persistence contract for the delivery ledger.
// Credential-bearing reads are explicit and scoped to one order item.
type OrderDeliveryRepository interface {
	EnsureOrderDeliveries(ctx context.Context, orderID string, now time.Time) ([]domain.OrderDelivery, error)
	ListOrderDeliveries(ctx context.Context, orderID string) ([]domain.OrderDelivery, error)
	GetOrderDelivery(ctx context.Context, orderID, deliveryID string) (*domain.OrderDelivery, error)
	LoadDeliveryMaterial(ctx context.Context, orderID, orderItemID string) (*DeliveryMaterial, error)
	// LoadDeliveryOptions returns every candidate account for every order item,
	// including whether this buyer already used the account for the product.
	LoadDeliveryOptions(ctx context.Context, orderID string) ([]DeliveryItemOptions, error)
	// PrepareDeliveryAssignment keeps the current slots when they are valid and
	// unused, otherwise moves them to an unused eligible account. It returns
	// ErrNoAlternativeAccount when every account was already used.
	PrepareDeliveryAssignment(ctx context.Context, orderID, orderItemID string, now time.Time) error
	// AssignDeliveryAccount moves one item's reserved slots to an explicitly
	// selected account under row locks.
	AssignDeliveryAccount(ctx context.Context, orderID, deliveryID, accountID string, now time.Time) (*domain.OrderDelivery, error)
	// ReplaceDeliveryAccount releases the expired slots and provisions new ones
	// on another account, recording the audit row before the email is sent.
	ReplaceDeliveryAccount(ctx context.Context, orderID, deliveryID, accountID, reason string, complaintApproved bool, actorUserID string, now time.Time) (*domain.DeliveryReplacement, error)
	MarkDeliveryReplacementSent(ctx context.Context, replacementID string, now time.Time) error
	MarkDeliveryReplacementFailed(ctx context.Context, replacementID, errorCode string, now time.Time) error
	// ActivateDeliveryAccess fills the buyer's access window after the email has
	// left the process. COALESCE keeps a retry from extending it.
	ActivateDeliveryAccess(ctx context.Context, orderID, orderItemID string, now, expiresAt time.Time) error
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

	// accessCodes and frontendURL power the email-OTP access page. Both are
	// optional so the W17 delivery flow keeps working in existing deployments.
	accessCodes AccessCodeStore
	frontendURL string
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

// DeliveryOptions returns the credential-free account candidates for every
// order item. Delivery rows are created on demand so the dashboard can choose
// an account before the first send.
func (s *OrderDeliveryService) DeliveryOptions(ctx context.Context, orderID string) ([]DeliveryItemOptions, error) {
	if _, err := uuid.Parse(orderID); err != nil {
		return nil, ErrNotFound
	}
	order, err := s.orders.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if !deliverySelectableStatus(order.Status) {
		return nil, ErrOrderNotApproved
	}
	if _, err := s.repo.EnsureOrderDeliveries(ctx, order.ID, s.now()); err != nil {
		return nil, err
	}
	return s.repo.LoadDeliveryOptions(ctx, order.ID)
}

// AssignAccount applies an explicit admin choice before the credentials are
// emailed. The repository validates capacity, reuse rules and ownership under
// row locks; this method only maps the outcome and writes the audit entry.
func (s *OrderDeliveryService) AssignAccount(ctx context.Context, actor AdminActor, orderID, deliveryID, accountID string) (*domain.OrderDelivery, error) {
	if _, err := uuid.Parse(orderID); err != nil {
		return nil, ErrNotFound
	}
	if _, err := uuid.Parse(deliveryID); err != nil {
		return nil, ErrDeliveryNotFound
	}
	if _, err := uuid.Parse(accountID); err != nil {
		return nil, ErrInvalidInput
	}
	order, err := s.orders.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if !deliverySelectableStatus(order.Status) {
		return nil, ErrOrderNotApproved
	}
	delivery, err := s.repo.AssignDeliveryAccount(ctx, order.ID, deliveryID, accountID, s.now())
	if err != nil {
		return nil, err
	}
	s.record(ctx, actor, order, delivery, AuditOrderAccountAssigned, map[string]any{
		"order_id": order.ID, "order_item_id": delivery.OrderItemID,
		"delivery_id": delivery.ID, "inventory_account_id": accountID,
	})
	return delivery, nil
}

// ReplaceAccount releases an expired (or complaint-approved) access window and
// emails the replacement account. No order or payment row is created or
// modified. A repeated call immediately after success is rejected because the
// newly assigned slot is no longer expired.
func (s *OrderDeliveryService) ReplaceAccount(ctx context.Context, actor AdminActor, orderID, deliveryID, accountID, reason string, complaintApproved bool) (*domain.OrderDelivery, error) {
	if _, err := uuid.Parse(orderID); err != nil {
		return nil, ErrNotFound
	}
	if _, err := uuid.Parse(deliveryID); err != nil {
		return nil, ErrDeliveryNotFound
	}
	if _, err := uuid.Parse(accountID); err != nil {
		return nil, ErrInvalidInput
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > maxDeliveryReasonLength {
		return nil, ErrInvalidInput
	}
	order, err := s.orders.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	delivery, err := s.repo.GetOrderDelivery(ctx, order.ID, deliveryID)
	if err != nil {
		return nil, err
	}
	if delivery.Status != domain.DeliverySent {
		return nil, ErrDeliveryNotSent
	}

	replacement, err := s.repo.ReplaceDeliveryAccount(ctx, order.ID, delivery.ID, accountID, reason, complaintApproved, actor.UserID, s.now())
	if err != nil {
		return nil, err
	}

	material, err := s.repo.LoadDeliveryMaterial(ctx, order.ID, delivery.OrderItemID)
	if err != nil {
		s.markReplacementFailed(ctx, replacement.ID)
		return nil, fmt.Errorf("load replacement material: %w", err)
	}
	if material.DurationDays <= 0 {
		s.markReplacementFailed(ctx, replacement.ID)
		return nil, ErrDeliveryDurationUnavailable
	}
	accounts, reasonCode := s.decryptAccounts(material)
	if reasonCode != "" {
		s.markReplacementFailed(ctx, replacement.ID)
		return nil, ErrDeliveryReplacementFailed
	}
	expiresAt := s.now().AddDate(0, 0, material.DurationDays)
	msg := s.buildReplacementMessage(order, findOrderItemByID(order, delivery.OrderItemID), accounts, expiresAt, reason)
	sendCtx, cancel := context.WithTimeout(ctx, deliverySendTimeout)
	defer cancel()
	if err := s.mail.Send(sendCtx, msg); err != nil {
		log.Printf("delivery: replacement email for order %s delivery %s failed: %v", order.ID, delivery.ID, err)
		s.markReplacementFailed(ctx, replacement.ID)
		return nil, ErrDeliveryReplacementFailed
	}
	if err := s.repo.MarkDeliveryReplacementSent(ctx, replacement.ID, s.now()); err != nil {
		log.Printf("delivery: mark replacement %s sent failed: %v", replacement.ID, err)
	}
	s.record(ctx, actor, order, delivery, AuditOrderDeliveryReplaced, map[string]any{
		"order_id": order.ID, "order_item_id": delivery.OrderItemID,
		"delivery_id": delivery.ID, "replacement_id": replacement.ID,
		"old_inventory_account_id": replacement.OldInventoryAccountID,
		"inventory_account_id":     replacement.InventoryAccountID,
		"complaint_approved":       complaintApproved,
	})
	return s.repo.GetOrderDelivery(ctx, order.ID, delivery.ID)
}

func (s *OrderDeliveryService) markReplacementFailed(ctx context.Context, replacementID string) {
	if err := s.repo.MarkDeliveryReplacementFailed(ctx, replacementID, DeliveryErrorEmailFailed, s.now()); err != nil {
		log.Printf("delivery: mark replacement %s failed: %v", replacementID, err)
	}
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
	if material.DurationDays <= 0 {
		s.fail(ctx, actor, order, delivery, DeliveryErrorDurationUnavailable, isRetry)
		return
	}
	// Keep the current account only when it is still usable and has not been
	// sent to this buyer for the same product. Otherwise move the reserved
	// slots to an unused account before any credentials are emailed.
	if err := s.repo.PrepareDeliveryAssignment(ctx, order.ID, item.ID, now); err != nil {
		if errors.Is(err, ErrNoAlternativeAccount) {
			s.fail(ctx, actor, order, delivery, DeliveryErrorNoAlternativeAccount, isRetry)
			return
		}
		log.Printf("delivery: prepare assignment for order %s item %s failed: %v", order.ID, item.ID, err)
		s.fail(ctx, actor, order, delivery, DeliveryErrorInventoryMissing, isRetry)
		return
	}
	material, err = s.repo.LoadDeliveryMaterial(ctx, order.ID, item.ID)
	if err != nil {
		log.Printf("delivery: reload material for order %s item %s failed: %v", order.ID, item.ID, err)
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

	accessExpiresAt := now.AddDate(0, 0, material.DurationDays)
	msg := s.buildMessage(order, item, accounts, accessExpiresAt)
	sendCtx, cancel := context.WithTimeout(ctx, deliverySendTimeout)
	defer cancel()
	if err := s.mail.Send(sendCtx, msg); err != nil {
		// Mailer errors are transport-level. Never log the body or the buyer
		// address; the delivery row carries the stable error code instead.
		log.Printf("delivery: sending order %s item %s failed: %v", order.ID, item.ID, err)
		s.fail(ctx, actor, order, delivery, DeliveryErrorEmailFailed, isRetry)
		return
	}

	// The credentials are already on their way, so the buyer's access window is
	// anchored at the send time. COALESCE inside the repository keeps a retry
	// from extending it.
	if err := s.repo.ActivateDeliveryAccess(ctx, order.ID, item.ID, s.now(), accessExpiresAt); err != nil {
		log.Printf("delivery: activate access for order %s item %s failed: %v", order.ID, item.ID, err)
		s.fail(ctx, actor, order, delivery, DeliveryErrorPersistenceFailed, isRetry)
		return
	}

	if err := s.repo.MarkOrderDeliverySent(ctx, delivery.ID, s.now()); err != nil {
		log.Printf("delivery: mark sent %s failed: %v", delivery.ID, err)
		// The email left the process, so the row must not silently return to an
		// open state. Report the persistence problem for operator follow-up.
		s.fail(ctx, actor, order, delivery, DeliveryErrorPersistenceFailed, isRetry)
		return
	}
	s.syncOrderStatusAfterDelivery(ctx, order)
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

func (s *OrderDeliveryService) buildMessage(order *domain.Order, item *domain.OrderItem, accounts []deliveryAccount, accessExpiresAt time.Time) mailer.Message {
	var body strings.Builder
	body.WriteString("Halo " + order.Customer.Name + ",\n\n")
	body.WriteString("Akses untuk pesanan Anda sudah aktif. Berikut detail pesanan dan akun yang dapat digunakan.\n\n")
	writeDeliverySummary(&body, order, item, accessExpiresAt)
	body.WriteString("AKUN ANDA\n")
	writeDeliveryAccounts(&body, accounts)
	s.writeUsageInstructions(&body, item)
	body.WriteString("\nEmail ini tidak menerima balasan. Semua kendala ditangani melalui halaman Laporan Kendala.\n")
	body.WriteString("— NECLY Services\n")
	return mailer.Message{
		To:      order.Customer.Email,
		Subject: "Akses pesanan " + order.Number + " — " + item.ProductName,
		Body:    body.String(),
	}
}

// buildReplacementMessage is deliberately separate from the first-delivery
// email: it explains why the account changed and never implies a new order or
// payment was created.
func (s *OrderDeliveryService) buildReplacementMessage(order *domain.Order, item *domain.OrderItem, accounts []deliveryAccount, accessExpiresAt time.Time, reason string) mailer.Message {
	var body strings.Builder
	body.WriteString("Halo " + order.Customer.Name + ",\n\n")
	body.WriteString("Akun untuk pesanan Anda telah diganti karena masa aktif akun sebelumnya berakhir atau keluhan Anda disetujui.\n\n")
	writeDeliverySummary(&body, order, item, accessExpiresAt)
	fmt.Fprintf(&body, "Alasan penggantian : %s\n\n", reason)
	body.WriteString("AKUN PENGGANTI\n")
	writeDeliveryAccounts(&body, accounts)
	s.writeUsageInstructions(&body, item)
	body.WriteString("\nTidak ada tagihan baru untuk penggantian ini.\n")
	body.WriteString("Email ini tidak menerima balasan. Semua kendala ditangani melalui halaman Laporan Kendala.\n")
	body.WriteString("— NECLY Services\n")
	subject := "Akun pengganti pesanan " + order.Number
	if item != nil {
		subject += " — " + item.ProductName
	}
	return mailer.Message{To: order.Customer.Email, Subject: subject, Body: body.String()}
}

func writeDeliverySummary(body *strings.Builder, order *domain.Order, item *domain.OrderItem, accessExpiresAt time.Time) {
	body.WriteString("RINGKASAN PESANAN\n")
	fmt.Fprintf(body, "Nomor pesanan : %s\n", order.Number)
	if item != nil {
		fmt.Fprintf(body, "Produk        : %s\n", item.ProductName)
		fmt.Fprintf(body, "Durasi        : %s\n", item.DurationName)
		fmt.Fprintf(body, "Jumlah akses  : %d\n", item.Quantity)
	}
	fmt.Fprintf(body, "Masa akses    : sampai %s\n\n", accessExpiresAt.Format("2 January 2006"))
}

func writeDeliveryAccounts(body *strings.Builder, accounts []deliveryAccount) {
	for index, account := range accounts {
		fmt.Fprintf(body, "Akun %d — %s\n", index+1, account.Label)
		fmt.Fprintf(body, "  Email/username : %s\n", account.Username)
		fmt.Fprintf(body, "  Kata sandi     : %s\n", account.Password)
		if strings.TrimSpace(account.Instructions) != "" {
			fmt.Fprintf(body, "  Catatan        : %s\n", account.Instructions)
		}
		body.WriteString("\n")
	}
}

// platformLoginURL keeps the customer instructions concrete for the products
// the store sells. Unknown products fall back to the official app/site name
// without inventing a URL.
func platformLoginURL(productName string) string {
	name := strings.ToLower(strings.TrimSpace(productName))
	switch {
	case strings.Contains(name, "chatgpt"):
		return "https://chatgpt.com"
	case strings.Contains(name, "netflix"):
		return "https://www.netflix.com/login"
	case strings.Contains(name, "disney"):
		return "https://www.disneyplus.com/login"
	case strings.Contains(name, "spotify"):
		return "https://accounts.spotify.com/login"
	case strings.Contains(name, "apple music"):
		return "https://music.apple.com"
	case strings.Contains(name, "microsoft") || strings.Contains(name, "office") || strings.Contains(name, "365"):
		return "https://www.office.com"
	case strings.Contains(name, "canva"):
		return "https://www.canva.com/login"
	case strings.Contains(name, "google one"):
		return "https://one.google.com"
	case strings.Contains(name, "dropbox"):
		return "https://www.dropbox.com/login"
	default:
		return ""
	}
}

func (s *OrderDeliveryService) writeUsageInstructions(body *strings.Builder, item *domain.OrderItem) {
	productName := "layanan yang dibeli"
	if item != nil && strings.TrimSpace(item.ProductName) != "" {
		productName = strings.TrimSpace(item.ProductName)
	}
	body.WriteString("Cara penggunaan dan login:\n")
	step := 1
	if loginURL := platformLoginURL(productName); loginURL != "" {
		fmt.Fprintf(body, "%d. Buka %s (%s).\n", step, productName, loginURL)
	} else {
		fmt.Fprintf(body, "%d. Buka situs atau aplikasi resmi %s.\n", step, productName)
	}
	step++
	body.WriteString(fmt.Sprintf("%d. Pilih menu Masuk/Login, lalu masukkan email/username dan kata sandi di atas.\n", step))
	step++
	body.WriteString(fmt.Sprintf("%d. Jangan mengubah kata sandi, email, atau profil akun karena akun digunakan bersama.\n", step))
	step++
	if s.accessCodes != nil {
		if s.frontendURL != "" {
			fmt.Fprintf(body, "%d. Jika platform meminta kode OTP, buka %s/otp dan masukkan email akun serta kode akses yang diberikan admin.\n", step, s.frontendURL)
		} else {
			fmt.Fprintf(body, "%d. Jika platform meminta kode OTP, gunakan halaman Ambil OTP di situs Necly dengan kode akses dari admin.\n", step)
		}
		step++
	}
	if s.frontendURL != "" {
		fmt.Fprintf(body, "%d. Jika tidak bisa masuk, buat laporan di %s/laporan-kendala dengan menyertakan nomor pesanan.\n", step, s.frontendURL)
	} else {
		fmt.Fprintf(body, "%d. Jika tidak bisa masuk, buat laporan melalui menu Laporan Kendala di situs Necly dengan menyertakan nomor pesanan.\n", step)
	}
}

// syncOrderStatusAfterDelivery keeps the buyer-facing tracking in step with the
// delivery ledger. A partially delivered order becomes "Dikirim", and the last
// successful email moves the order to "Akses diterima" (delivered). The email
// has already left the process, so a rejected transition is logged and never
// turns a successful delivery into a failure.
func (s *OrderDeliveryService) syncOrderStatusAfterDelivery(ctx context.Context, order *domain.Order) {
	deliveries, err := s.repo.ListOrderDeliveries(ctx, order.ID)
	if err != nil {
		log.Printf("delivery: list deliveries for order status %s failed: %v", order.ID, err)
		return
	}
	if len(deliveries) == 0 {
		return
	}
	anySent := false
	allSent := true
	for _, delivery := range deliveries {
		if delivery.Status == domain.DeliverySent {
			anySent = true
			continue
		}
		allSent = false
	}
	if !anySent {
		return
	}

	// The in-memory order is a snapshot from before this batch. A multi-item
	// order may already have moved to shipped after the previous item, so read
	// the current status before deciding which transition is valid.
	currentStatus := order.Status
	if fresh, err := s.orders.GetOrderByID(ctx, order.ID); err == nil && fresh != nil {
		currentStatus = fresh.Status
	}

	if !allSent {
		if currentStatus == domain.OrderProcessing {
			if _, err := s.orders.TransitionOrderStatus(ctx, order.ID, domain.OrderShipped, s.now()); err != nil {
				log.Printf("delivery: transition order %s to shipped failed: %v", order.ID, err)
			}
		}
		return
	}

	// Delivered is reachable from processing only through shipped in the
	// shared state machine, so a single-item order may need both transitions.
	if currentStatus == domain.OrderProcessing {
		if _, err := s.orders.TransitionOrderStatus(ctx, order.ID, domain.OrderShipped, s.now()); err != nil {
			log.Printf("delivery: transition order %s to shipped failed: %v", order.ID, err)
			return
		}
		currentStatus = domain.OrderShipped
	}
	if currentStatus == domain.OrderShipped {
		if _, err := s.orders.TransitionOrderStatus(ctx, order.ID, domain.OrderDelivered, s.now()); err != nil {
			log.Printf("delivery: transition order %s to delivered failed: %v", order.ID, err)
		}
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

// deliverySelectableStatus allows the dashboard to prepare an assignment while
// the order is still paid/fulfilling. Sending still requires an approved
// fulfilment state.
func deliverySelectableStatus(status domain.OrderStatus) bool {
	return status == domain.OrderPaid || status == domain.OrderFulfilling || deliveryApprovedStatus(status)
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

func findOrderItemByID(order *domain.Order, itemID string) *domain.OrderItem {
	return findOrderItem(order, itemID)
}

// IsDeliveryApprovedStatus is exported for the handler so it can distinguish a
// missing approval from an infrastructure failure without duplicating the list.
func IsDeliveryApprovedStatus(status domain.OrderStatus) bool {
	return deliveryApprovedStatus(status)
}
