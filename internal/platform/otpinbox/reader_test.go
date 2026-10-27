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

func TestParseMessageIgnoresPasswordChangeMail(t *testing.T) {
	cases := []struct {
		name    string
		subject string
		body    string
	}{
		{
			name:    "english reset",
			subject: "Reset your password",
			body:    "Use code 551122 to reset your password. The code expires in 15 minutes.",
		},
		{
			name:    "english change",
			subject: "Change your password",
			body:    "Your verification code is 318824.",
		},
		{
			name:    "english changed notification",
			subject: "Your password has been changed",
			body:    "If this was not you, use code 774411 to secure your account.",
		},
		{
			name:    "english new password",
			subject: "Kode verifikasi",
			body:    "Enter 445566 to choose a new password.",
		},
		{
			name:    "indonesian subject",
			subject: "Ubah kata sandi akun Anda",
			body:    "Kode verifikasi: 883311.",
		},
		{
			name:    "indonesian body",
			subject: "Kode verifikasi",
			body:    "Masukkan 660022 untuk mengubah kata sandi Anda.",
		},
		{
			name:    "indonesian password replaced",
			subject: "Sandi berhasil diubah",
			body:    "Kata sandi akun Anda telah berhasil diubah. Kode 998877.",
		},
		{
			name:    "reset password without possessive",
			subject: "Verification code",
			body:    "Use 230194 before you reset password.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := strings.Join([]string{
				"To: family@example.test",
				"Subject: " + tc.subject,
				"Content-Type: text/plain; charset=UTF-8",
				"",
				tc.body,
			}, "\r\n")

			if message, ok := parseMessage([]byte(raw), "family@example.test", time.Now().Add(-time.Hour)); ok {
				t.Fatalf("password change mail was returned as OTP: %+v", message)
			}
		})
	}
}

func TestParseMessageKeepsLoginOTPWhenPasswordIsOnlyMentioned(t *testing.T) {
	cases := []struct {
		name    string
		subject string
		body    string
		code    string
	}{
		{
			name:    "password warning",
			subject: "Kode masuk",
			body:    "Kode OTP Anda 472819. Jangan bagikan kata sandi Anda kepada siapa pun.",
			code:    "472819",
		},
		{
			name:    "new sign in",
			subject: "New sign-in to your account",
			body:    "Your login code is 731902. Never share your password.",
			code:    "731902",
		},
		{
			name:    "password policy",
			subject: "Your verification code",
			body:    "We updated our password policy. Your code is 551234.",
			code:    "551234",
		},
		{
			name:    "login code with password word",
			subject: "Kode OTP login",
			body:    "Masukkan kode 824113. Kata sandi akun tidak boleh dibagikan.",
			code:    "824113",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := strings.Join([]string{
				"To: family@example.test",
				"Subject: " + tc.subject,
				"Content-Type: text/plain; charset=UTF-8",
				"",
				tc.body,
			}, "\r\n")

			message, ok := parseMessage([]byte(raw), "family@example.test", time.Now().Add(-time.Hour))
			if !ok {
				t.Fatal("expected a login OTP to be accepted")
			}
			if message.Code != tc.code {
				t.Fatalf("code = %q, want %q", message.Code, tc.code)
			}
		})
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
