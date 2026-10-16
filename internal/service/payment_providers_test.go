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

func TestXenditVerifyPaymentSessionWebhook(t *testing.T) {
	provider := NewXenditProvider("callback-token", "xendit-api-key", false)
	body := []byte(`{
		"event":"payment_session.completed",
		"created":"2026-10-16T12:00:01Z",
		"data":{
			"payment_session_id":"ps-123",
			"reference_id":"order-1",
			"status":"COMPLETED",
			"updated":"2026-10-16T12:00:00Z"
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

func TestXenditRejectsInvalidWebhookToken(t *testing.T) {
	provider := NewXenditProvider("callback-token", "xendit-api-key", false)
	_, err := provider.VerifyWebhook(map[string]string{"X-Callback-Token": "wrong-token"}, []byte(`{}`))
	if err != ErrUnauthorized {
		t.Fatalf("VerifyWebhook() error = %v, want ErrUnauthorized", err)
	}
}
