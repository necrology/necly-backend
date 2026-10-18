package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
)

type CheckoutItemInput struct {
	ProductPriceID string `json:"product_price_id"`
	Quantity       int    `json:"quantity"`
	// DeviceCount is how many devices the subscription should cover (QA baris
	// 38). Zero means the client did not send it, which is normalised to 1 so a
	// checkout that predates the picker still succeeds.
	DeviceCount int `json:"device_count"`
}

type CheckoutInput struct {
	UserID string `json:"-"`
	// Normally supplied as an Idempotency-Key header. The JSON tag stays so a
	// client that sends it in the body still works.
	IdempotencyKey string              `json:"idempotency_key"`
	VoucherCode    string              `json:"voucher_code"`
	Items          []CheckoutItemInput `json:"items"`
	// ExpectedSubtotal lets the storefront pin the amount it displayed. It is
	// deliberately excluded from the idempotency fingerprint.
	ExpectedSubtotal *int64 `json:"expected_subtotal"`
}

type CheckoutCommand struct {
	UserID           string
	IdempotencyKey   string
	VoucherCode      string
	Items            []CheckoutItemInput
	Fingerprint      string
	ExpectedSubtotal *int64
	Now              time.Time
	PaymentDueAt     time.Time
}

type PaymentRequest struct {
	OrderID     string
	OrderNumber string
	Amount      int64
	Currency    string
	CustomerID  string
	ExpiresAt   time.Time
}

type PaymentInitiation struct {
	ProviderPaymentID string
	Status            string
	CheckoutURL       string
}

type PaymentEvent struct {
	ID                string
	Provider          string
	ProviderPaymentID string
	OrderID           string
	Status            string
	PaidAt            time.Time
	Raw               []byte
}

type PaymentProvider interface {
	Name() string
	CreatePayment(ctx context.Context, request PaymentRequest) (*PaymentInitiation, error)
	VerifyWebhook(headers map[string]string, body []byte) (*PaymentEvent, error)
}

// CheckoutState is the outcome of the reservation step.
type CheckoutState string

const (
	// CheckoutReserved means this request owns the reservation and should
	// perform the provider call.
	CheckoutReserved CheckoutState = "reserved"
	// CheckoutReplay means a stored logical checkout already exists. When a
	// payment is present the identical body is returned without provider I/O.
	CheckoutReplay CheckoutState = "replay"
	// CheckoutInProgress means another request with the same owner/key is
	// still working. The caller waits and then reads the stored result.
	CheckoutInProgress CheckoutState = "in_progress"
)

type CheckoutReservation struct {
	State   CheckoutState
	Order   *domain.Order
	Payment *domain.Payment
	Attempt *domain.CheckoutAttempt
}

type CheckoutProgress struct {
	Order   *domain.Order
	Payment *domain.Payment
	Attempt *domain.CheckoutAttempt
}

type CheckoutRepository interface {
	// BeginCheckout either returns the stored logical checkout, claims a new
	// reservation, or reports that a concurrent request owns it.
	BeginCheckout(ctx context.Context, command CheckoutCommand) (*CheckoutReservation, error)
	// ClaimProviderCall atomically moves an attempt to the creating state. It
	// never holds a transaction across the provider call.
	ClaimProviderCall(ctx context.Context, attemptID string, now time.Time) (bool, error)
	SaveProviderResult(ctx context.Context, attemptID, provider, providerPaymentID, checkoutURL string, now time.Time) error
	CompleteCheckout(ctx context.Context, attemptID string, payment *domain.Payment) (*domain.Payment, error)
	FailCheckout(ctx context.Context, attemptID, errorCode string, now time.Time) error
	GetCheckoutProgress(ctx context.Context, userID, idempotencyKey string) (*CheckoutProgress, error)
	CancelAndRelease(ctx context.Context, orderID string, now time.Time) error
}

type CheckoutResult struct {
	Order       domain.Order   `json:"order"`
	Payment     domain.Payment `json:"payment"`
	CheckoutURL string         `json:"checkout_url"`
	// Replayed is never serialized: the handler uses it to choose 200 vs 201.
	Replayed bool `json:"-"`
}

