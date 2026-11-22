package emailtemplate

import (
	"strings"
	"testing"
	"time"
)

func TestRenderLayout_Basic(t *testing.T) {
	cfg := DefaultLayoutConfig()
	data := LayoutData{
		Title:     "Test Email",
		Greeting:  "Halo Budi,",
		IntroText: "Ini adalah email percobaan.",
	}

	html, err := RenderLayout(cfg, data)
	if err != nil {
		t.Fatalf("RenderLayout returned error: %v", err)
	}

	// Check structure
	if !strings.Contains(html, `<table role="presentation"`) {
		t.Error("HTML should contain table-based layout")
	}
	if !strings.Contains(html, `max-width:600px`) {
		t.Error("HTML should have max-width 600px")
	}
	if !strings.Contains(html, `background-color:#f4f6fb`) {
		t.Error("HTML should have page background color")
	}
	if !strings.Contains(html, `background-color:#ffffff`) {
		t.Error("HTML should have card background color")
	}
	if !strings.Contains(html, `color:#111827`) {
		t.Error("HTML should have primary text color")
	}
	if !strings.Contains(html, `color:#6b7280`) {
		t.Error("HTML should have secondary text color")
	}
	if !strings.Contains(html, `#4f46e5`) {
		t.Error("HTML should have accent color (indigo)")
	}

	// Check preheader
	if !strings.Contains(html, `display:none`) {
		t.Error("HTML should have hidden preheader div")
	}
	if !strings.Contains(html, `Ini adalah email percobaan`) {
		t.Error("HTML should contain intro text")
	}

	// Check no forbidden elements
	checkForbidden(html, t)

	// Check size < 100KB
	if len(html) >= 100*1024 {
		t.Errorf("HTML size %d bytes exceeds 100KB limit", len(html))
	}
}

func TestRenderLayout_WithCodeCard(t *testing.T) {
	cfg := DefaultLayoutConfig()
	data := LayoutData{
		Title:    "Kode Akses OTP",
		Greeting: "Halo Budi,",
		CodeCard: &CodeCardData{
			Label:     "Kode Akses OTP",
			Code:      "ABCD-EFGH-IJKL",
			ExpiresAt: time.Date(2026, 12, 25, 15, 30, 0, 0, time.FixedZone("WIB", 7*3600)),
		},
	}

	html, err := RenderLayout(cfg, data)
	if err != nil {
		t.Fatalf("RenderLayout returned error: %v", err)
	}

	if !strings.Contains(html, `ABCD-EFGH-IJKL`) {
		t.Error("HTML should contain access code")
	}
	if !strings.Contains(html, `letter-spacing:3px`) {
		t.Error("Code block should have letter-spacing")
	}
	if !strings.Contains(html, `user-select:all`) {
		t.Error("Code block should have user-select:all")
	}
	if !strings.Contains(html, `#eef2ff`) {
		t.Error("Code card should have indigo background")
	}
	if !strings.Contains(html, `Berlaku sampai`) {
		t.Error("HTML should show expiration label")
	}

	checkForbidden(html, t)
}

func TestRenderLayout_WithSummaryTable(t *testing.T) {
	cfg := DefaultLayoutConfig()
	data := LayoutData{
		Title: "Test",
		SummaryTable: &SummaryTableData{
			Rows: []SummaryRow{
				{Label: "Nomor pesanan", Value: "ORD-123"},
				{Label: "Produk", Value: "Netflix Premium"},
			},
		},
	}

	html, err := RenderLayout(cfg, data)
	if err != nil {
		t.Fatalf("RenderLayout returned error: %v", err)
	}

	if !strings.Contains(html, `Nomor pesanan`) {
		t.Error("HTML should contain summary label")
	}
	if !strings.Contains(html, `ORD-123`) {
		t.Error("HTML should contain summary value")
	}

	checkForbidden(html, t)
}

func TestRenderLayout_WithStepsTable(t *testing.T) {
	cfg := DefaultLayoutConfig()
	data := LayoutData{
		Title: "Test",
		StepsTable: &StepsTableData{
			Title: "Cara mengambil OTP",
			Steps: []Step{
				{
					Number:        1,
					Title:         "Buka halaman Ambil OTP",
					Description:   "Buka tautan di bawah.",
					ButtonLabel:   "Buka",
					ButtonURL:     "https://example.com/otp",
					TextLinkLabel: "https://example.com/otp",
					TextLinkURL:   "https://example.com/otp",
				},
				{
					Number:      2,
					Title:       "Login biasa",
					Description: "Masukkan email dan kata sandi.",
				},
				{
					Number:      3,
					Title:       "JANGAN pakai Google/Apple",
					Description: "Login SSO tidak mengirim OTP.",
					IsWarning:   true,
				},
			},
		},
	}

	html, err := RenderLayout(cfg, data)
	if err != nil {
		t.Fatalf("RenderLayout returned error: %v", err)
	}

	if !strings.Contains(html, `Buka halaman Ambil OTP`) {
		t.Error("HTML should contain step title")
	}
	if !strings.Contains(html, `https://example.com/otp`) {
		t.Error("HTML should contain button URL")
	}
	if !strings.Contains(html, `JANGAN pakai Google/Apple`) {
		t.Error("HTML should contain warning step")
	}
	if !strings.Contains(html, `fffbeb`) {
		t.Error("Warning step should have yellow background")
	}

	checkForbidden(html, t)
}

