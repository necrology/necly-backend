package service

import (
	"os"
	"testing"
	"time"

	"github.com/necly/services/internal/platform/emailtemplate"
)

// TestDumpDeliveryEmailWithAccessCode renders the delivery email through the
// real path with an access code present, so the operator can confirm the code
// actually reaches the HTML the customer reads.
// Opt in with NECLY_EMAIL_DUMP=1.
func TestDumpDeliveryEmailWithAccessCode(t *testing.T) {
	if os.Getenv("NECLY_EMAIL_DUMP") != "1" {
		t.Skip("set NECLY_EMAIL_DUMP=1 to dump rendered emails")
	}
	outDir := "/tmp/necly-delivery-fixed"
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	const code = "XK7M9PQ2R4"
	expires := time.Date(2026, 12, 5, 21, 30, 0, 0, time.UTC)

	html, err := emailtemplate.RenderDeliveryEmail(emailtemplate.DeliveryEmailInput{
		CustomerName:        "Maulana",
		OrderNumber:         "ORD-20261122-0041",
		ProductName:         "ChatGPT Plus 1 Bulan",
		DurationName:        "1 Bulan",
		Quantity:            1,
		AccessExpiresAt:     expires,
		AccessCode:          code,
		AccessCodeExpiresAt: expires,
		Accounts: []emailtemplate.DeliveryAccountView{{
			Label:        "Akun 1",
			Username:     "buyer-account@example.test",
			Password:     "Example-Pass-1",
			Instructions: "Buka chatgpt.com, pilih Log in, masukkan email lalu kata sandi.",
		}},
		FrontendURL: "https://necly-service.my.id",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := os.WriteFile(outDir+"/delivery-with-code.html", []byte(html), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// No access code path: must not render an empty card.
	htmlNoCode, err := emailtemplate.RenderDeliveryEmail(emailtemplate.DeliveryEmailInput{
		CustomerName:    "Maulana",
		OrderNumber:     "ORD-1",
		ProductName:     "ChatGPT Plus 1 Bulan",
		DurationName:    "1 Bulan",
		Quantity:        1,
		AccessExpiresAt: expires,
		Accounts: []emailtemplate.DeliveryAccountView{{
			Label: "Akun 1", Username: "buyer-account@example.test", Password: "Example-Pass-1",
		}},
		FrontendURL: "https://necly-service.my.id",
	})
	if err != nil {
		t.Fatalf("render no-code: %v", err)
	}
	if err := os.WriteFile(outDir+"/delivery-without-code.html", []byte(htmlNoCode), 0o644); err != nil {
		t.Fatalf("write no-code: %v", err)
	}

	t.Logf("wrote delivery emails to %s (with=%d bytes, without=%d bytes)", outDir, len(html), len(htmlNoCode))
}
