package service

import (
	"os"
	"testing"
	"time"

	"github.com/necly/services/internal/platform/emailtemplate"
)

// TestDumpCredentialHighlightEmails renders delivery and replacement emails
// with synthetic credentials so the operator can inspect the real HTML.
func TestDumpCredentialHighlightEmails(t *testing.T) {
	out := os.Getenv("NECLY_EMAIL_OUT")
	if out == "" {
		out = "/tmp/necly-cred-fixed"
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	expires := time.Date(2026, 12, 6, 4, 30, 0, 0, time.UTC)
	acc1 := emailtemplate.DeliveryAccountView{
		Label:    "Akun 1",
		Username: "buyer-account@example.test",
		Password: "Example-Pass-1",
	}
	acc2 := emailtemplate.DeliveryAccountView{
		Label:    "Akun 2",
		Username: "second-account@example.test",
		Password: "Example-Pass-2",
	}

	one := emailtemplate.DeliveryEmailInput{
		CustomerName:        "Maulana",
		OrderNumber:         "ORD-20261122-0041",
		ProductName:         "ChatGPT Plus 1 Bulan",
		DurationName:        "1 Bulan",
		Quantity:            1,
		Accounts:            []emailtemplate.DeliveryAccountView{acc1},
		AccessCode:          "AB3C5DEF7G",
		AccessCodeExpiresAt: expires,
		AccessExpiresAt:     expires,
		FrontendURL:         "https://necly-service.my.id",
	}
	two := one
	two.OrderNumber = "ORD-20261122-0042"
	two.Quantity = 2
	two.Accounts = []emailtemplate.DeliveryAccountView{acc1, acc2}

	rep := emailtemplate.ReplacementEmailInput{
		CustomerName:    "Maulana",
		OrderNumber:     "ORD-20261122-0041",
		ProductName:     "ChatGPT Plus 1 Bulan",
		DurationName:    "1 Bulan",
		Reason:          "Masa aktif akun sebelumnya telah berakhir.",
		Accounts:        []emailtemplate.DeliveryAccountView{acc1},
		AccessExpiresAt: expires,
		FrontendURL:     "https://necly-service.my.id",
	}

	// Escaping fixture: hostile values must not break out of the HTML.
	esc := one
	esc.OrderNumber = "ORD-20261122-0043"
	esc.Accounts = []emailtemplate.DeliveryAccountView{{
		Label:    "<script>alert(1)</script>",
		Username: "weird+alias&co@example.test",
		Password: `Pass"<b>Plus</b>&more`,
	}}

	cases := []struct {
		name string
		html string
	}{
		{"1-delivery-one-account.html", mustRender(t, func() (string, error) { return emailtemplate.RenderDeliveryEmail(one) })},
		{"2-delivery-two-accounts.html", mustRender(t, func() (string, error) { return emailtemplate.RenderDeliveryEmail(two) })},
		{"3-replacement.html", mustRender(t, func() (string, error) { return emailtemplate.RenderReplacementEmail(rep) })},
		{"4-escaping.html", mustRender(t, func() (string, error) { return emailtemplate.RenderDeliveryEmail(esc) })},
	}

	for _, tc := range cases {
		path := out + "/" + tc.name
		if err := os.WriteFile(path, []byte(tc.html), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		t.Logf("wrote %s (%d bytes)", path, len(tc.html))
	}
}

func mustRender(t *testing.T, fn func() (string, error)) string {
	t.Helper()
	s, err := fn()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if s == "" {
		t.Fatal("render returned empty string")
	}
	return s
}
