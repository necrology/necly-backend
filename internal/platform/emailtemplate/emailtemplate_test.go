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

	// Alur 4 langkah: login kata sandi dulu, form OTP kedua, halaman OTP ketiga.
	stepCount := strings.Count(html, `background-color:#4f46e5;color:#ffffff;font-size:13px;font-weight:700;line-height:1;">`)
	if stepCount < 4 {
		t.Errorf("Expected at least 4 steps, found %d", stepCount)
	}

	// Check specific step content
	if !strings.Contains(html, `JANGAN pilih Continue with Google`) && !strings.Contains(html, `JANGAN login pakai Google`) {
		t.Error("HTML should contain warning about not using Google/Apple login")
	}
	if !strings.Contains(html, `Continue with Google`) && !strings.Contains(html, `Sign in with Apple`) {
		t.Error("HTML should mention specific SSO providers to avoid")
	}
	if !strings.Contains(html, `JANGAN tutup halaman`) {
		t.Error("HTML should tell the user to keep the OTP form open")
	}

	// URUTAN: login kata sandi < form OTP < halaman Ambil OTP.
	loginIdx := strings.Index(html, `kata sandi akun`)
	formIdx := strings.Index(html, `Tunggu form kode OTP muncul`)
	otpIdx := strings.Index(html, `Buka Halaman Ambil OTP`)
	if loginIdx < 0 || formIdx < 0 || otpIdx < 0 {
		t.Fatalf("missing markers login=%d form=%d otp=%d", loginIdx, formIdx, otpIdx)
	}
	if !(loginIdx < formIdx && formIdx < otpIdx) {
		t.Errorf("urutan harus login < form OTP < halaman OTP (login=%d form=%d otp=%d)", loginIdx, formIdx, otpIdx)
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

func TestRenderDeliveryEmail_WithAccessCode(t *testing.T) {
	accounts := []DeliveryAccountView{
		{
			Label:        "Akun Utama",
			Username:     "user@example.com",
			Password:     "secretpass123",
			Instructions: "Jangan mengubah kata sandi",
		},
	}

	input := DeliveryEmailInput{
		CustomerName:        "Budi Santoso",
		OrderNumber:         "ORD-20261122-001",
		ProductName:         "Netflix Premium",
		DurationName:        "1 Bulan",
		Quantity:            1,
		AccessExpiresAt:     time.Date(2026, 12, 22, 0, 0, 0, 0, time.UTC),
		Accounts:            accounts,
		FrontendURL:         "https://necly.example",
		CompanyName:         "NECLY Services",
		AccessCode:          "XK7M9PQ2R4",
		AccessCodeExpiresAt: time.Date(2026, 11, 22, 15, 35, 0, 0, time.FixedZone("WIB", 7*3600)),
	}

	html, err := RenderDeliveryEmail(input)
	if err != nil {
		t.Fatalf("RenderDeliveryEmail returned error: %v", err)
	}

	// Should contain access code card
	if !strings.Contains(html, "Kode Akses Necly (bukan OTP 6 digit)") {
		t.Error("HTML should contain access code card label")
	}
	if !strings.Contains(html, "XK7M9PQ2R4") {
		t.Error("HTML should contain access code")
	}
	if !strings.Contains(html, "Masa berlaku") {
		t.Error("HTML should contain expires label")
	}

	// Should contain mandatory box
	if !strings.Contains(html, "WAJIB — Login pakai KATA SANDI, form OTP harus terbuka dulu") {
		t.Error("HTML should contain mandatory box")
	}
	if !strings.Contains(html, "Continue with Google") || !strings.Contains(html, "Sign in with Apple") {
		t.Error("HTML should mention specific SSO providers to avoid")
	}

	// Alur 4 langkah: login kata sandi dulu, form OTP kedua, halaman OTP ketiga.
	stepCount := strings.Count(html, `background-color:#4f46e5;color:#ffffff;font-size:13px;font-weight:700;line-height:1;">`)
	if stepCount < 4 {
		t.Errorf("Expected at least 4 steps, found %d", stepCount)
	}

	// Check specific step content
	if !strings.Contains(html, "Buka halaman Ambil OTP Necly dan masukkan email akun + kode akses") {
		t.Error("HTML should contain the step that opens the OTP page with the access code")
	}
	if !strings.Contains(html, "pakai email + kata sandi akun") {
		t.Error("HTML should contain step 1: login with password")
	}
	if !strings.Contains(html, "Continue with Google") {
		t.Error("HTML should warn against Google/Apple/SSO")
	}
	if !strings.Contains(html, "Tunggu form kode OTP muncul") {
		t.Error("HTML should contain the step where the OTP form appears")
	}
	if !strings.Contains(html, "Ambil kode OTP-nya") {
		t.Error("HTML should contain the step that copies the 6-digit OTP")
	}

	checkForbidden(html, t)

	if len(html) >= 100*1024 {
		t.Errorf("HTML size %d bytes exceeds 100KB limit", len(html))
	}
}

func TestRenderDeliveryEmail_WithoutAccessCode(t *testing.T) {
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
		AccessCode:      "",
	}

	html, err := RenderDeliveryEmail(input)
	if err != nil {
		t.Fatalf("RenderDeliveryEmail returned error: %v", err)
	}

	// Should NOT contain access code card
	if strings.Contains(html, "Kode Akses Necly (bukan OTP 6 digit)") {
		t.Error("HTML should NOT contain access code card label when AccessCode is empty")
	}
	if strings.Contains(html, "Masa berlaku") {
		t.Error("HTML should NOT contain expires label when AccessCode is empty")
	}

	// Should still contain mandatory box and steps (they are always shown now)
	if !strings.Contains(html, "WAJIB — Login pakai KATA SANDI, form OTP harus terbuka dulu") {
		t.Error("HTML should still contain mandatory box")
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
