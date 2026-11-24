package emailtemplate

import (
	"strings"
	"testing"
	"time"
)

// TestDeliveryEmailMustCarryAccessCode reproduces the production complaint:
// a customer pays, receives the delivery email, but the email has no access
// code, so they cannot open the Ambil OTP page to fetch the ChatGPT OTP.
//
// The service generates an access code and includes it in the plain-text body
// (buildMessage in internal/service/order_delivery.go), but RenderDeliveryEmail
// had no field to receive it, so the HTML part -- the part the customer
// actually reads -- silently dropped it.
//
// This test fails while DeliveryEmailInput has no AccessCode field.
func TestDeliveryEmailMustCarryAccessCode(t *testing.T) {
	const code = "XK7M9PQ2R4"

	html, err := RenderDeliveryEmail(DeliveryEmailInput{
		CustomerName:    "Maulana",
		OrderNumber:     "ORD-20261122-0041",
		ProductName:     "ChatGPT Plus 1 Bulan",
		DurationName:    "1 Bulan",
		Quantity:        1,
		AccessExpiresAt: time.Date(2026, 12, 5, 0, 0, 0, 0, time.UTC),
		AccessCode:      code,
		Accounts: []DeliveryAccountView{{
			Label:    "Akun 1",
			Username: "buyer-account@example.test",
			Password: "Example-Pass-1",
		}},
		FrontendURL: "https://necly-service.my.id",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if !strings.Contains(html, code) {
		t.Fatalf("delivery email HTML does not contain the access code %q; "+
			"the customer cannot fetch the OTP", code)
	}
	// The customer must be told where to use the code, not just given it.
	if !strings.Contains(html, "/otp") {
		t.Error("delivery email HTML does not link to the Ambil OTP page")
	}
	// The code must not be confused with the account password or the platform OTP.
	if !strings.Contains(strings.ToLower(html), "otp") {
		t.Error("delivery email HTML never mentions OTP, so the code's purpose is unclear")
	}
}

// TestDeliveryEmailWithoutAccessCodeStaysClean pins the other half of the
// contract: when access codes are disabled the email must not render an empty
// code card or a dangling "Kode Akses" heading.
func TestDeliveryEmailWithoutAccessCodeStaysClean(t *testing.T) {
	html, err := RenderDeliveryEmail(DeliveryEmailInput{
		CustomerName:    "Maulana",
		OrderNumber:     "ORD-1",
		ProductName:     "ChatGPT Plus 1 Bulan",
		DurationName:    "1 Bulan",
		Quantity:        1,
		AccessExpiresAt: time.Date(2026, 12, 5, 0, 0, 0, 0, time.UTC),
		Accounts: []DeliveryAccountView{{
			Label: "Akun 1", Username: "a@example.test", Password: "p",
		}},
		FrontendURL: "https://necly-service.my.id",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(html, "KODE AKSES") || strings.Contains(html, "Kode Akses Necly") {
		t.Error("empty access-code card rendered even though no code was provided")
	}
}
