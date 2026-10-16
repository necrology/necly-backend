package service

import (
	"context"
	"encoding/json"
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
		"data":{"id":"ps-999","reference_id":"order-2","status":"EXPIRED","updated":"2026-10-16T13:00:00Z"}
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
		"data":{"payment_id":"py-1","payment_session_id":"ps-555","reference_id":"order-3","status":"SUCCEEDED","updated":"2026-10-16T12:30:00Z"}
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
	body := []byte(`{"id":"inv-1","external_id":"order-4","status":"PAID","paid_at":"2026-10-16T11:00:00Z"}`)

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