// attemptRecoveryLease keeps a concurrent waiter from stealing a fresh
// reservation. Only an attempt that has not moved for this long is recovered
// by another request (for example after its process died).
const attemptRecoveryLease = 30 * time.Second

var (
	ErrIdempotencyConflict = errors.New("Kunci idempotensi sudah digunakan untuk permintaan checkout yang berbeda")
	ErrCheckoutInProgress  = errors.New("Checkout dengan kunci yang sama masih diproses")
	ErrCheckoutFailed      = errors.New("Checkout sebelumnya gagal; gunakan kunci idempotensi baru")
)

// PriceChangedError reports the server subtotal when the caller pinned a
// different displayed amount. The caller must review the new amount.
type PriceChangedError struct {
	CurrentSubtotal int64
	Currency        string
}

func (e *PriceChangedError) Error() string {
	return "Harga pesanan berubah; silakan periksa total terbaru"
}

type CheckoutService struct {
	repo       CheckoutRepository
	provider   PaymentProvider
	paymentTTL time.Duration
	now        func() time.Time
}

func NewCheckoutService(repo CheckoutRepository, provider PaymentProvider, paymentTTL time.Duration) *CheckoutService {
	return &CheckoutService{repo: repo, provider: provider, paymentTTL: paymentTTL, now: time.Now}
}

