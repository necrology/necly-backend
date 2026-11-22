package service

import (
	"strings"
	"testing"
	"time"

	"github.com/necly/services/internal/domain"
)

// helper order for the HTML email integration tests. Credentials and codes are
// dummy values; no real secret is ever rendered or logged here.
func htmlTestOrder() (*domain.Order, *domain.OrderItem) {
	item := &domain.OrderItem{
		ID: "11111111-1111-4111-8111-111111111111", ProductName: "ChatGPT Plus",
		DurationName: "30 hari", Quantity: 1,
	}
	order := &domain.Order{
		ID: "22222222-2222-4222-8222-222222222222", Number: "ORD-123",
		Customer: &domain.User{Name: "Alice", Email: "alice@example.com"},
		Items:    []domain.OrderItem{*item},
	}
	return order, item
}

func htmlTestAccounts() []deliveryAccount {
	return []deliveryAccount{{
		Label: "Akun utama", Username: "dummy-account@example.com",
		Password: "DUMMY-PASSWORD", Instructions: "Jangan ubah kata sandi.",
	}}
}

func TestBuildMessageIncludesHTMLAlternative(t *testing.T) {
	s := &OrderDeliveryService{frontendURL: "https://frontend.example"}
	order, item := htmlTestOrder()
	expires := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

	msg := s.buildMessage(order, item, htmlTestAccounts(), expires, "ABC123DEF4", expires)
	if msg.HTML == "" {
		t.Fatal("buildMessage must set the HTML alternative")
	}
	for _, needle := range []string{"ORD-123", "ChatGPT Plus", "DUMMY-PASSWORD"} {
		if !strings.Contains(msg.HTML, needle) {
			t.Errorf("HTML part missing %q", needle)
		}
	}
	if !strings.Contains(msg.Body, "ABC123DEF4") {
		t.Error("plain text fallback must keep the access code")
	}
	if !strings.Contains(msg.HTML, "https://frontend.example/laporan-kendala") {
		t.Error("HTML part must link to the report page built from frontendURL")
	}
}

func TestBuildReplacementMessageIncludesHTMLAlternative(t *testing.T) {
	s := &OrderDeliveryService{frontendURL: "https://frontend.example"}
	order, item := htmlTestOrder()
	expires := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	const reason = "Masa aktif akun sebelumnya berakhir"

	msg := s.buildReplacementMessage(order, item, htmlTestAccounts(), expires, reason)
	if msg.HTML == "" {
		t.Fatal("buildReplacementMessage must set the HTML alternative")
	}
	for _, needle := range []string{reason, "ORD-123", "DUMMY-PASSWORD"} {
		if !strings.Contains(msg.HTML, needle) {
			t.Errorf("HTML part missing %q", needle)
		}
	}
}

func TestBuildAccessCodeMessageIncludesHTMLAlternative(t *testing.T) {
	s := &OrderDeliveryService{frontendURL: "https://frontend.example"}
	order, item := htmlTestOrder()
	expires := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	const code = "ABC123DEF4"

	msg := s.buildAccessCodeMessage(order, item, []string{"dummy-account@example.com"}, code, expires)
	if msg.HTML == "" {
		t.Fatal("buildAccessCodeMessage must set the HTML alternative")
	}
	for _, needle := range []string{code, "ORD-123", "dummy-account@example.com", "Google"} {
		if !strings.Contains(msg.HTML, needle) {
			t.Errorf("HTML part missing %q", needle)
		}
	}
	if !strings.Contains(msg.Subject, "berlaku 5 menit") {
		t.Errorf("subject must state the access-code validity: %q", msg.Subject)
	}
	if !strings.Contains(msg.Body, code) {
		t.Error("plain text fallback must keep the access code")
	}
}

func TestWriteUsageInstructionsRequiresPasswordLogin(t *testing.T) {
	s := &OrderDeliveryService{}
	_, item := htmlTestOrder()
	var body strings.Builder
	s.writeUsageInstructions(&body, item, false)
	got := body.String()

	for _, needle := range []string{"Login WAJIB memakai kata sandi", "JANGAN login lewat Google, Apple, atau SSO"} {
		if !strings.Contains(got, needle) {
			t.Errorf("usage instructions missing %q:\n%s", needle, got)
		}
	}
}

func TestRenderEmailHTMLFailsClosed(t *testing.T) {
	if got := renderEmailHTML(func() (string, error) { return "", errTemplateStub }); got != "" {
		t.Fatalf("renderEmailHTML should return empty string on error, got %q", got)
	}
	if got := renderEmailHTML(func() (string, error) { return "   \n", nil }); got != "" {
		t.Fatalf("renderEmailHTML should reject blank output, got %q", got)
	}
}

var errTemplateStub = &stubTemplateError{}

type stubTemplateError struct{}

func (*stubTemplateError) Error() string { return "dummy template failure" }
