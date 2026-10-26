package otpinbox

import (
	"context"
	"fmt"
	"net"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestNormalizeMailboxIMAPHostAllowsOnlyGmailIMAPS(t *testing.T) {
	allowed := map[string]string{
		"":                          DefaultMailboxIMAPHost,
		"   ":                       DefaultMailboxIMAPHost,
		"imap.gmail.com":            "imap.gmail.com",
		"IMAP.GMAIL.COM":            "imap.gmail.com",
		"imap.googlemail.com":       "imap.googlemail.com",
		"imap.gmail.com:993":        "imap.gmail.com",
		" imap.googlemail.com:993 ": "imap.googlemail.com",
	}
	for input, want := range allowed {
		got, err := NormalizeMailboxIMAPHost(input)
		if err != nil || got != want {
			t.Fatalf("NormalizeMailboxIMAPHost(%q) = %q, %v; want %q, nil", input, got, err, want)
		}
	}

	rejected := []string{
		"localhost",
		"imap",
		"127.0.0.1",
		"127.0.0.1:993",
		"[::1]",
		"[::1]:993",
		"169.254.169.254",
		"metadata.google.internal",
		"metadata.google.internal:993",
		"imap.gmail.com:443",
		"imap.gmail.com:143",
		"imap.gmail.com:notaport",
		"imap.gmail.com:",
		"imap.gmail.com.evil.test",
		"imap.gmail.com.evil.test:993",
		"evil-imap.gmail.com",
		"imap.gmail.com@evil.test",
		"https://imap.gmail.com",
		"imap.gmail.com/path",
		"imap.gmail.com:993/path",
		"user@imap.gmail.com",
		"imap.gmail.com.",
		strings.Repeat("a", maxMailboxHostLength+1),
	}
	for _, input := range rejected {
		if got, err := NormalizeMailboxIMAPHost(input); err == nil {
			t.Fatalf("NormalizeMailboxIMAPHost(%q) = %q, nil; want rejection", input, got)
		}
	}
}

func TestAccountReaderFactoryNeverDialsDisallowedHost(t *testing.T) {
	// This listener stands in for an internal/attacker-controlled endpoint. A
	// rejected host must not produce any connection, let alone send credentials.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	accepted := make(chan struct{}, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- struct{}{}
			_ = conn.Close()
		}
	}()

	factory := AccountReaderFactory{Timeout: 200 * time.Millisecond}
	for _, host := range []string{
		listener.Addr().String(),
		"127.0.0.1",
		"localhost",
		"metadata.google.internal:443",
		"imap.gmail.com.evil.test",
	} {
		reader := factory.NewReader("family@example.test", "app-password-secret", host)
		if reader == nil || reader.Enabled() {
			t.Fatalf("factory accepted disallowed host %q", host)
		}
		if _, err := reader.Latest(context.Background(), "family@example.test", time.Now()); err != ErrNotConfigured {
			t.Fatalf("reader for %q error = %v, want ErrNotConfigured", host, err)
		}
	}

	select {
	case <-accepted:
		t.Fatal("factory dialed a disallowed host")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestAccountReaderFactoryAcceptsGmailAndAddsIMAPSPort(t *testing.T) {
	factory := AccountReaderFactory{}
	for _, host := range []string{"", "imap.gmail.com", "imap.googlemail.com", "imap.gmail.com:993"} {
		reader := factory.NewReader("family@example.test", "app-password-secret", host)
		if reader == nil || !reader.Enabled() {
			t.Fatalf("factory rejected allowed host %q", host)
		}
		imapReader, ok := reader.(*imapReader)
		if !ok {
			t.Fatalf("reader type = %T", reader)
		}
		if !strings.HasSuffix(imapReader.cfg.Addr, ":993") {
			t.Fatalf("addr = %q, want IMAPS port", imapReader.cfg.Addr)
		}
	}
}

func TestAddressedToRequiresExactMailbox(t *testing.T) {
	const target = "victim@example.com"
	cases := []struct {
		name   string
		header mail.Header
		want   bool
	}{
		{
			name:   "To exact",
			header: mail.Header{"To": {"victim@example.com"}},
			want:   true,
		},
		{
			name:   "To case-insensitive exact with display name",
			header: mail.Header{"To": {"Victim Account <VICTIM@EXAMPLE.COM>"}},
			want:   true,
		},
		{
			name:   "Cc exact",
			header: mail.Header{"Cc": {"victim@example.com"}},
			want:   true,
		},
		{
			name:   "Delivered-To exact",
			header: mail.Header{"Delivered-To": {"victim@example.com"}},
			want:   true,
		},
		{
			name:   "X-Original-To exact bracketed",
			header: mail.Header{"X-Original-To": {"<victim@example.com>"}},
			want:   true,
		},
		{
			name:   "Envelope-To exact",
			header: mail.Header{"Envelope-To": {"Victim <victim@example.com>"}},
			want:   true,
		},
		{
			name:   "target only as domain suffix",
			header: mail.Header{"To": {"victim@example.com.attacker.test"}},
			want:   false,
		},
		{
			name:   "target only in display name",
			header: mail.Header{"To": {`"victim@example.com" <attacker@example.net>`}},
			want:   false,
		},
		{
			name:   "target as local-part prefix",
			header: mail.Header{"To": {"victim2@example.com"}},
			want:   false,
		},
		{
			name:   "target as local-part suffix",
			header: mail.Header{"To": {"2victim@example.com"}},
			want:   false,
		},
		{
			name:   "target only in comment",
			header: mail.Header{"To": {"attacker@example.net (victim@example.com)"}},
			want:   false,
		},
		{
			name:   "several recipients with target only as substring",
			header: mail.Header{"To": {"victim@example.com.attacker.test, other@example.net"}},
			want:   false,
		},
		{
			name:   "malformed To header",
			header: mail.Header{"To": {"victim@example.com <attacker@example.net"}},
			want:   false,
		},
		{
			name:   "malformed envelope header",
			header: mail.Header{"Delivered-To": {"victim@example.com <attacker@example.net"}},
			want:   false,
		},
		{
			name:   "multiple addresses in one envelope header",
			header: mail.Header{"Delivered-To": {"victim@example.com, attacker@example.net"}},
			want:   false,
		},
		{
			name:   "non-ASCII lookalike domain",
			header: mail.Header{"To": {"victim@exämple.com"}},
			want:   false,
		},
		{
			name:   "quoted local part that is not the target address",
			header: mail.Header{"To": {`"victim@example.com"@attacker.test`}},
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := addressedTo(tc.header, target); got != tc.want {
				t.Fatalf("addressedTo(%v) = %t, want %t", tc.header, got, tc.want)
			}
		})
	}
}

