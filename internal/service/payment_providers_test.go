package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMidtransCreatePaymentPostsSnapTransactionAndParsesResponse(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/snap/v1/transactions" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		username, password, ok := r.BasicAuth()
		if !ok || username != "server-key" || password != "" {
			t.Fatalf("unexpected basic authentication")
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"snap-token","redirect_url":"https://pay.example/checkout"}`))
	}))
	defer server.Close()

	provider := NewMidtransProvider("server-key", "client-key", false,
		WithPaymentBaseURL(server.URL),
		WithPaymentHTTPClient(server.Client()),
	)
	result, err := provider.CreatePayment(context.Background(), PaymentRequest{
		OrderID: "order-1", Amount: 125000, Currency: "IDR", CustomerID: "user-1",
	})
	if err != nil {
		t.Fatalf("CreatePayment() error = %v", err)
	}
	transaction, ok := received["transaction_details"].(map[string]any)
	if !ok || transaction["order_id"] != "order-1" || transaction["gross_amount"] != float64(125000) {
		t.Fatalf("transaction_details = %#v", received["transaction_details"])
	}
	if result.ProviderPaymentID != "snap-token" || result.Status != "pending" || result.CheckoutURL != "https://pay.example/checkout" {
		t.Fatalf("result = %#v", result)
	}
}

func TestXenditCreatePaymentPostsSessionAndParsesResponse(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/sessions" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		username, password, ok := r.BasicAuth()
		if !ok || username != "xendit-api-key" || password != "" {
			t.Fatalf("unexpected basic authentication")
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"payment_session_id":"ps-123","payment_link_url":"https://dev.xen.to/session-123","status":"ACTIVE","reference_id":"order-1"}`))
	}))
	defer server.Close()

	provider := NewXenditProvider("callback-token", "xendit-api-key", false,
		WithPaymentBaseURL(server.URL),
		WithPaymentHTTPClient(server.Client()),
	)
	provider.returnBaseURL = "https://necly.example"
	result, err := provider.CreatePayment(context.Background(), PaymentRequest{
		OrderID: "order-1", OrderNumber: "NCL-001", Amount: 125000, Currency: "IDR", CustomerID: "user-1",
	})
	if err != nil {
		t.Fatalf("CreatePayment() error = %v", err)
	}
	if received["reference_id"] != "order-1" || received["amount"] != float64(125000) || received["currency"] != "IDR" || received["session_type"] != "PAY" || received["mode"] != "PAYMENT_LINK" {
		t.Fatalf("request body = %#v", received)
	}
	if received["success_return_url"] != "https://necly.example/payment/order-1?result=success" || received["cancel_return_url"] != "https://necly.example/payment/order-1?result=cancel" {
		t.Fatalf("return urls = %#v %#v", received["success_return_url"], received["cancel_return_url"])
	}
	if result.ProviderPaymentID != "ps-123" || result.Status != "pending" || result.CheckoutURL != "https://dev.xen.to/session-123" {
		t.Fatalf("result = %#v", result)
	}
}

// The payload below is the shape Xendit actually delivers for a
// payment_session.* event, captured from production. The earlier fixture used
// data.payment_session_id, which only payment.* events carry — so the suite
// stayed green while every real payment_session callback was rejected with 400.
func TestXenditVerifyPaymentSessionWebhook(t *testing.T) {
	provider := NewXenditProvider("callback-token", "xendit-api-key", false)
	body := []byte(`{
		"event":"payment_session.completed",
		"business_id":"biz-1",
		"created":"2026-10-16T12:00:01Z",
		"data":{
			"id":"ps-123",
			"reference_id":"order-1",
			"status":"COMPLETED",
			"updated":"2026-10-16T12:00:00Z",
			"payment_request_id":"pr-1",
			"payment_link_url":"https://checkout.xendit.co/web/ps-123",
			"amount":149000,
			"currency":"IDR"
		}
	}`)

	event, err := provider.VerifyWebhook(map[string]string{"x-callback-token": "callback-token"}, body)
	if err != nil {
		t.Fatalf("VerifyWebhook() error = %v", err)
	}
	if event.ProviderPaymentID != "ps-123" || event.OrderID != "order-1" || event.Status != "paid" {
		t.Fatalf("event = %#v", event)
	}
	if event.Amount != 149000 || event.Currency != "IDR" {
		t.Fatalf("amount/currency = %d/%s, want 149000/IDR", event.Amount, event.Currency)
	}
	if event.PaidAt.Format(time.RFC3339) != "2026-10-16T12:00:00Z" {
		t.Fatalf("paid at = %s", event.PaidAt)
	}
}