// CanonicalCheckoutFingerprint implements the C-01 contract: lower-case UUIDs,
// items sorted by product_price_id then device_count, trimmed/uppercased
// voucher, lower-case channel, compact JSON, SHA-256 hex. expected_subtotal is
// excluded on purpose.
func CanonicalCheckoutFingerprint(items []CheckoutItemInput, voucherCode, paymentChannel string) string {
	type fingerprintItem struct {
		DeviceCount    int    `json:"device_count"`
		ProductPriceID string `json:"product_price_id"`
		Quantity       int    `json:"quantity"`
	}
	type fingerprintPayload struct {
		Items          []fingerprintItem `json:"items"`
		PaymentChannel string            `json:"payment_channel"`
		VoucherCode    string            `json:"voucher_code"`
	}

	normalised := make([]fingerprintItem, 0, len(items))
	for _, item := range items {
		normalised = append(normalised, fingerprintItem{
			DeviceCount:    item.DeviceCount,
			ProductPriceID: strings.ToLower(strings.TrimSpace(item.ProductPriceID)),
			Quantity:       item.Quantity,
		})
	}
	sort.Slice(normalised, func(i, j int) bool {
		if normalised[i].ProductPriceID != normalised[j].ProductPriceID {
			return normalised[i].ProductPriceID < normalised[j].ProductPriceID
		}
		return normalised[i].DeviceCount < normalised[j].DeviceCount
	})

	payload := fingerprintPayload{
		Items:          normalised,
		PaymentChannel: strings.ToLower(strings.TrimSpace(paymentChannel)),
		VoucherCode:    strings.ToUpper(strings.TrimSpace(voucherCode)),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		// The payload only contains strings and integers, so Marshal cannot
		// fail in practice. Hashing the empty string would silently weaken the
		// idempotency guarantee, so make the impossible case explicit.
		panic(fmt.Sprintf("canonical checkout fingerprint: %v", err))
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (s *CheckoutService) Checkout(ctx context.Context, input CheckoutInput) (*CheckoutResult, error) {
	input.UserID = strings.TrimSpace(input.UserID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.UserID == "" || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 || len(input.Items) == 0 || len(input.Items) > 50 {
		// Name the failing condition: a bare "Input tidak valid" left the caller
		// with no way to tell a missing idempotency key from an empty cart.
		log.Printf("checkout rejected: user_set=%t key_len=%d items=%d",
			input.UserID != "", len(input.IdempotencyKey), len(input.Items))
		return nil, ErrInvalidInput
	}
	seen := make(map[string]bool, len(input.Items))
	for i := range input.Items {
		input.Items[i].ProductPriceID = strings.TrimSpace(input.Items[i].ProductPriceID)
		if input.Items[i].DeviceCount == 0 {
			input.Items[i].DeviceCount = 1
		}
		item := input.Items[i]
		if item.ProductPriceID == "" || item.Quantity <= 0 || item.Quantity > 100 || item.DeviceCount < 1 || item.DeviceCount > 10 || seen[item.ProductPriceID] {
			log.Printf("checkout rejected: item[%d] price_id_set=%t qty=%d devices=%d duplicate=%t",
				i, item.ProductPriceID != "", item.Quantity, item.DeviceCount, seen[item.ProductPriceID])
			return nil, ErrInvalidInput
		}
		seen[item.ProductPriceID] = true
	}
	if input.ExpectedSubtotal != nil && *input.ExpectedSubtotal < 0 {
		return nil, ErrInvalidInput
	}

	// PostgreSQL stores timestamptz at microsecond precision. Truncating before
	// the first response makes the in-memory 201 body byte-identical to the
	// 200 body that is later read back from the database.
	now := s.now().UTC().Truncate(time.Microsecond)
	fingerprint := CanonicalCheckoutFingerprint(input.Items, input.VoucherCode, "")
	reservation, err := s.repo.BeginCheckout(ctx, CheckoutCommand{
		UserID: input.UserID, IdempotencyKey: input.IdempotencyKey,
		VoucherCode: strings.ToUpper(strings.TrimSpace(input.VoucherCode)),
		Items:       input.Items, Fingerprint: fingerprint, ExpectedSubtotal: input.ExpectedSubtotal,
		Now: now, PaymentDueAt: now.Add(s.paymentTTL),
	})
	if err != nil {
		return nil, err
	}

	switch reservation.State {
	case CheckoutReplay:
		if reservation.Payment != nil {
			return resultFrom(reservation.Order, *reservation.Payment, true), nil
		}
		// A previous process reserved the order but died before saving the
		// payment. Fall through to recovery.
	case CheckoutInProgress:
		return s.waitForProgress(ctx, input.UserID, input.IdempotencyKey)
	}
	return s.completeReservation(ctx, reservation, now, false)
}

func (s *CheckoutService) completeReservation(ctx context.Context, reservation *CheckoutReservation, now time.Time, alreadyClaimed bool) (*CheckoutResult, error) {
	order := reservation.Order
	attempt := reservation.Attempt
	if order == nil || attempt == nil {
		return s.waitForProgress(ctx, attemptUserID(attempt), attemptKey(attempt))
	}

	// A previous provider call may already have produced the reference. Saving
	// the payment from that stored result requires no network call and cannot
	// create a second logical charge.
	if attempt.ProviderPaymentID != nil && strings.TrimSpace(*attempt.ProviderPaymentID) != "" {
		payment := paymentFromAttempt(order, attempt, now)
		saved, err := s.repo.CompleteCheckout(ctx, attempt.ID, payment)
		if err != nil {
			return nil, err
		}
		return resultFrom(order, *saved, reservation.State == CheckoutReplay), nil
	}

	claimed := alreadyClaimed
	if !claimed {
		var err error
		claimed, err = s.repo.ClaimProviderCall(ctx, attempt.ID, now)
		if err != nil {
			return nil, err
		}
	}
	if !claimed {
		return s.waitForProgress(ctx, order.UserID, attempt.IdempotencyKey)
	}

	initiation, err := s.provider.CreatePayment(ctx, PaymentRequest{
		OrderID: order.ID, OrderNumber: order.Number, Amount: order.Total, Currency: order.Currency,
		CustomerID: order.UserID, ExpiresAt: order.PaymentDueAt,
	})
	if err != nil {
		_ = s.repo.FailCheckout(ctx, attempt.ID, "provider_error", now)
		_ = s.repo.CancelAndRelease(ctx, order.ID, now)
		return nil, fmt.Errorf("create payment: %w", err)
	}
	if initiation == nil || strings.TrimSpace(initiation.ProviderPaymentID) == "" {
		_ = s.repo.FailCheckout(ctx, attempt.ID, "provider_invalid_response", now)
		_ = s.repo.CancelAndRelease(ctx, order.ID, now)
		return nil, errors.New("Penyedia pembayaran mengembalikan respons yang tidak valid")
	}

	if err := s.repo.SaveProviderResult(ctx, attempt.ID, s.provider.Name(), initiation.ProviderPaymentID, initiation.CheckoutURL, now); err != nil {
		// The provider may already have accepted the charge. Never cancel the
		// order here; a later retry recovers from the stored reference.
		return nil, err
	}

	payment := domain.Payment{
		ID: uuid.NewString(), OrderID: order.ID, Provider: s.provider.Name(),
		ProviderPaymentID: initiation.ProviderPaymentID, Status: normalisePaymentStatus(initiation.Status),
		Amount: order.Total, CheckoutURL: initiation.CheckoutURL, CreatedAt: now, UpdatedAt: now,
	}
	saved, err := s.repo.CompleteCheckout(ctx, attempt.ID, &payment)
	if err != nil {
		// Same reasoning: the provider reference exists, so recovery is safe
		// and the order must not be cancelled.
		return nil, err
	}
	return resultFrom(order, *saved, false), nil
}

// waitForProgress polls the persisted attempt without holding any database
// transaction. It also takes over a stale reservation (for example after the
// original process died) using the atomic ClaimProviderCall lease.
func (s *CheckoutService) waitForProgress(ctx context.Context, userID, idempotencyKey string) (*CheckoutResult, error) {
	delay := 20 * time.Millisecond
	for {
		progress, err := s.repo.GetCheckoutProgress(ctx, userID, idempotencyKey)
		if err == nil && progress != nil {
			if progress.Order != nil && progress.Payment != nil {
				return resultFrom(progress.Order, *progress.Payment, true), nil
			}
			if progress.Attempt != nil {
				if progress.Attempt.State == "failed" {
					return nil, ErrCheckoutFailed
				}
				if progress.Attempt.ProviderPaymentID != nil && progress.Order != nil &&
					strings.TrimSpace(*progress.Attempt.ProviderPaymentID) != "" {
					now := s.now().UTC().Truncate(time.Microsecond)
					payment := paymentFromAttempt(progress.Order, progress.Attempt, now)
					saved, completeErr := s.repo.CompleteCheckout(ctx, progress.Attempt.ID, payment)
					if completeErr == nil {
						return resultFrom(progress.Order, *saved, true), nil
					}
				}
				now := s.now().UTC().Truncate(time.Microsecond)
				recoverable := progress.Attempt.State == "reserved" || progress.Attempt.State == "creating" || progress.Attempt.State == "started"
				if progress.Order != nil && recoverable && now.Sub(progress.Attempt.UpdatedAt) >= attemptRecoveryLease {
					claimed, claimErr := s.repo.ClaimProviderCall(ctx, progress.Attempt.ID, now)
					if claimErr == nil && claimed {
						// The lease was just acquired; completeReservation must
						// not try to claim the same attempt a second time.
						return s.completeReservation(ctx, &CheckoutReservation{
							State: CheckoutInProgress, Order: progress.Order, Attempt: progress.Attempt,
						}, now, true)
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil, ErrCheckoutInProgress
		case <-time.After(delay):
		}
		if delay < 250*time.Millisecond {
			delay *= 2
		}
	}
}

func resultFrom(order *domain.Order, payment domain.Payment, replayed bool) *CheckoutResult {
	result := &CheckoutResult{Payment: payment, CheckoutURL: payment.CheckoutURL, Replayed: replayed}
	if order != nil {
		result.Order = *order
	}
	return result
}

func paymentFromAttempt(order *domain.Order, attempt *domain.CheckoutAttempt, now time.Time) *domain.Payment {
	provider := ""
	if attempt.Provider != nil {
		provider = *attempt.Provider
	}
	providerPaymentID := ""
	if attempt.ProviderPaymentID != nil {
		providerPaymentID = *attempt.ProviderPaymentID
	}
	checkoutURL := ""
	if attempt.CheckoutURL != nil {
		checkoutURL = *attempt.CheckoutURL
	}
	return &domain.Payment{
		ID: uuid.NewString(), OrderID: order.ID, Provider: provider,
		ProviderPaymentID: providerPaymentID, Status: "pending", Amount: order.Total,
		CheckoutURL: checkoutURL, CreatedAt: now, UpdatedAt: now,
	}
}

func normalisePaymentStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		return "pending"
	}
	return status
}

func attemptUserID(attempt *domain.CheckoutAttempt) string {
	if attempt == nil {
		return ""
	}
	return attempt.UserID
}

func attemptKey(attempt *domain.CheckoutAttempt) string {
	if attempt == nil {
		return ""
	}
	return attempt.IdempotencyKey
}
