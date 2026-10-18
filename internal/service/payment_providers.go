package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
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

// midtransChannels documents the subset of the canonical enum that this Snap
// integration can restrict. A channel omitted here is rejected rather than
// silently broadened to the provider default. Sources are listed in
// docs/payment-provider-contract.md.
var midtransChannels = map[string]string{
	"qris":      "other_qris",
	"shopeepay": "shopeepay",
	"bni_va":    "bni_va",
	"bri_va":    "bri_va",
}

func (m *MidtransProvider) CreatePayment(ctx context.Context, req PaymentRequest) (*PaymentInitiation, error) {
	orderID := req.OrderID
	grossAmount := req.Amount

	enabledPayments := []string{"credit_card", "bca_va", "bni_va", "bri_va", "permata_va", "other_va", "gopay", "shopeepay", "other_qris"}
	if channel := strings.ToLower(strings.TrimSpace(req.Channel)); channel != "" {
		mapped, ok := midtransChannels[channel]
		if !ok {
			return nil, ErrUnsupportedPaymentChannel
		}
		enabledPayments = []string{mapped}
	}

	snapReq := map[string]any{
		"transaction_details": map[string]any{
			"order_id":     orderID,
			"gross_amount": grossAmount,
		},
		"customer_details": map[string]any{
			"customer_id": req.CustomerID,
		},
		"enabled_payments": enabledPayments,
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

// midtransNotification mirrors the documented HTTP notification body. The
// signature lives in the JSON body, not in a header. gross_amount is a string
// because Midtrans may include decimal places (for example "10000.00") even
// for an IDR charge.
type midtransNotification struct {
	OrderID           string `json:"order_id"`
	StatusCode        string `json:"status_code"`
	GrossAmount       string `json:"gross_amount"`
	SignatureKey      string `json:"signature_key"`
	TransactionStatus string `json:"transaction_status"`
	FraudStatus       string `json:"fraud_status"`
	TransactionID     string `json:"transaction_id"`
	SettlementTime    string `json:"settlement_time"`
	Currency          string `json:"currency"`
}

func (m *MidtransProvider) VerifyWebhook(_ map[string]string, body []byte) (*PaymentEvent, error) {
	var notification midtransNotification
	if err := json.Unmarshal(body, &notification); err != nil {
		log.Printf("midtrans webhook: body is not the expected JSON object (top_keys=%v)", jsonKeys(body))
		return nil, ErrInvalidPaymentEvent
	}
	notification.OrderID = strings.TrimSpace(notification.OrderID)
	notification.StatusCode = strings.TrimSpace(notification.StatusCode)
	notification.GrossAmount = strings.TrimSpace(notification.GrossAmount)
	notification.TransactionStatus = strings.ToLower(strings.TrimSpace(notification.TransactionStatus))
	notification.TransactionID = strings.TrimSpace(notification.TransactionID)
	notification.SignatureKey = strings.ToLower(strings.TrimSpace(notification.SignatureKey))
	if notification.OrderID == "" || notification.StatusCode == "" || notification.GrossAmount == "" ||
		notification.TransactionStatus == "" || notification.TransactionID == "" || notification.SignatureKey == "" {
		log.Printf("midtrans webhook: missing required fields order_id=%t status_code=%t gross_amount=%t transaction_status=%t transaction_id=%t signature_key=%t",
			notification.OrderID == "", notification.StatusCode == "", notification.GrossAmount == "",
			notification.TransactionStatus == "", notification.TransactionID == "", notification.SignatureKey == "")
		return nil, ErrInvalidPaymentEvent
	}

	expectedSignature := generateMidtransSignature(notification.OrderID, notification.StatusCode, notification.GrossAmount, m.serverKey)
	if subtle.ConstantTimeCompare([]byte(notification.SignatureKey), []byte(expectedSignature)) != 1 {
		return nil, ErrUnauthorized
	}

	amount, err := parseProviderAmount(notification.GrossAmount)
	if err != nil {
		return nil, ErrInvalidPaymentEvent
	}
	status, ok := mapMidtransStatus(notification.TransactionStatus, notification.FraudStatus)
	if !ok {
		log.Printf("midtrans webhook: unsupported transaction_status=%q fraud_status=%q", notification.TransactionStatus, notification.FraudStatus)
		return nil, ErrInvalidPaymentEvent
	}
	currency := strings.ToUpper(strings.TrimSpace(notification.Currency))
	if currency == "" {
		// Snap supports IDR only in this integration; the notification omits
		// currency. Treat the absence as IDR rather than rejecting a valid event.
		currency = "IDR"
	}

	var paidAt time.Time
	if status == "paid" {
		paidAt = parseMidtransSettlementTime(notification.SettlementTime)
	}

	return &PaymentEvent{
		ID:                paymentEventKey("midtrans", notification.TransactionID, notification.TransactionStatus, notification.FraudStatus, notification.StatusCode, notification.GrossAmount),
		Provider:          "midtrans",
		ProviderPaymentID: notification.TransactionID,
		OrderID:           notification.OrderID,
		Status:            status,
		Amount:            amount,
		Currency:          currency,
		PaidAt:            paidAt,
		Raw:               body,
	}, nil
}

func mapMidtransStatus(transactionStatus, fraudStatus string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(transactionStatus)) {
	case "capture":
		switch strings.ToLower(strings.TrimSpace(fraudStatus)) {
		case "accept", "":
			return "paid", true
		case "challenge":
			// The bank accepted the card but Midtrans is reviewing it. Do not
			// mark the order paid until a later accepted notification arrives.
			return "pending", true
		case "deny":
			return "failed", true
		default:
			return "", false
		}
	case "settlement":
		return "paid", true
	case "pending":
		return "pending", true
	case "deny", "failure":
		return "failed", true
	case "cancel":
		return "cancelled", true
	case "expire":
		return "expired", true
	case "refund", "partial_refund":
		return "refunded", true
	case "chargeback":
		// A chargeback reverses the money; it is a refund-family event. Stock is
		// deliberately not released by the webhook (see ApplyPaymentEvent).
		return "refunded", true
	default:
		return "", false
	}
}

func parseMidtransSettlementTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Now().UTC()
	}
	// Midtrans documents settlement_time in WIB (UTC+7). Some fixtures use an
	// RFC3339 value, so accept both without silently treating WIB as UTC.
	jakarta := time.FixedZone("WIB", 7*60*60)
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339} {
		location := jakarta
		if layout == time.RFC3339 {
			location = time.UTC
		}
		if parsed, err := time.ParseInLocation(layout, value, location); err == nil {
			return parsed.UTC()
		}
	}
	return time.Now().UTC()
}