// An expired session has to map through so the order is closed and its reserved
// stock released; it used to be rejected before ever reaching the order.
func TestXenditVerifyExpiredPaymentSessionWebhook(t *testing.T) {
	provider := NewXenditProvider("callback-token", "xendit-api-key", false)
	body := []byte(`{
		"event":"payment_session.expired",
		"created":"2026-10-16T13:00:01Z",
		"data":{"id":"ps-999","reference_id":"order-2","status":"EXPIRED","updated":"2026-10-16T13:00:00Z","amount":99000,"currency":"IDR"}
	}`)

	event, err := provider.VerifyWebhook(map[string]string{"x-callback-token": "callback-token"}, body)
	if err != nil {
		t.Fatalf("VerifyWebhook() error = %v", err)
	}
	if event.ProviderPaymentID != "ps-999" || event.OrderID != "order-2" || event.Status != "expired" {
		t.Fatalf("event = %#v", event)
	}
}

// payment.* events describe a payment and reference the session instead.
func TestXenditVerifyPaymentEventWebhook(t *testing.T) {
	provider := NewXenditProvider("callback-token", "xendit-api-key", false)
	body := []byte(`{
		"event":"payment.succeeded",
		"created":"2026-10-16T12:30:01Z",
		"data":{"payment_id":"py-1","payment_session_id":"ps-555","reference_id":"order-3","status":"SUCCEEDED","updated":"2026-10-16T12:30:00Z","amount":125000,"currency":"IDR"}
	}`)

	event, err := provider.VerifyWebhook(map[string]string{"x-callback-token": "callback-token"}, body)
	if err != nil {
		t.Fatalf("VerifyWebhook() error = %v", err)
	}
	if event.ProviderPaymentID != "ps-555" || event.OrderID != "order-3" || event.Status != "paid" {
		t.Fatalf("event = %#v", event)
	}
}

// The legacy invoice callback keeps its identifiers at the top level.
func TestXenditVerifyLegacyInvoiceWebhook(t *testing.T) {
	provider := NewXenditProvider("callback-token", "xendit-api-key", false)
	body := []byte(`{"id":"inv-1","external_id":"order-4","status":"PAID","paid_at":"2026-10-16T11:00:00Z","amount":75000,"currency":"IDR"}`)

	event, err := provider.VerifyWebhook(map[string]string{"x-callback-token": "callback-token"}, body)
	if err != nil {
		t.Fatalf("VerifyWebhook() error = %v", err)
	}
	if event.ProviderPaymentID != "inv-1" || event.OrderID != "order-4" || event.Status != "paid" {
		t.Fatalf("event = %#v", event)
	}
}

func TestXenditRejectsInvalidWebhookToken(t *testing.T) {
	provider := NewXenditProvider("callback-token", "xendit-api-key", false)
	_, err := provider.VerifyWebhook(map[string]string{"X-Callback-Token": "wrong-token"}, []byte(`{}`))
	if err != ErrUnauthorized {
		t.Fatalf("VerifyWebhook() error = %v, want ErrUnauthorized", err)
	}
}

