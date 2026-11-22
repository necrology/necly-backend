package service

import (
	"os"
	"testing"
	"time"

	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/platform/emailtemplate"
)

// TestRenderProductionEmailsToDisk renders the real templates through the same
// input structs the services build at runtime, and writes the HTML to
// /tmp/necly-email-out so an operator can open them in a browser. It is skipped
// unless NECLY_EMAIL_DUMP=1 so it never interferes with CI.
func TestRenderProductionEmailsToDisk(t *testing.T) {
	if os.Getenv("NECLY_EMAIL_DUMP") != "1" {
		t.Skip("set NECLY_EMAIL_DUMP=1 to dump rendered emails")
	}
	outDir := "/tmp/necly-email-out"
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	expires := time.Date(2026, 12, 5, 21, 30, 0, 0, time.UTC)
	accounts := []emailtemplate.DeliveryAccountView{
		{
			Label:    "Akun 1 dari 1",
			Username: "necrology45@gmail.com",
			Password: "Contoh-Password-9f2",
			Instructions: "Buka chatgpt.com, pilih Log in, masukkan email lalu kata sandi di atas. " +
				"Jangan mengubah kata sandi atau profil akun.",
		},
	}

	// 1. Access code email, sent after a customer pays.
	html, err := emailtemplate.RenderAccessCodeEmail(emailtemplate.AccessCodeEmailInput{
		CustomerName:  "Maulana",
		OrderNumber:   "ORD-20261122-0041",
		ProductName:   "ChatGPT Plus 1 Bulan",
		DurationName:  "1 Bulan",
		AccountEmails: []string{"necrology45@gmail.com"},
		AccessCode:    "XK7M9PQ2R4",
		ExpiresAt:     expires,
		FrontendURL:   "https://necly-service.my.id",
	})
	if err != nil {
		t.Fatalf("render access code: %v", err)
	}
	if err := os.WriteFile(outDir+"/1-access-code-otp.html", []byte(html), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	assertTemplate(t, "access code", html, []string{
		"XK7M9PQ2R4", "ORD-20261122-0041", "necrology45@gmail.com",
		"Langkah 1", "kata sandi",
	})

	// 2. Delivery email, sent when the account credentials are handed over.
	html, err = emailtemplate.RenderDeliveryEmail(emailtemplate.DeliveryEmailInput{
		CustomerName:    "Maulana",
		OrderNumber:     "ORD-20261122-0041",
		ProductName:     "ChatGPT Plus 1 Bulan",
		DurationName:    "1 Bulan",
		Quantity:        1,
		AccessExpiresAt: expires,
		Accounts:        accounts,
		FrontendURL:     "https://necly-service.my.id",
	})
	if err != nil {
		t.Fatalf("render delivery: %v", err)
	}
	if err := os.WriteFile(outDir+"/2-delivery.html", []byte(html), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	assertTemplate(t, "delivery", html, []string{
		"necrology45@gmail.com", "Contoh-Password-9f2", "ORD-20261122-0041",
	})

	// 3. Replacement email, sent when an account is swapped.
	html, err = emailtemplate.RenderReplacementEmail(emailtemplate.ReplacementEmailInput{
		CustomerName:    "Maulana",
		OrderNumber:     "ORD-20261122-0041",
		ProductName:     "ChatGPT Plus 1 Bulan",
		DurationName:    "1 Bulan",
		Quantity:        1,
		AccessExpiresAt: expires,
		Accounts:        accounts,
		Reason:          "Masa aktif akun sebelumnya telah berakhir.",
		FrontendURL:     "https://necly-service.my.id",
	})
	if err != nil {
		t.Fatalf("render replacement: %v", err)
	}
	if err := os.WriteFile(outDir+"/3-replacement.html", []byte(html), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	assertTemplate(t, "replacement", html, []string{
		"necrology45@gmail.com", "ORD-20261122-0041",
	})

	t.Logf("wrote 3 rendered emails to %s", outDir)
}

func assertTemplate(t *testing.T, name, html string, mustContain []string) {
	t.Helper()
	if len(html) < 1500 {
		t.Fatalf("%s: suspiciously short html (%d bytes)", name, len(html))
	}
	for _, want := range mustContain {
		if !contains(html, want) {
			t.Fatalf("%s: missing %q", name, want)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

var _ = domain.Order{}
