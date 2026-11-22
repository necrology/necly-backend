package emailtemplate

import (
	"strings"
	"testing"
	"time"
)

func TestRenderAccessCodeEmail(t *testing.T) {
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
		t.Fatalf("RenderAccessCodeEmail returned error: %v", err)
	}

	// Check structure
	if !strings.Contains(html, `<table role="presentation"`) {
		t.Error("HTML should contain table-based layout")
	}

	// Check content
	if !strings.Contains(html, `Budi Santoso`) {
		t.Error("HTML should contain customer name")
	}
	if !strings.Contains(html, `ORD-20261122-001`) {
		t.Error("HTML should contain order number")
	}
	if !strings.Contains(html, `Netflix Premium`) {
		t.Error("HTML should contain product name")
	}
	if !strings.Contains(html, `1 Bulan`) {
		t.Error("HTML should contain duration")
	}
	if !strings.Contains(html, `user1@example.com`) || !strings.Contains(html, `user2@example.com`) {
		t.Error("HTML should contain account emails")
	}
	if !strings.Contains(html, `XK7M9PQ2R4`) {
		t.Error("HTML should contain access code")
	}

	// Check for 8 steps
	stepCount := strings.Count(html, `background-color:#4f46e5;color:#ffffff;font-size:13px;font-weight:700;line-height:1;">`)
	if stepCount < 8 {
		t.Errorf("Expected at least 8 steps, found %d", stepCount)
	}

	// Check specific step content
	if !strings.Contains(html, `JANGAN login pakai Google`) && !strings.Contains(html, `JANGAN pakai Google`) {
		t.Error("HTML should contain warning about not using Google/Apple login")
	}
	if !strings.Contains(html, `Continue with Google`) && !strings.Contains(html, `Sign in with Apple`) {
		t.Error("HTML should mention specific SSO providers to avoid")
	}
	if !strings.Contains(html, `Ambil ulang`) {
		t.Error("HTML should contain step about retrying if OTP doesn't appear")
	}

	// Check help box
	if !strings.Contains(html, `Laporan Kendala`) {
		t.Error("HTML should contain help box with report link")
	}
	if !strings.Contains(html, `menyertakan nomor pesanan`) {
		t.Error("HTML should remind to include order number")
	}

	// Check footer note
	if !strings.Contains(html, `Jangan bagikan kode akses`) {
		t.Error("HTML should warn not to share access code")
	}
	if !strings.Contains(html, `tidak menerima balasan`) {
		t.Error("HTML should state email doesn't accept replies")
	}

	checkForbidden(html, t)

	if len(html) >= 100*1024 {
		t.Errorf("HTML size %d bytes exceeds 100KB limit", len(html))
	}
}

func TestRenderDeliveryEmail(t *testing.T) {
	accounts := []DeliveryAccountView{
		{
			Label:        "Akun Utama",
			Username:     "user@example.com",
			Password:     "secretpass123",
			Instructions: "Jangan mengubah kata sandi",
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
		t.Fatalf("RenderDeliveryEmail returned error: %v", err)
	}

	if !strings.Contains(html, `Akses Pesanan Aktif`) {
		t.Error("HTML should have correct title")
	}
	if !strings.Contains(html, `Budi Santoso`) {
		t.Error("HTML should contain customer name")
	}
	if !strings.Contains(html, `ORD-20261122-001`) {
		t.Error("HTML should contain order number")
	}
	if !strings.Contains(html, `Netflix Premium`) {
		t.Error("HTML should contain product name")
	}
	if !strings.Contains(html, `user@example.com`) {
		t.Error("HTML should contain account username")
	}
	if !strings.Contains(html, `secretpass123`) {
		t.Error("HTML should contain account password")
	}
	if !strings.Contains(html, `Jangan mengubah kata sandi`) {
		t.Error("HTML should contain warning not to change password")
	}
	if !strings.Contains(html, `Laporan Kendala`) {
		t.Error("HTML should contain help box")
	}

	checkForbidden(html, t)

	if len(html) >= 100*1024 {
		t.Errorf("HTML size %d bytes exceeds 100KB limit", len(html))
	}
}

func TestRenderReplacementEmail(t *testing.T) {
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
		t.Fatalf("RenderReplacementEmail returned error: %v", err)
	}

	if !strings.Contains(html, `Akun Pengganti`) {
		t.Error("HTML should have correct title")
	}
	if !strings.Contains(html, `Masa aktif akun sebelumnya berakhir`) {
		t.Error("HTML should contain replacement reason")
	}
	if !strings.Contains(html, `Tidak ada tagihan baru`) {
		t.Error("HTML should mention no new billing")
	}
	if !strings.Contains(html, `newuser@example.com`) {
		t.Error("HTML should contain replacement account username")
	}

	checkForbidden(html, t)

	if len(html) >= 100*1024 {
		t.Errorf("HTML size %d bytes exceeds 100KB limit", len(html))
	}
}