// Midtrans documents the notification signature as
// SHA512(order_id + status_code + gross_amount + ServerKey), carried in the
// JSON body as signature_key. These fixtures mirror that contract, including
// the decimal gross_amount shape used by real settlement notifications.
func midtransFixture(t *testing.T, transactionStatus, fraudStatus, grossAmount, settlementTime string) []byte {
	t.Helper()
	const orderID = "2f1d4a6e-4f0c-4c1b-9d0e-7a3b5c8d9e01"
	const statusCode = "200"
	signature := generateMidtransSignature(orderID, statusCode, grossAmount, "server-key")
	body := map[string]any{
		"transaction_status": transactionStatus,
		"status_code":        statusCode,
		"transaction_id":     "57d5293c-e65f-4a29-95e4-5959c3fa335b",
		"order_id":           orderID,
		"gross_amount":       grossAmount,
		"signature_key":      signature,
	}
	if fraudStatus != "" {
		body["fraud_status"] = fraudStatus
	}
	if settlementTime != "" {
		body["settlement_time"] = settlementTime
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestMidtransVerifySettlementFixture(t *testing.T) {
	provider := NewMidtransProvider("server-key", "client-key", false)
	body := midtransFixture(t, "settlement", "accept", "154600.00", "2021-06-15 18:45:28")
	event, err := provider.VerifyWebhook(nil, body)
	if err != nil {
		t.Fatalf("VerifyWebhook() error = %v", err)
	}
	if event.Provider != "midtrans" || event.Status != "paid" {
		t.Fatalf("event = %#v", event)
	}
	if event.OrderID != "2f1d4a6e-4f0c-4c1b-9d0e-7a3b5c8d9e01" || event.ProviderPaymentID != "57d5293c-e65f-4a29-95e4-5959c3fa335b" {
		t.Fatalf("identity = %#v", event)
	}
	if event.Amount != 154600 || event.Currency != "IDR" {
		t.Fatalf("amount/currency = %d/%s", event.Amount, event.Currency)
	}
	if event.PaidAt.Format(time.RFC3339) != "2021-06-15T11:45:28Z" {
		t.Fatalf("paid at = %s", event.PaidAt.Format(time.RFC3339))
	}
	if event.ID == "" || len(event.ID) > 255 {
		t.Fatalf("unstable event key = %q", event.ID)
	}
}

func TestMidtransEventKeyDistinguishesStatusesAndDeduplicatesReplays(t *testing.T) {
	provider := NewMidtransProvider("server-key", "client-key", false)
	pending := midtransFixture(t, "pending", "", "154600.00", "")
	settlement := midtransFixture(t, "settlement", "accept", "154600.00", "2021-06-15 18:45:28")

	pendingEvent, err := provider.VerifyWebhook(nil, pending)
	if err != nil {
		t.Fatal(err)
	}
	settlementEvent, err := provider.VerifyWebhook(nil, settlement)
	if err != nil {
		t.Fatal(err)
	}
	if pendingEvent.ID == settlementEvent.ID {
		t.Fatal("different statuses produced the same event key; later status would be dropped as a duplicate")
	}
	replayed, err := provider.VerifyWebhook(nil, settlement)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ID != settlementEvent.ID {
		t.Fatalf("identical notification changed event key: %q != %q", replayed.ID, settlementEvent.ID)
	}
}

func TestMidtransVerifyCaptureChallengeStaysPending(t *testing.T) {
	provider := NewMidtransProvider("server-key", "client-key", false)
	event, err := provider.VerifyWebhook(nil, midtransFixture(t, "capture", "challenge", "10000", ""))
	if err != nil {
		t.Fatal(err)
	}
	if event.Status != "pending" {
		t.Fatalf("challenge status = %q, want pending", event.Status)
	}
}

func TestMidtransRejectsInvalidSignatureAndMissingFields(t *testing.T) {
	provider := NewMidtransProvider("server-key", "client-key", false)
	body := midtransFixture(t, "settlement", "accept", "154600.00", "")
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	payload["signature_key"] = "deadbeef"
	badSignature, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.VerifyWebhook(nil, badSignature); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("bad signature error = %v, want ErrUnauthorized", err)
	}
	if _, err := provider.VerifyWebhook(nil, []byte(`{"order_id":"x"}`)); !errors.Is(err, ErrInvalidPaymentEvent) {
		t.Fatalf("missing fields error = %v, want ErrInvalidPaymentEvent", err)
	}
}
