package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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

func TestXenditCreatePaymentPostsInvoiceAndParsesResponse(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/invoices" {
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
		_, _ = w.Write([]byte(`{"id":"inv-123","invoice_url":"https://pay.xendit/inv-123","status":"PENDING","external_id":"order-1"}`))
	}))
	defer server.Close()

	provider := NewXenditProvider("callback-token", "xendit-api-key", false,
		WithPaymentBaseURL(server.URL),
		WithPaymentHTTPClient(server.Client()),
	)
	result, err := provider.CreatePayment(context.Background(), PaymentRequest{
		OrderID: "order-1", Amount: 125000, Currency: "IDR", CustomerID: "user-1",
	})
	if err != nil {
		t.Fatalf("CreatePayment() error = %v", err)
	}
	if received["external_id"] != "order-1" || received["amount"] != float64(125000) || received["currency"] != "IDR" {
		t.Fatalf("request body = %#v", received)
	}
	if result.ProviderPaymentID != "inv-123" || result.Status != "pending" || result.CheckoutURL != "https://pay.xendit/inv-123" {
		t.Fatalf("result = %#v", result)
	}
}
