package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/handler"
	"github.com/necly/services/internal/service"
)

// checkoutHTTPRepository is a minimal in-memory CheckoutRepository used to
// assert the HTTP mapping of the C-01 error contract.
type checkoutHTTPRepository struct {
	beginErr  error
	order     *domain.Order
	attempt   *domain.CheckoutAttempt
	payment   *domain.Payment
	completed bool
}

func (r *checkoutHTTPRepository) BeginCheckout(_ context.Context, _ service.CheckoutCommand) (*service.CheckoutReservation, error) {
	if r.beginErr != nil {
		return nil, r.beginErr
	}
	if r.completed {
		return &service.CheckoutReservation{State: service.CheckoutReplay, Order: r.order, Payment: r.payment, Attempt: r.attempt}, nil
	}
	return &service.CheckoutReservation{State: service.CheckoutReserved, Order: r.order, Attempt: r.attempt}, nil
}

func (r *checkoutHTTPRepository) ClaimProviderCall(_ context.Context, _ string, _ time.Time) (bool, error) {
	return true, nil
}

func (r *checkoutHTTPRepository) SaveProviderResult(_ context.Context, _, _, _, _ string, _ time.Time) error {
	return nil
}

func (r *checkoutHTTPRepository) CompleteCheckout(_ context.Context, _ string, payment *domain.Payment) (*domain.Payment, error) {
	r.completed = true
	r.payment = payment
	return payment, nil
}

func (r *checkoutHTTPRepository) FailCheckout(_ context.Context, _, _ string, _ time.Time) error {
	return nil
}

func (r *checkoutHTTPRepository) GetCheckoutProgress(_ context.Context, _, _ string) (*service.CheckoutProgress, error) {
	return &service.CheckoutProgress{Order: r.order, Payment: r.payment, Attempt: r.attempt}, nil
}

func (r *checkoutHTTPRepository) CancelAndRelease(_ context.Context, _ string, _ time.Time) error {
	return nil
}

type checkoutHTTPProvider struct{}

func (checkoutHTTPProvider) Name() string { return "mock" }
func (checkoutHTTPProvider) CreatePayment(_ context.Context, request service.PaymentRequest) (*service.PaymentInitiation, error) {
	return &service.PaymentInitiation{
		ProviderPaymentID: "pay_" + request.OrderID,
		Status:            "pending",
		CheckoutURL:       "https://pay.mock/" + request.OrderID,
	}, nil
}
func (checkoutHTTPProvider) VerifyWebhook(_ map[string]string, _ []byte) (*service.PaymentEvent, error) {
	return nil, nil
}

func checkoutTestApp(t *testing.T, repo *checkoutHTTPRepository) (*fiber.App, string) {
	t.Helper()
	tokens, err := service.NewTokenManager("handler-test-secret-at-least-32-bytes", "test", time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	checkout := service.NewCheckoutService(repo, checkoutHTTPProvider{}, time.Minute)
	router := handler.NewRouter(nil, nil, nil, checkout, nil, nil, nil, nil, nil, nil, nil, nil, nil, tokens, nil, nil, nil, nil)
	app := fiber.New()
	app.Use(recover.New())
	router.Register(app)
	token, err := tokens.IssueAccess("0192f0c0-0000-0000-0000-000000000001", "customer")
	if err != nil {
		t.Fatal(err)
	}
	return app, token
}

func checkoutRequest(t *testing.T, app *fiber.App, token, body string) (int, map[string]any) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/checkout", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "fixture-checkout-0001")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := app.Test(request, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	decoded := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("decode response %q: %v", raw, err)
		}
	}
	return response.StatusCode, decoded
}

func newCheckoutHTTPRepository() *checkoutHTTPRepository {
	now := time.Now().UTC()
	orderID := "0192f0b0-0000-0000-0000-000000000001"
	return &checkoutHTTPRepository{
		order: &domain.Order{
			ID: orderID, Number: "ORD-1760832000000-0192f0b0", UserID: "0192f0c0-0000-0000-0000-000000000001",
			Status: domain.OrderPending, Subtotal: 186000, Total: 186000, Currency: "IDR",
			PaymentDueAt: now.Add(15 * time.Minute), CreatedAt: now, UpdatedAt: now,
		},
		attempt: &domain.CheckoutAttempt{
			ID: "0192f0b5-0000-0000-0000-000000000001", UserID: "0192f0c0-0000-0000-0000-000000000001",
			IdempotencyKey: "fixture-checkout-0001", State: "reserved", OrderID: &orderID,
		},
	}
}

func TestCheckoutHTTPMapsErrorContract(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"invalid input", service.ErrInvalidInput, http.StatusBadRequest, "invalid_input"},
		{"invalid voucher", service.ErrNotFound, http.StatusNotFound, "not_found"},
		{"out of stock", service.ErrOutOfStock, http.StatusConflict, "out_of_stock"},
		{"idempotency conflict", service.ErrIdempotencyConflict, http.StatusConflict, "idempotency_conflict"},
		{"in progress", service.ErrCheckoutInProgress, http.StatusConflict, "idempotency_conflict"},
		{"price changed", &service.PriceChangedError{CurrentSubtotal: 186000, Currency: "IDR"}, http.StatusConflict, "price_changed"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			repo := newCheckoutHTTPRepository()
			repo.beginErr = testCase.err
			app, token := checkoutTestApp(t, repo)
			status, body := checkoutRequest(t, app, token, `{"items":[{"product_price_id":"0192f0a4-0000-0000-0000-000000000001","quantity":1}]}`)
			if status != testCase.wantStatus {
				t.Fatalf("status = %d, want %d (%v)", status, testCase.wantStatus, body)
			}
			if body["code"] != testCase.wantCode {
				t.Fatalf("code = %v, want %s", body["code"], testCase.wantCode)
			}
			if testCase.wantCode == "price_changed" {
				if body["current_subtotal"] != float64(186000) || body["currency"] != "IDR" {
					t.Fatalf("price change body = %v", body)
				}
			}
		})
	}
}

func TestCheckoutHTTPReturns201Then200OnReplay(t *testing.T) {
	repo := newCheckoutHTTPRepository()
	app, token := checkoutTestApp(t, repo)
	body := `{"items":[{"product_price_id":"0192f0a4-0000-0000-0000-000000000001","quantity":1}]}`
	status, decoded := checkoutRequest(t, app, token, body)
	if status != http.StatusCreated {
		t.Fatalf("first status = %d, want 201 (%v)", status, decoded)
	}
	if decoded["order"] == nil || decoded["payment"] == nil || decoded["checkout_url"] == nil {
		t.Fatalf("checkout fixture fields missing: %v", decoded)
	}
	status, decoded = checkoutRequest(t, app, token, body)
	if status != http.StatusOK {
		t.Fatalf("replay status = %d, want 200 (%v)", status, decoded)
	}
}
