package handler_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/necly/services/internal/domain"
)

func TestNEC137InventoryLifecycleRoutes(t *testing.T) {
	db, _, app, access, productID := newNEC96HandlerFixture(t)
	now := time.Now().UTC()
	accountExpiry := now.AddDate(0, 0, 20).Format(time.RFC3339)

	status, payload := nec96Request(t, app, access, http.MethodPost, "/api/v1/admin/inventory",
		`{"product_id":"`+productID+`","credentials":"user:pass","label":"Akun expiry","sharing_limit":5,"expires_at":"`+accountExpiry+`"}`)
	if status != fiber.StatusCreated {
		t.Fatalf("create account = %d %s", status, payload)
	}
	var account struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(payload, &account); err != nil {
		t.Fatal(err)
	}

	var price domain.ProductPrice
	if err := db.Where("product_id = ?", productID).First(&price).Error; err != nil {
		t.Fatalf("load price: %v", err)
	}
	status, payload = nec96Request(t, app, access, http.MethodPost, "/api/v1/admin/inventory/slots",
		`{"product_price_id":"`+price.ID+`","inventory_account_id":"`+account.ID+`","quantity":1}`)
	if status != fiber.StatusCreated {
		t.Fatalf("create slot = %d %s", status, payload)
	}
	var storedSlot domain.InventorySlot
	if err := db.Where("inventory_account_id = ?", account.ID).First(&storedSlot).Error; err != nil {
		t.Fatalf("load created slot: %v", err)
	}
	slot := struct {
		ID string `json:"id"`
	}{ID: storedSlot.ID}

	// A newly provisioned slot may not carry an access window beyond the account.
	overrunExpiry := time.Now().UTC().AddDate(0, 0, 40).Format(time.RFC3339)
	status, payload = nec96Request(t, app, access, http.MethodPost, "/api/v1/admin/inventory/slots",
		`{"product_price_id":"`+price.ID+`","inventory_account_id":"`+account.ID+`","quantity":1,"access_expires_at":"`+overrunExpiry+`"}`)
	if status != fiber.StatusUnprocessableEntity {
		t.Fatalf("create slot overrun = %d %s", status, payload)
	}

	// The explicit slot route must not be shadowed by /inventory/:id/extend.
	status, payload = nec96Request(t, app, access, http.MethodPost, "/api/v1/admin/inventory/slots/"+slot.ID+"/extend", `{"duration_days":30}`)
	if status != fiber.StatusUnprocessableEntity {
		t.Fatalf("slot overrun = %d %s", status, payload)
	}
	if !json.Valid(payload) || !containsCode(payload, "slot_exceeds_account_expiry") {
		t.Fatalf("overrun response = %s", payload)
	}

	status, payload = nec96Request(t, app, access, http.MethodPost, "/api/v1/admin/inventory/slots/"+slot.ID+"/extend", `{"duration_days":10}`)
	if status != fiber.StatusOK {
		t.Fatalf("slot extend = %d %s", status, payload)
	}

	status, payload = nec96Request(t, app, access, http.MethodPost, "/api/v1/admin/inventory/"+account.ID+"/extend", `{"duration_days":10}`)
	if status != fiber.StatusOK {
		t.Fatalf("account extend = %d %s", status, payload)
	}
	if err := json.Unmarshal(payload, &account); err != nil {
		t.Fatal(err)
	}
	if account.Status != "active" || account.ExpiresAt == "" {
		t.Fatalf("account after extend = %+v", account)
	}

	status, payload = nec96Request(t, app, access, http.MethodPost, "/api/v1/admin/inventory/"+account.ID+"/deactivate", "")
	if status != fiber.StatusOK {
		t.Fatalf("account deactivate = %d %s", status, payload)
	}
	if err := json.Unmarshal(payload, &account); err != nil {
		t.Fatal(err)
	}
	if account.Status != "inactive" {
		t.Fatalf("account status after deactivate = %q", account.Status)
	}

	status, payload = nec96Request(t, app, access, http.MethodPost, "/api/v1/admin/inventory/slots/"+slot.ID+"/deactivate", "")
	if status != fiber.StatusOK {
		t.Fatalf("slot deactivate = %d %s", status, payload)
	}
}

func containsCode(payload []byte, code string) bool {
	var body struct {
		Code string `json:"code"`
	}
	return json.Unmarshal(payload, &body) == nil && body.Code == code
}