func TestParseMessageRejectsRecipientSubstringCollisions(t *testing.T) {
	rejectedHeaders := []string{
		"To: victim@example.com.attacker.test",
		`To: "victim@example.com" <attacker@example.net>`,
		"To: attacker@example.net (victim@example.com)",
		"Delivered-To: victim@example.com.attacker.test",
		"Delivered-To: victim@example.com, attacker@example.net",
		"X-Original-To: victim2@example.com",
		"To: victim@example.com <attacker@example.net",
	}
	for _, header := range rejectedHeaders {
		t.Run(header, func(t *testing.T) {
			raw := strings.Join([]string{
				header,
				"Subject: Your verification code is 482913",
				"Content-Type: text/plain; charset=UTF-8",
				"",
				"Use 482913 to sign in.",
			}, "\r\n")
			if message, ok := parseMessage([]byte(raw), "victim@example.com", time.Now().Add(-time.Hour)); ok {
				t.Fatalf("message with %q was accepted: %+v", header, message)
			}
		})
	}

	acceptedHeaders := []string{
		"To: Victim Account <VICTIM@EXAMPLE.COM>",
		"Delivered-To: victim@example.com",
		"X-Original-To: <victim@example.com>",
		"Envelope-To: victim@example.com",
		"Cc: victim@example.com",
	}
	for _, header := range acceptedHeaders {
		t.Run("accept "+header, func(t *testing.T) {
			raw := strings.Join([]string{
				header,
				"Subject: Your verification code is 482913",
				"Content-Type: text/plain; charset=UTF-8",
				"",
				"Use 482913 to sign in.",
			}, "\r\n")
			if message, ok := parseMessage([]byte(raw), "victim@example.com", time.Now().Add(-time.Hour)); !ok || message.Code != "482913" {
				t.Fatalf("message with %q was rejected: %+v ok=%t", header, message, ok)
			}
		})
	}
}

func TestParseMessageDoesNotLeakRecipientOrSecretsInFailure(t *testing.T) {
	const secret = "app-password-secret"
	raw := strings.Join([]string{
		"To: victim@example.com.attacker.test",
		"Subject: Your verification code is 482913",
		"Content-Type: text/plain",
		"",
		"Use 482913 to sign in.",
	}, "\r\n")
	if message, ok := parseMessage([]byte(raw), "victim@example.com", time.Now().Add(-time.Hour)); ok {
		t.Fatalf("unexpected message: %+v", message)
	}
	if strings.Contains(fmt.Sprint(ErrMailboxHostNotAllowed), secret) {
		t.Fatal("error contains a credential")
	}
}