func generateMidtransSignature(orderID, statusCode, grossAmount, serverKey string) string {
	data := orderID + statusCode + grossAmount + serverKey
	hash := sha512.Sum512([]byte(data))
	return hex.EncodeToString(hash[:])
}

// paymentEventKey returns a stable, bounded key for deduplication. Replaying
// the same provider event yields the same key; a later status for the same
// payment yields a different key and is therefore processed instead of hidden.
func paymentEventKey(provider string, parts ...string) string {
	hash := sha256.Sum256([]byte(provider + "\x00" + strings.Join(parts, "\x00")))
	return provider + ":" + hex.EncodeToString(hash[:])
}

func parseProviderAmount(raw string) (int64, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, fmt.Errorf("amount is empty")
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < 0 || parsed != math.Trunc(parsed) {
		return 0, fmt.Errorf("invalid amount %q", value)
	}
	if parsed > math.MaxInt64 {
		return 0, fmt.Errorf("amount %q overflows int64", value)
	}
	return int64(parsed), nil
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

// xenditChannels maps the canonical enum to the documented
// allowed_payment_channels values. Activation remains account-specific; Xendit
// returns INVALID_PAYMENT_CHANNEL when a channel is not active, and the caller
// surfaces that as unsupported_payment_channel.
var xenditChannels = map[string]string{
	"qris":       "QRIS",
	"ovo":        "OVO",
	"dana":       "DANA",
	"shopeepay":  "SHOPEEPAY",
	"linkaja":    "LINKAJA",
	"alfamart":   "ALFAMART",
	"bni_va":     "BNI_VIRTUAL_ACCOUNT",
	"bsi_va":     "BSI_VIRTUAL_ACCOUNT",
	"bri_va":     "BRI_VIRTUAL_ACCOUNT",
	"mandiri_va": "MANDIRI_VIRTUAL_ACCOUNT",
}

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
	if channel := strings.ToLower(strings.TrimSpace(req.Channel)); channel != "" {
		mapped, ok := xenditChannels[channel]
		if !ok {
			return nil, ErrUnsupportedPaymentChannel
		}
		reqBody["allowed_payment_channels"] = []string{mapped}
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
			// payment.* events describe a payment and reference the session …
			PaymentSessionID string `json:"payment_session_id"`
			// … while payment_session.* events carry the session object itself, so
			// its id arrives as data.id.
			ID          string `json:"id"`
			ReferenceID string `json:"reference_id"`
			Status      string `json:"status"`
			Updated     string `json:"updated"`
			Amount      int64  `json:"amount"`
			Currency    string `json:"currency"`
		} `json:"data"`
		// Legacy Invoice fields remain accepted while old pending invoices can
		// still deliver callbacks during the migration window.
		ID         string `json:"id"`
		ExternalID string `json:"external_id"`
		Status     string `json:"status"`
		PaidAt     string `json:"paid_at"`
		Amount     int64  `json:"amount"`
		PaidAmount int64  `json:"paid_amount"`
		Currency   string `json:"currency"`
	}
	if err := json.Unmarshal(body, &notification); err != nil {
		log.Printf("xendit webhook: body is not the expected JSON object (top_keys=%v)", jsonKeys(body))
		return nil, ErrInvalidPaymentEvent
	}

	paymentID := notification.Data.PaymentSessionID
	if paymentID == "" {
		paymentID = notification.Data.ID
	}
	orderID := notification.Data.ReferenceID
	status := notification.Data.Status
	paidAtValue := notification.Data.Updated
	amount := notification.Data.Amount
	currency := notification.Data.Currency
	eventIdentity := paymentEventKey("xendit", notification.Event, paymentID, orderID, status, paidAtValue)
	// The legacy invoice shape puts its identifiers at the top level. Take that
	// branch only when they are really there: gating it on paymentID alone wiped
	// the reference_id and status that had parsed correctly, so every
	// payment_session.* callback — including completed and expired — was rejected
	// with a 400, and a lapsed session never released its reserved stock.
	if notification.ID != "" || notification.ExternalID != "" {
		paymentID = notification.ID
		orderID = notification.ExternalID
		status = notification.Status
		paidAtValue = notification.PaidAt
		amount = notification.Amount
		if notification.PaidAmount > 0 {
			amount = notification.PaidAmount
		}
		currency = notification.Currency
		eventIdentity = paymentEventKey("xendit", "invoice", paymentID, orderID, status, paidAtValue)
	}
	if strings.TrimSpace(paymentID) == "" || strings.TrimSpace(orderID) == "" || strings.TrimSpace(status) == "" || amount <= 0 || strings.TrimSpace(currency) == "" {
		// A 400 here means a genuine notification we could not map, which would
		// leave a paid order stuck on pending_payment. Log the event name and
		// which fields were missing so the shape can be fixed without guessing.
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(body, &raw)
		log.Printf("xendit webhook: unmappable event=%q missing(payment_id=%t reference_id=%t status=%t amount=%t currency=%t) top_keys=%v data_keys=%v",
			notification.Event, paymentID == "", orderID == "", status == "", amount <= 0, currency == "", jsonKeys(body), jsonKeys(raw["data"]))
		return nil, ErrInvalidPaymentEvent
	}

	var paidAt time.Time
	if mapXenditStatus(status) == "paid" {
		if paidAtValue == "" {
			paidAtValue = notification.Created
		}
		if t, err := time.Parse(time.RFC3339, paidAtValue); err == nil {
			paidAt = t.UTC()
		} else {
			paidAt = time.Now().UTC()
		}
	}

	return &PaymentEvent{
		ID:                eventIdentity,
		Provider:          "xendit",
		ProviderPaymentID: paymentID,
		OrderID:           orderID,
		Status:            mapXenditStatus(status),
		Amount:            amount,
		Currency:          strings.ToUpper(strings.TrimSpace(currency)),
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