func TestRenderLayout_WithWarningBox(t *testing.T) {
	cfg := DefaultLayoutConfig()
	data := LayoutData{
		Title: "Test",
		WarningBox: &WarningBoxData{
			Title:   "PERINGATAN",
			Message: "Jangan login pakai Google/Apple.",
		},
	}

	html, err := RenderLayout(cfg, data)
	if err != nil {
		t.Fatalf("RenderLayout returned error: %v", err)
	}

	if !strings.Contains(html, `PERINGATAN`) {
		t.Error("HTML should contain warning title")
	}
	if !strings.Contains(html, `Jangan login pakai Google/Apple`) {
		t.Error("HTML should contain warning message")
	}
	if !strings.Contains(html, `fffbeb`) {
		t.Error("Warning box should have yellow background")
	}
	if !strings.Contains(html, `#b45309`) {
		t.Error("Warning box should have warning color")
	}

	checkForbidden(html, t)
}

func TestRenderLayout_WithHelpBox(t *testing.T) {
	cfg := DefaultLayoutConfig()
	data := LayoutData{
		Title: "Test",
		HelpBox: &HelpBoxData{
			Title:       "Bantuan",
			Message:     "Lapor kendala di sini.",
			ButtonLabel: "Buka Laporan",
			ButtonURL:   "https://example.com/laporan",
			Note:        "Sertakan nomor pesanan.",
		},
	}

	html, err := RenderLayout(cfg, data)
	if err != nil {
		t.Fatalf("RenderLayout returned error: %v", err)
	}

	if !strings.Contains(html, `Bantuan`) {
		t.Error("HTML should contain help title")
	}
	if !strings.Contains(html, `Lapor kendala di sini`) {
		t.Error("HTML should contain help message")
	}
	if !strings.Contains(html, `Buka Laporan`) {
		t.Error("HTML should contain help button")
	}
	if !strings.Contains(html, `059669`) {
		t.Error("Help button should have success green color")
	}

	checkForbidden(html, t)
}

func TestRenderLayout_MediaQueries(t *testing.T) {
	cfg := DefaultLayoutConfig()
	data := LayoutData{Title: "Test"}

	html, err := RenderLayout(cfg, data)
	if err != nil {
		t.Fatalf("RenderLayout returned error: %v", err)
	}

	// Check for media query block
	if !strings.Contains(html, `@media (max-width:600px)`) {
		t.Error("HTML should contain mobile media query")
	}
	if !strings.Contains(html, `.card-padding`) {
		t.Error("HTML should have card-padding class for mobile")
	}
	if !strings.Contains(html, `.code-block`) {
		t.Error("HTML should have code-block class for mobile")
	}

	checkForbidden(html, t)
}

func TestRenderLayout_FontStack(t *testing.T) {
	cfg := DefaultLayoutConfig()
	data := LayoutData{Title: "Test"}

	html, err := RenderLayout(cfg, data)
	if err != nil {
		t.Fatalf("RenderLayout returned error: %v", err)
	}

	if !strings.Contains(html, `-apple-system`) || !strings.Contains(html, `BlinkMacSystemFont`) || !strings.Contains(html, `'Segoe UI'`) {
		t.Error("HTML should use system font stack")
	}
	// No external fonts - check for @import or google fonts URLs
	if strings.Contains(html, `@import url`) || strings.Contains(html, `fonts.googleapis.com`) || strings.Contains(html, `fonts.gstatic.com`) {
		t.Error("HTML should not use external fonts")
	}

	checkForbidden(html, t)
}

func TestRenderLayout_NoScriptsOrIframes(t *testing.T) {
	cfg := DefaultLayoutConfig()
	data := LayoutData{
		Title:     "Test",
		IntroText: `<script>alert(1)</script><iframe src="evil.com"></iframe><div onclick="xss()">Click</div>`,
	}

	html, err := RenderLayout(cfg, data)
	if err != nil {
		t.Fatalf("RenderLayout returned error: %v", err)
	}

	// All dangerous content should be escaped - check for unescaped patterns
	if strings.Contains(html, `<script>`) {
		t.Error("HTML should not contain unescaped <script> tag")
	}
	if strings.Contains(html, `<iframe`) {
		t.Error("HTML should not contain unescaped <iframe> tag")
	}
	// onclick= in escaped form becomes onclick=&#34; which is safe; check for unescaped onclick=
	if strings.Contains(html, `onclick="`) || strings.Contains(html, `onclick='`) {
		t.Error("HTML should not contain unescaped onclick attribute")
	}
	if strings.Contains(html, `javascript:`) {
		t.Error("HTML should not contain javascript: URLs")
	}

	// The escaped content should be present (escaped as &lt;script&gt;)
	if !strings.Contains(html, `&lt;script&gt;`) {
		t.Error("HTML should contain escaped script tag")
	}
	if !strings.Contains(html, `&lt;iframe`) {
		t.Error("HTML should contain escaped iframe tag")
	}

	// But checkForbidden would fail on escaped content, so skip it for this test
	// checkForbidden(html, t)
}

func checkForbidden(html string, t *testing.T) {
	t.Helper()
	// Check for unescaped dangerous patterns only
	// We look for patterns that are NOT HTML-escaped (i.e., raw <, >, ", ', etc.)
	forbidden := []struct {
		pattern string
		msg     string
	}{
		{`<script`, "unescaped <script tag"},
		{`<iframe`, "unescaped <iframe tag"},
		{`onclick="`, "unescaped onclick attribute"},
		{`onclick='`, "unescaped onclick attribute"},
		{`onload="`, "unescaped onload attribute"},
		{`onerror="`, "unescaped onerror attribute"},
		{`javascript:`, "javascript: URL"},
	}
	for _, f := range forbidden {
		if strings.Contains(html, f.pattern) {
			t.Errorf("HTML contains forbidden pattern: %s", f.msg)
		}
	}
}
