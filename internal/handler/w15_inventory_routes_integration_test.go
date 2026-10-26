package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/handler"
	"github.com/necly/services/internal/migrate"
	"github.com/necly/services/internal/platform/testdb"
	"github.com/necly/services/internal/repository/gormrepo"
	"github.com/necly/services/internal/service"
)

// TestW15AdminInventoryUpdateContractExercisesTheRealStore covers the PATCH
// contract fixed after review NEC-58: lowering sharing_limit below the
// provisioned slot rows must answer 409 sharing_limit_exceeded (not 500), and
// the single-account responses must carry the same derived counters the list
// endpoint reports.
func TestW15AdminInventoryUpdateContractExercisesTheRealStore(t *testing.T) {
	if !testdb.Available() {
		t.Skip("DB_HOST/DB_USER not set; skipping PostgreSQL integration test")
	}
	db, cleanup, err := testdb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	migrations, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Run(context.Background(), db, migrate.Options{Dir: migrations}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Now().UTC()
	adminID := uuid.NewString()
	if err := db.Create(&domain.User{
		ID: adminID, Email: "w15.admin@example.test", Name: "W15 Admin",
		PasswordHash: "x", Role: domain.RoleAdmin, Active: true, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	categoryID := uuid.NewString()
	productID := uuid.NewString()
	durationID := uuid.NewString()
	priceID := uuid.NewString()
	if err := db.Create(&domain.Category{ID: categoryID, Name: "Streaming Video", Slug: "w15-" + categoryID[:8], Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed category: %v", err)
	}
	if err := db.Create(&domain.Product{ID: productID, CategoryID: categoryID, Name: "Netflix Premium", Slug: "w15-netflix-" + productID[:8], Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed product: %v", err)
	}
	if err := db.Create(&domain.ProductDuration{ID: durationID, Name: "1 Bulan", Days: 30, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed duration: %v", err)
	}
	if err := db.Create(&domain.ProductPrice{ID: priceID, ProductID: productID, DurationID: durationID, Amount: 186000, Currency: "IDR", Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed price: %v", err)
	}

	accountID := uuid.NewString()
	if err := db.Create(&domain.InventoryAccount{
		ID: accountID, ProductID: productID, Label: "Akun utama", EncryptedCredentials: []byte("cipher"),
		Status: "active", SharingLimit: 5, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed account: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := db.Create(&domain.InventorySlot{
			ID: uuid.NewString(), InventoryAccountID: accountID, ProductPriceID: priceID,
			Status: "available", CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatalf("seed slot: %v", err)
		}
	}

	store := gormrepo.NewStore(db)
	cipher, err := service.NewCredentialCipher("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	tokens, err := service.NewTokenManager("test-jwt-secret-that-has-at-least-32-bytes", "necly", 15*time.Minute, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	authSvc := service.NewAuthService(store, service.NewPasswordHasher(4), tokens, service.AuthSettings{})
	router := handler.NewRouter(
		authSvc,
		nil, nil, nil, nil,
		nil,
		nil, nil, nil, nil,
		nil,
		nil, service.NewAdminInventoryService(store, cipher),
		tokens, cipher, nil, nil, nil,
	)
	app := fiber.New()
	router.Register(app)

	sessionID := uuid.NewString()
	if err := store.SaveRefreshToken(context.Background(), &domain.RefreshToken{
		ID: sessionID, UserID: adminID, TokenHash: uuid.NewString(), ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	access, err := tokens.IssueAccess(adminID, string(domain.RoleAdmin), sessionID)
	if err != nil {
		t.Fatal(err)
	}

	patch := func(body string) (int, []byte) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/inventory/"+accountID, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, payload
	}

	// Lowering the limit under the three provisioned rows is a client error the
	// dashboard can explain, not a 500.
	status, payload := patch(`{"sharing_limit":2}`)
	if status != fiber.StatusConflict {
		t.Fatalf("lower limit = %d %s, want 409", status, payload)
	}
	var conflict struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(payload, &conflict); err != nil {
		t.Fatal(err)
	}
	if conflict.Code != "sharing_limit_exceeded" {
		t.Fatalf("code = %q, want sharing_limit_exceeded (%s)", conflict.Code, payload)
	}

	// Raising the limit keeps the three slots and must report the derived
	// counters on the PATCH response itself, not just on the list endpoint.
	status, payload = patch(`{"sharing_limit":4}`)
	if status != fiber.StatusOK {
		t.Fatalf("raise limit = %d %s, want 200", status, payload)
	}
	var updated struct {
		SharingLimit   int  `json:"sharing_limit"`
		TotalSlots     int  `json:"total_slots"`
		AvailableSlots int  `json:"available_slots"`
		UsedSlots      int  `json:"used_slots"`
		RemainingSlots int  `json:"remaining_slots"`
		Sellable       bool `json:"sellable"`
	}
	if err := json.Unmarshal(payload, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.SharingLimit != 4 {
		t.Fatalf("sharing_limit = %d, want 4", updated.SharingLimit)
	}
	if updated.TotalSlots != 3 || updated.AvailableSlots != 3 || updated.UsedSlots != 0 || updated.RemainingSlots != 1 {
		t.Fatalf("derived counters = %+v, want total=3 available=3 used=0 remaining=1", updated)
	}
	if !updated.Sellable {
		t.Fatal("active non-expired account should be sellable")
	}
	if bytes.Contains(payload, []byte("encrypted_credentials")) || bytes.Contains(payload, []byte("cipher")) {
		t.Fatalf("PATCH response leaked credentials: %s", payload)
	}

	// GET /:id must agree with the PATCH response.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/inventory/"+accountID, nil)
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	getPayload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("get account = %d %s, want 200", resp.StatusCode, getPayload)
	}
	var fetched struct {
		TotalSlots     int  `json:"total_slots"`
		RemainingSlots int  `json:"remaining_slots"`
		Sellable       bool `json:"sellable"`
	}
	if err := json.Unmarshal(getPayload, &fetched); err != nil {
		t.Fatal(err)
	}
	if fetched.TotalSlots != 3 || fetched.RemainingSlots != 1 || !fetched.Sellable {
		t.Fatalf("GET derived counters = %+v, want total=3 remaining=1 sellable=true", fetched)
	}

	// NEC-121: the PATCH route binds the separate login and mailbox fields. The
	// existing account has a non-decryptable placeholder, so create a second one
	// with a real encrypted credential and update only the email.
	legacyCredential, err := cipher.Encrypt([]byte(`{"username":"old@example.test","password":"old-pass","instructions":"profil 2"}`))
	if err != nil {
		t.Fatal(err)
	}
	secondID := uuid.NewString()
	if err := db.Create(&domain.InventoryAccount{
		ID: secondID, ProductID: productID, Label: "Akun kedua", AccountEmail: "old@example.test",
		EncryptedCredentials: legacyCredential, Status: "active", SharingLimit: 5, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed second account: %v", err)
	}
	emailPatch := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/inventory/"+secondID, bytes.NewBufferString(
		`{"account_email":"family@example.test","mailbox_app_password":"gmail-app-password","mailbox_imap_host":"imap.gmail.com:993"}`,
	))
	emailPatch.Header.Set("Authorization", "Bearer "+access)
	emailPatch.Header.Set("Content-Type", "application/json")
	emailResp, err := app.Test(emailPatch)
	if err != nil {
		t.Fatal(err)
	}
	emailPayload, _ := io.ReadAll(emailResp.Body)
	if emailResp.StatusCode != fiber.StatusOK {
		t.Fatalf("email PATCH = %d %s, want 200", emailResp.StatusCode, emailPayload)
	}
	var updatedAccount struct {
		AccountEmail       string `json:"account_email"`
		MailboxIMAPHost    string `json:"mailbox_imap_host"`
		OTPInboxConfigured bool   `json:"otp_inbox_configured"`
		OTPInboxSource     string `json:"otp_inbox_source"`
	}
	if err := json.Unmarshal(emailPayload, &updatedAccount); err != nil {
		t.Fatal(err)
	}
	if updatedAccount.AccountEmail != "family@example.test" || updatedAccount.MailboxIMAPHost != "imap.gmail.com:993" {
		t.Fatalf("email response = %+v", updatedAccount)
	}
	if !updatedAccount.OTPInboxConfigured || updatedAccount.OTPInboxSource != "direct" {
		t.Fatalf("otp inbox response = %+v, want direct/true", updatedAccount)
	}
	for _, secret := range []string{"old-pass", "gmail-app-password", "mailbox_app_password", "account_password", "encrypted_credentials"} {
		if bytes.Contains(emailPayload, []byte(secret)) {
			t.Fatalf("email PATCH response leaked %q: %s", secret, emailPayload)
		}
	}
	var stored domain.InventoryAccount
	if err := db.Where("id = ?", secondID).First(&stored).Error; err != nil {
		t.Fatalf("reload second account: %v", err)
	}
	plain, err := cipher.Decrypt(stored.EncryptedCredentials)
	if err != nil {
		t.Fatalf("decrypt merged credential: %v", err)
	}
	if !bytes.Contains(plain, []byte(`"username":"family@example.test"`)) || !bytes.Contains(plain, []byte(`"password":"old-pass"`)) {
		t.Fatalf("merged credential = %s", plain)
	}
	if len(stored.MailboxAppPasswordEncrypted) == 0 {
		t.Fatal("mailbox app password was not stored")
	}
}
