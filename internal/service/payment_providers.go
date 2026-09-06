package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

type paymentProviderOption func(baseURL *string, client **http.Client)

func WithPaymentBaseURL(baseURL string) paymentProviderOption {
	return func(target *string, _ **http.Client) {
		*target = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	}
}

func WithPaymentHTTPClient(client *http.Client) paymentProviderOption {
	return func(_ *string, target **http.Client) {
		if client != nil {
			*target = client
		}
	}
}

type MidtransProvider struct {
	serverKey    string
	clientKey    string
	isProduction bool
	baseURL      string
	httpClient   *http.Client
}

func NewMidtransProvider(serverKey, clientKey string, isProduction bool, options ...paymentProviderOption) *MidtransProvider {
	baseURL := "https://app.sandbox.midtrans.com"
	if isProduction {
		baseURL = "https://app.midtrans.com"
	}
	client := &http.Client{Timeout: 10 * time.Second}
	for _, option := range options {
		option(&baseURL, &client)
	}
	return &MidtransProvider{
		serverKey:    serverKey,
		clientKey:    clientKey,
		isProduction: isProduction,
		baseURL:      baseURL,
		httpClient:   client,
	}
}

func (m *MidtransProvider) Name() string { return "midtrans" }

func (m *MidtransProvider) CreatePayment(ctx context.Context, req PaymentRequest) (*PaymentInitiation, error) {
	orderID := req.OrderID
	grossAmount := req.Amount

	snapReq := map[string]any{
		"transaction_details": map[string]any{
			"order_id":     orderID,
			"gross_amount": grossAmount,
		},
		"customer_details": map[string]any{
			"customer_id": req.CustomerID,
		},
		"enabled_payments": []string{"credit_card", "bca_va", "bni_va", "bri_va", "permata_va", "other_va", "gopay", "shopeepay", "other_qris"},
	}

	bodyBytes, err := json.Marshal(snapReq)
	if err != nil {
		return nil, fmt.Errorf("marshal snap request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/snap/v1/transactions", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create http request: %w", err)
	}
	httpReq.SetBasicAuth(m.serverKey, "")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := m.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("snap request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("snap request returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var snapResp struct {
		Token       string `json:"token"`
		RedirectURL string `json:"redirect_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&snapResp); err != nil {
		return nil, fmt.Errorf("decode snap response: %w", err)
	}

	if snapResp.Token == "" {
		return nil, fmt.Errorf("snap response missing token")
	}

	return &PaymentInitiation{
		ProviderPaymentID: snapResp.Token,
		Status:            "pending",
		CheckoutURL:       snapResp.RedirectURL,
	}, nil
}

func (m *MidtransProvider) VerifyWebhook(headers map[string]string, body []byte) (*PaymentEvent, error) {
	signatureKey := headers["X-Signature-Key"]
	if signatureKey == "" {
		return nil, ErrUnauthorized
	}

	var notification map[string]any
	if err := json.Unmarshal(body, &notification); err != nil {
		return nil, ErrInvalidInput
	}

	orderID := fmt.Sprintf("%v", notification["order_id"])
	statusCode := fmt.Sprintf("%v", notification["status_code"])
	grossAmount := fmt.Sprintf("%v", notification["gross_amount"])

	expectedSig := generateMidtransSignature(orderID, statusCode, grossAmount, m.serverKey)
	if !hmac.Equal([]byte(signatureKey), []byte(expectedSig)) {
		return nil, ErrUnauthorized
	}

	transactionStatus := fmt.Sprintf("%v", notification["transaction_status"])
	var status string
	switch transactionStatus {
	case "capture", "settlement":
		status = "paid"
	case "pending":
		status = "pending"
	case "deny", "cancel", "expire", "failure":
		status = "failed"
	default:
		status = "pending"
	}

	var paidAt time.Time
	if settlementTime, ok := notification["settlement_time"].(string); ok && settlementTime != "" {
		if t, err := time.Parse("2006-01-02 15:04:05", settlementTime); err == nil {
			paidAt = t
		}
	}

	return &PaymentEvent{
		ID:                uuid.NewString(),
		Provider:          "midtrans",
		ProviderPaymentID: fmt.Sprintf("%v", notification["transaction_id"]),
		OrderID:           orderID,
		Status:            status,
		PaidAt:            paidAt,
		Raw:               body,
	}, nil
}

func generateMidtransSignature(orderID, statusCode, grossAmount, serverKey string) string {
	data := orderID + statusCode + grossAmount + serverKey
	hash := sha512.Sum512([]byte(data))
	return hex.EncodeToString(hash[:])
}

type XenditProvider struct {
	callbackToken string
	apiKey        string
	isProduction  bool
	baseURL       string
	httpClient    *http.Client
}

func NewXenditProvider(callbackToken, apiKey string, isProduction bool, options ...paymentProviderOption) *XenditProvider {
	baseURL := "https://api.xendit.co"
	client := &http.Client{Timeout: 10 * time.Second}
	for _, option := range options {
		option(&baseURL, &client)
	}
	return &XenditProvider{
		callbackToken: callbackToken,
		apiKey:        apiKey,
		isProduction:  isProduction,
		baseURL:       baseURL,
		httpClient:    client,
	}
}

func (x *XenditProvider) Name() string { return "xendit" }

func (x *XenditProvider) CreatePayment(ctx context.Context, req PaymentRequest) (*PaymentInitiation, error) {
	externalID := req.OrderID
	amount := req.Amount

	reqBody := map[string]any{
		"external_id":      externalID,
		"amount":           amount,
		"currency":         req.Currency,
		"description":      "Order " + externalID,
		"invoice_duration": 86400,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal xendit request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, x.baseURL+"/v2/invoices", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create http request: %w", err)
	}
	httpReq.SetBasicAuth(x.apiKey, "")
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := x.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("xendit request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("xendit request returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var xenditResp struct {
		ID         string `json:"id"`
		InvoiceURL string `json:"invoice_url"`
		Status     string `json:"status"`
		ExternalID string `json:"external_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&xenditResp); err != nil {
		return nil, fmt.Errorf("decode xendit response: %w", err)
	}

	if xenditResp.ID == "" {
		return nil, fmt.Errorf("xendit response missing id")
	}

	return &PaymentInitiation{
		ProviderPaymentID: xenditResp.ID,
		Status:            strings.ToLower(xenditResp.Status),
		CheckoutURL:       xenditResp.InvoiceURL,
	}, nil
}

func (x *XenditProvider) VerifyWebhook(headers map[string]string, body []byte) (*PaymentEvent, error) {
	callbackToken := headers["X-Callback-Token"]
	if callbackToken == "" {
		callbackToken = headers["x-callback-token"]
	}
	if callbackToken != x.callbackToken {
		return nil, ErrUnauthorized
	}

	var notification map[string]any
	if err := json.Unmarshal(body, &notification); err != nil {
		return nil, ErrInvalidInput
	}

	externalID := fmt.Sprintf("%v", notification["external_id"])
	status := strings.ToLower(fmt.Sprintf("%v", notification["status"]))
	paymentID := fmt.Sprintf("%v", notification["id"])

	var mappedStatus string
	switch status {
	case "paid":
		mappedStatus = "paid"
	case "pending":
		mappedStatus = "pending"
	case "expired", "failed":
		mappedStatus = "failed"
	default:
		mappedStatus = "pending"
	}

	var paidAt time.Time
	if paidAtStr, ok := notification["paid_at"].(string); ok && paidAtStr != "" {
		if t, err := time.Parse(time.RFC3339, paidAtStr); err == nil {
			paidAt = t
		}
	}

	return &PaymentEvent{
		ID:                uuid.NewString(),
		Provider:          "xendit",
		ProviderPaymentID: paymentID,
		OrderID:           externalID,
		Status:            mappedStatus,
		PaidAt:            paidAt,
		Raw:               body,
	}, nil
}

type PaymentProviderRegistry struct {
	providers map[string]PaymentProvider
}

func NewPaymentProviderRegistry() *PaymentProviderRegistry {
	return &PaymentProviderRegistry{providers: make(map[string]PaymentProvider)}
}

func (r *PaymentProviderRegistry) Register(name string, provider PaymentProvider) {
	r.providers[name] = provider
}

func (r *PaymentProviderRegistry) Get(name string) (PaymentProvider, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, errors.New("payment provider not found: " + name)
	}
	return p, nil
}

func (r *PaymentProviderRegistry) List() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}

func CreatePaymentProviders(config map[string]string, timeout time.Duration) *PaymentProviderRegistry {
	registry := NewPaymentProviderRegistry()

	if serverKey := config["MIDTRANS_SERVER_KEY"]; serverKey != "" {
		clientKey := config["MIDTRANS_CLIENT_KEY"]
		isProduction := config["MIDTRANS_IS_PRODUCTION"] == "true"
		registry.Register("midtrans", NewMidtransProvider(serverKey, clientKey, isProduction,
			WithPaymentHTTPClient(&http.Client{Timeout: timeout}),
		))
	}

	if callbackToken := config["XENDIT_CALLBACK_TOKEN"]; callbackToken != "" {
		apiKey := config["XENDIT_API_KEY"]
		isProduction := config["XENDIT_IS_PRODUCTION"] == "true"
		registry.Register("xendit", NewXenditProvider(callbackToken, apiKey, isProduction,
			WithPaymentHTTPClient(&http.Client{Timeout: timeout}),
		))
	}

	return registry
}
