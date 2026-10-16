package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// jsonKeys reports the field names of a JSON object, sorted. It exists so the
// shape of an unexpected webhook payload can be logged without printing its
// values, which for a payment callback include the payer's details.
func jsonKeys(body []byte) []string {
	if len(body) == 0 {
		return nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		return nil
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

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
	returnBaseURL string
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
	reqBody := map[string]any{
		"reference_id":   req.OrderID,
		"session_type":   "PAY",
		"mode":           "PAYMENT_LINK",
		"amount":         req.Amount,
		"currency":       req.Currency,
		"country":        "ID",
		"capture_method": "AUTOMATIC",
		"locale":         "en",
		"description":    "Pembayaran pesanan " + req.OrderNumber,
	}
	if !req.ExpiresAt.IsZero() {
		reqBody["expires_at"] = req.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if returnBaseURL := strings.TrimRight(strings.TrimSpace(x.returnBaseURL), "/"); returnBaseURL != "" {
		if parsed, err := url.Parse(returnBaseURL); err == nil && parsed.Scheme == "https" && parsed.Host != "" {
			paymentPath := "/payment/" + url.PathEscape(req.OrderID)
			reqBody["success_return_url"] = returnBaseURL + paymentPath + "?result=success"
			reqBody["cancel_return_url"] = returnBaseURL + paymentPath + "?result=cancel"
		}
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal xendit session request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, x.baseURL+"/sessions", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create http request: %w", err)
	}
	httpReq.SetBasicAuth(x.apiKey, "")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

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
		PaymentSessionID string `json:"payment_session_id"`
		PaymentLinkURL   string `json:"payment_link_url"`
		Status           string `json:"status"`
		ReferenceID      string `json:"reference_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&xenditResp); err != nil {
		return nil, fmt.Errorf("decode xendit response: %w", err)
	}

	if xenditResp.PaymentSessionID == "" || xenditResp.PaymentLinkURL == "" {
		return nil, fmt.Errorf("xendit session response missing payment_session_id or payment_link_url")
	}

	return &PaymentInitiation{
		ProviderPaymentID: xenditResp.PaymentSessionID,
		Status:            mapXenditStatus(xenditResp.Status),
		CheckoutURL:       xenditResp.PaymentLinkURL,
	}, nil
}

func (x *XenditProvider) VerifyWebhook(headers map[string]string, body []byte) (*PaymentEvent, error) {
	var callbackToken string
	for name, value := range headers {
		if strings.EqualFold(name, "x-callback-token") {
			callbackToken = value
			break
		}
	}
	if callbackToken == "" || subtle.ConstantTimeCompare([]byte(callbackToken), []byte(x.callbackToken)) != 1 {
		return nil, ErrUnauthorized
	}

	var notification struct {
		Event   string `json:"event"`
		Created string `json:"created"`
		Data    struct {
			PaymentSessionID string `json:"payment_session_id"`
			ReferenceID      string `json:"reference_id"`
			Status           string `json:"status"`
			Updated          string `json:"updated"`
		} `json:"data"`
		// Legacy Invoice fields remain accepted while old pending invoices can
		// still deliver callbacks during the migration window.
		ID         string `json:"id"`
		ExternalID string `json:"external_id"`
		Status     string `json:"status"`
		PaidAt     string `json:"paid_at"`
	}
	if err := json.Unmarshal(body, &notification); err != nil {
		log.Printf("xendit webhook: body is not the expected JSON object (top_keys=%v)", jsonKeys(body))
		return nil, ErrInvalidInput
	}

	paymentID := notification.Data.PaymentSessionID
	orderID := notification.Data.ReferenceID
	status := notification.Data.Status
	paidAtValue := notification.Data.Updated
	if paymentID == "" {
		paymentID = notification.ID
		orderID = notification.ExternalID
		status = notification.Status
		paidAtValue = notification.PaidAt
	}
	if strings.TrimSpace(paymentID) == "" || strings.TrimSpace(orderID) == "" || strings.TrimSpace(status) == "" {
		// A 400 here means a genuine notification we could not map, which would
		// leave a paid order stuck on pending_payment. Log the event name and
		// which fields were missing so the shape can be fixed without guessing.
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(body, &raw)
		log.Printf("xendit webhook: unmappable event=%q missing(payment_id=%t reference_id=%t status=%t) top_keys=%v data_keys=%v",
			notification.Event, paymentID == "", orderID == "", status == "", jsonKeys(body), jsonKeys(raw["data"]))
		return nil, ErrInvalidInput
	}

	var paidAt time.Time
	if mapXenditStatus(status) == "paid" {
		if paidAtValue == "" {
			paidAtValue = notification.Created
		}
		if t, err := time.Parse(time.RFC3339, paidAtValue); err == nil {
			paidAt = t
		} else {
			paidAt = time.Now().UTC()
		}
	}

	return &PaymentEvent{
		ID:                uuid.NewString(),
		Provider:          "xendit",
		ProviderPaymentID: paymentID,
		OrderID:           orderID,
		Status:            mapXenditStatus(status),
		PaidAt:            paidAt,
		Raw:               body,
	}, nil
}

func mapXenditStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "paid", "settled", "succeeded":
		return "paid"
	case "expired":
		return "expired"
	case "canceled", "cancelled":
		return "cancelled"
	case "failed", "failure":
		return "failed"
	default:
		return "pending"
	}
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
		options := []paymentProviderOption{WithPaymentHTTPClient(&http.Client{Timeout: timeout})}
		if baseURL := strings.TrimSpace(config["XENDIT_BASE_URL"]); baseURL != "" {
			options = append(options, WithPaymentBaseURL(baseURL))
		}
		provider := NewXenditProvider(callbackToken, apiKey, isProduction, options...)
		provider.returnBaseURL = config["FRONTEND_BASE_URL"]
		registry.Register("xendit", provider)
	}

	return registry
}
