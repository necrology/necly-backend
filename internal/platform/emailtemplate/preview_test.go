package emailtemplate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPreviewAccessCodeEmail writes the rendered access code email HTML to a file
// when EMAILTEMPLATE_PREVIEW_DIR environment variable is set.
func TestPreviewAccessCodeEmail(t *testing.T) {
	previewDir := os.Getenv("EMAILTEMPLATE_PREVIEW_DIR")
	if previewDir == "" {
		t.Skip("EMAILTEMPLATE_PREVIEW_DIR not set, skipping preview generation")
	}

	input := AccessCodeEmailInput{
		CustomerName:  "Budi Santoso",
		OrderNumber:   "ORD-20261122-001",
		ProductName:   "Netflix Premium",
		DurationName:  "1 Bulan",
		AccountEmails: []string{"user1@example.com", "user2@example.com"},
		AccessCode:    "XK7M9PQ2R4",
		ExpiresAt:     time.Date(2026, 11, 22, 15, 30, 0, 0, time.FixedZone("WIB", 7*3600)),
		FrontendURL:   "https://necly.example",
		CompanyName:   "NECLY Services",
	}

	html, err := RenderAccessCodeEmail(input)
	if err != nil {
		t.Fatalf("RenderAccessCodeEmail failed: %v", err)
	}

	// Verify HTML structure before writing
	if !strings.Contains(html, `<table role="presentation"`) {
		t.Error("Preview HTML missing table-based layout")
	}
	if !strings.Contains(html, `XK7M9PQ2R4`) {
		t.Error("Preview HTML missing access code")
	}
	if !strings.Contains(html, `ORD-20261122-001`) {
		t.Error("Preview HTML missing order number")
	}
	// Verify 8 steps present
	stepCount := strings.Count(html, `background-color:#4f46e5;color:#ffffff;font-size:13px;font-weight:700;line-height:1;">`)
	if stepCount < 8 {
		t.Errorf("Preview HTML missing steps, found %d", stepCount)
	}

	// Write to file
	filename := filepath.Join(previewDir, "access_code_email.html")
	if err := os.WriteFile(filename, []byte(html), 0644); err != nil {
		t.Fatalf("Failed to write preview file: %v", err)
	}
	t.Logf("Preview written to %s", filename)
}

// TestPreviewDeliveryEmail writes the rendered delivery email HTML to a file
// when EMAILTEMPLATE_PREVIEW_DIR environment variable is set.
func TestPreviewDeliveryEmail(t *testing.T) {
	previewDir := os.Getenv("EMAILTEMPLATE_PREVIEW_DIR")
	if previewDir == "" {
		t.Skip("EMAILTEMPLATE_PREVIEW_DIR not set, skipping preview generation")
	}

	accounts := []DeliveryAccountView{
		{
			Label:        "Akun Utama",
			Username:     "user@example.com",
			Password:     "secretpass123",
			Instructions: "Jangan mengubah kata sandi atau profil akun",
		},
	}

	input := DeliveryEmailInput{
		CustomerName:    "Budi Santoso",
		OrderNumber:     "ORD-20261122-001",
		ProductName:     "Netflix Premium",
		DurationName:    "1 Bulan",
		Quantity:        1,
		AccessExpiresAt: time.Date(2026, 12, 22, 0, 0, 0, 0, time.UTC),
		Accounts:        accounts,
		FrontendURL:     "https://necly.example",
		CompanyName:     "NECLY Services",
	}

	html, err := RenderDeliveryEmail(input)
	if err != nil {
		t.Fatalf("RenderDeliveryEmail failed: %v", err)
	}

	if !strings.Contains(html, `<table role="presentation"`) {
		t.Error("Preview HTML missing table-based layout")
	}
	if !strings.Contains(html, `ORD-20261122-001`) {
		t.Error("Preview HTML missing order number")
	}
	if !strings.Contains(html, `user@example.com`) {
		t.Error("Preview HTML missing account username")
	}
	if !strings.Contains(html, `secretpass123`) {
		t.Error("Preview HTML missing account password")
	}

	filename := filepath.Join(previewDir, "delivery_email.html")
	if err := os.WriteFile(filename, []byte(html), 0644); err != nil {
		t.Fatalf("Failed to write preview file: %v", err)
	}
	t.Logf("Preview written to %s", filename)
}

// TestPreviewReplacementEmail writes the rendered replacement email HTML to a file
// when EMAILTEMPLATE_PREVIEW_DIR environment variable is set.
func TestPreviewReplacementEmail(t *testing.T) {
	previewDir := os.Getenv("EMAILTEMPLATE_PREVIEW_DIR")
	if previewDir == "" {
		t.Skip("EMAILTEMPLATE_PREVIEW_DIR not set, skipping preview generation")
	}

	accounts := []DeliveryAccountView{
		{
			Label:        "Akun Pengganti 1",
			Username:     "newuser@example.com",
			Password:     "newpass456",
			Instructions: "",
		},
	}

	input := ReplacementEmailInput{
		CustomerName:    "Budi Santoso",
		OrderNumber:     "ORD-20261122-001",
		ProductName:     "Netflix Premium",
		DurationName:    "1 Bulan",
		Quantity:        1,
		AccessExpiresAt: time.Date(2026, 12, 22, 0, 0, 0, 0, time.UTC),
		Accounts:        accounts,
		Reason:          "Masa aktif akun sebelumnya berakhir",
		FrontendURL:     "https://necly.example",
		CompanyName:     "NECLY Services",
	}

	html, err := RenderReplacementEmail(input)
	if err != nil {
		t.Fatalf("RenderReplacementEmail failed: %v", err)
	}

	if !strings.Contains(html, `<table role="presentation"`) {
		t.Error("Preview HTML missing table-based layout")
	}
	if !strings.Contains(html, `Masa aktif akun sebelumnya berakhir`) {
		t.Error("Preview HTML missing replacement reason")
	}
	if !strings.Contains(html, `newuser@example.com`) {
		t.Error("Preview HTML missing replacement account username")
	}

	filename := filepath.Join(previewDir, "replacement_email.html")
	if err := os.WriteFile(filename, []byte(html), 0644); err != nil {
		t.Fatalf("Failed to write preview file: %v", err)
	}
	t.Logf("Preview written to %s", filename)
}