func TestRenderAccessCodeEmail_Escaping(t *testing.T) {
	input := AccessCodeEmailInput{
		CustomerName:  `<script>alert(1)</script>`,
		OrderNumber:   `"><iframe src="evil.com">`,
		ProductName:   `onclick="xss()"`,
		DurationName:  `Duration<script>`,
		AccountEmails: []string{`evil<script>@example.com`},
		AccessCode:    `CODE<script>`,
		ExpiresAt:     time.Now().Add(15 * time.Minute),
		FrontendURL:   `https://example.com`,
		CompanyName:   `NECLY`,
	}

	html, err := RenderAccessCodeEmail(input)
	if err != nil {
		t.Fatalf("RenderAccessCodeEmail returned error: %v", err)
	}

	// All user input should be escaped - verify no unescaped dangerous tags
	if strings.Contains(html, `<script>`) {
		t.Error("HTML should not contain unescaped <script>")
	}
	if strings.Contains(html, `<iframe`) {
		t.Error("HTML should not contain <iframe>")
	}

	checkForbidden(html, t)
}

func TestRenderDeliveryEmail_Escaping(t *testing.T) {
	accounts := []DeliveryAccountView{
		{
			Label:    `<script>alert(1)</script>`,
			Username: `"><iframe src="evil.com">`,
			Password: `onclick="xss()"`,
		},
	}

	input := DeliveryEmailInput{
		CustomerName:    `"><script>alert(1)</script>`,
		OrderNumber:     `ORD<script>`,
		ProductName:     `Product<iframe>`,
		DurationName:    `Duration`,
		Quantity:        1,
		AccessExpiresAt: time.Now().AddDate(0, 0, 30),
		Accounts:        accounts,
		FrontendURL:     `https://example.com`,
		CompanyName:     `NECLY`,
	}

	html, err := RenderDeliveryEmail(input)
	if err != nil {
		t.Fatalf("RenderDeliveryEmail returned error: %v", err)
	}

	checkForbidden(html, t)
}

func TestRenderReplacementEmail_Escaping(t *testing.T) {
	accounts := []DeliveryAccountView{
		{
			Label:    `<script>`,
			Username: `user<script>@example.com`,
			Password: `pass<iframe>`,
		},
	}

	input := ReplacementEmailInput{
		CustomerName:    `Name<script>`,
		OrderNumber:     `ORD<script>`,
		ProductName:     `Product<script>`,
		DurationName:    `Duration`,
		Quantity:        1,
		AccessExpiresAt: time.Now().AddDate(0, 0, 30),
		Accounts:        accounts,
		Reason:          `Reason<script>`,
		FrontendURL:     `https://example.com`,
		CompanyName:     `NECLY`,
	}

	html, err := RenderReplacementEmail(input)
	if err != nil {
		t.Fatalf("RenderReplacementEmail returned error: %v", err)
	}

	checkForbidden(html, t)
}
