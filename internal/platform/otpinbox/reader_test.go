package otpinbox

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestParseMessageFindsCodeAddressedToTheAccount(t *testing.T) {
	raw := strings.Join([]string{
		"Delivered-To: family@example.test",
		"To: Family Account <family@example.test>",
		"From: Netflix <info@netflix.com>",
		"Subject: Your sign-in code",
		"Date: Tue, 21 Oct 2026 10:00:00 +0700",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		"Your verification code is 482913. It expires in 10 minutes.",
	}, "\r\n")

	message, ok := parseMessage([]byte(raw), "family@example.test", time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
	if !ok {
		t.Fatal("expected the addressed OTP message to be accepted")
	}
	if message.Code != "482913" {
		t.Fatalf("code = %q, want 482913", message.Code)
	}
}

func TestParseMessageRejectsAnotherCustomersMail(t *testing.T) {
	raw := strings.Join([]string{
		"Delivered-To: other@example.test",
		"To: other@example.test",
		"Subject: Your login code is 771204",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		"Use 771204 to sign in.",
	}, "\r\n")

	if _, ok := parseMessage([]byte(raw), "family@example.test", time.Now().Add(-time.Hour)); ok {
		t.Fatal("a message addressed to another account must never be returned")
	}
}

func TestParseMessageReadsHTMLMultipartAndQuotedPrintable(t *testing.T) {
	raw := strings.Join([]string{
		"To: family@example.test",
		"Subject: Kode verifikasi",
		"Content-Type: multipart/alternative; boundary=abc",
		"",
		"--abc",
		"Content-Type: text/html; charset=UTF-8",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"<p>Kode masuk Anda: <b>903412</b></p>",
		"--abc--",
	}, "\r\n")

	message, ok := parseMessage([]byte(raw), "family@example.test", time.Now().Add(-time.Hour))
	if !ok {
		t.Fatal("expected multipart HTML OTP to be accepted")
	}
	if message.Code != "903412" {
		t.Fatalf("code = %q, want 903412", message.Code)
	}
}

func TestParseMessageRejectsMessagesWithoutOTPContext(t *testing.T) {
	raw := strings.Join([]string{
		"To: family@example.test",
		"Subject: Invoice 12345678",
		"Content-Type: text/plain",
		"",
		"Your invoice reference is 12345678.",
	}, "\r\n")

	if _, ok := parseMessage([]byte(raw), "family@example.test", time.Now().Add(-time.Hour)); ok {
		t.Fatal("an invoice reference must not be treated as an OTP")
	}
}

func TestDisabledReaderFailsClosed(t *testing.T) {
	reader := New(Config{})
	if reader.Enabled() {
		t.Fatal("reader without a mailbox must be disabled")
	}
	if _, err := reader.Latest(context.Background(), "family@example.test", time.Now()); err != ErrNotConfigured {
		t.Fatalf("error = %v, want ErrNotConfigured", err)
	}
}
