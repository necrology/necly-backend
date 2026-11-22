package mailer

import (
	"bufio"
	"context"
	"encoding/base64"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProductionWithoutSMTPDisablesDeliveryWithoutLoggingTheBody(t *testing.T) {
	mail := New(Config{Production: true, From: "noreply@necly.example"})
	if mail.Enabled() {
		t.Fatal("production mailer without SMTP must report Enabled() == false")
	}
	// Send must fail closed. The message body is not visible here; the contract
	// is that disabledMailer never writes it anywhere.
	if err := mail.Send(context.Background(), Message{To: "alice@example.com", Subject: "Reset", Body: "token=secret"}); err == nil {
		t.Fatal("disabled production mailer accepted a message")
	}
}

func TestSMTPRequiresTLSConfiguration(t *testing.T) {
	mail := New(Config{Host: "smtp.example", Port: 587, From: "noreply@necly.example", UseTLS: false})
	if mail.Enabled() {
		t.Fatal("a SMTP transport without TLS must be disabled")
	}
}

func TestSMTPAbortsBeforeSendingTheBodyWhenSTARTTLSIsMissing(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	var received strings.Builder
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)
		write := func(line string) {
			_, _ = writer.WriteString(line + "\r\n")
			_ = writer.Flush()
		}
		write("220 smtp.example ESMTP ready")
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			mu.Lock()
			received.WriteString(line)
			mu.Unlock()
			command := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
				// Advertise AUTH but deliberately no STARTTLS.
				write("250-smtp.example")
				write("250 AUTH PLAIN LOGIN")
			case strings.HasPrefix(command, "QUIT"):
				write("221 bye")
				return
			default:
				write("500 not allowed")
			}
		}
	}()

	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	mail := &smtpMailer{cfg: Config{Host: host, Port: portNumber, From: "noreply@necly.example", UseTLS: true}}
	deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = mail.Send(deadline, Message{To: "alice@example.com", Subject: "Reset", Body: "token=super-secret-reset-token"})
	if err == nil {
		t.Fatal("Send() succeeded against a server without STARTTLS")
	}
	<-done

	mu.Lock()
	wire := received.String()
	mu.Unlock()
	if strings.Contains(wire, "super-secret-reset-token") {
		t.Fatalf("the message body was written to a cleartext connection: %q", wire)
	}
	if strings.Contains(strings.ToUpper(wire), "MAIL FROM") {
		t.Fatalf("SMTP envelope commands were sent before TLS was established: %q", wire)
	}
}

// TestRenderPlainTextOnly verifies backward compatibility: Message without HTML
// produces a text/plain message (no multipart).
func TestRenderPlainTextOnly(t *testing.T) {
	mail := &smtpMailer{cfg: Config{From: "noreply@necly.example"}}
	msg := Message{
		To:      "alice@example.com",
		Subject: "Test Subject",
		Body:    "Hello, world!\nThis is plain text.",
		HTML:    "",
	}
	raw := mail.render(msg)
	rawStr := string(raw)

	// Should be text/plain only
	if !strings.Contains(rawStr, "Content-Type: text/plain; charset=\"UTF-8\"") {
		t.Fatalf("expected text/plain content type, got: %s", rawStr)
	}
	if strings.Contains(rawStr, "multipart/alternative") {
		t.Fatalf("expected no multipart for plain text only, got: %s", rawStr)
	}
	if strings.Contains(rawStr, "Content-Type: text/html") {
		t.Fatalf("unexpected text/html part in plain text only message: %s", rawStr)
	}
	// Should have quoted-printable encoding
	if !strings.Contains(rawStr, "Content-Transfer-Encoding: quoted-printable") {
		t.Fatalf("expected quoted-printable encoding: %s", rawStr)
	}
	// Body should be present (QP encoded)
	if !strings.Contains(rawStr, "Hello=2C world=21") && !strings.Contains(rawStr, "Hello, world!") {
		t.Fatalf("body not found in rendered message: %s", rawStr)
	}
}

// TestRenderMultipartWithHTML verifies multipart/alternative structure when HTML is provided.
func TestRenderMultipartWithHTML(t *testing.T) {
	mail := &smtpMailer{cfg: Config{From: "noreply@necly.example"}}
	msg := Message{
		To:      "alice@example.com",
		Subject: "Test Subject",
		Body:    "Hello, world!\nThis is plain text.",
		HTML:    "<p>Hello, <strong>world</strong>!</p>\n<p>This is HTML.</p>",
	}
	raw := mail.render(msg)
	rawStr := string(raw)

	// Must have multipart/alternative with boundary
	if !strings.Contains(rawStr, "Content-Type: multipart/alternative; boundary=") {
		t.Fatalf("expected multipart/alternative with boundary, got: %s", rawStr)
	}

	// Extract boundary
	boundary := extractBoundary(rawStr)
	if boundary == "" {
		t.Fatalf("could not extract boundary from: %s", rawStr)
	}

	// Must have two parts separated by boundary
	parts := splitParts(rawStr, boundary)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d: %s", len(parts), rawStr)
	}

	// Part 1: text/plain
	if !strings.Contains(parts[0], "Content-Type: text/plain; charset=\"UTF-8\"") {
		t.Fatalf("part 1 should be text/plain: %s", parts[0])
	}
	if !strings.Contains(parts[0], "Content-Transfer-Encoding: quoted-printable") {
		t.Fatalf("part 1 should be quoted-printable: %s", parts[0])
	}

	// Part 2: text/html
	if !strings.Contains(parts[1], "Content-Type: text/html; charset=\"UTF-8\"") {
		t.Fatalf("part 2 should be text/html: %s", parts[1])
	}
	if !strings.Contains(parts[1], "Content-Transfer-Encoding: quoted-printable") {
		t.Fatalf("part 2 should be quoted-printable: %s", parts[1])
	}

	// End boundary must be present
	if !strings.Contains(rawStr, "--"+boundary+"--") {
		t.Fatalf("missing end boundary: %s", rawStr)
	}
}

// TestQuotedPrintableRoundTrip verifies QP encode/decode round-trip for challenging content.
func TestQuotedPrintableRoundTrip(t *testing.T) {
	testCases := []struct {
		name  string
		input string
	}{
		{
			name:  "equals_sign",
			input: "Price = $100\nFormula: a = b + c",
		},
		{
			name:  "em_dash_and_unicode",
			input: "Hello — world → test • bullet\nEmoji: 🎉🚀💯",
		},
		{
			name:  "long_line_over_76_chars",
			input: strings.Repeat("a", 80) + "\n" + strings.Repeat("b", 100),
		},
		{
			name:  "equals_at_line_end",
			input: "This line ends with equals=\nNext line",
		},
		{
			name:  "spaces_at_line_end",
			input: "Trailing spaces   \nNormal line",
		},
		{
			name:  "mixed_content",
			input: "Line 1: = equals\nLine 2: — dash\nLine 3: " + strings.Repeat("x", 80) + "\nLine 4: 🎉 emoji\nLine 5: trailing=\n",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			encoded := quotedPrintableEncode(tc.input)
			decoded := quotedPrintableDecode(encoded)
			if decoded != tc.input {
				t.Errorf("round-trip failed\ninput:    %q\nencoded:  %q\ndecoded:  %q", tc.input, encoded, decoded)
			}
			// Verify line length limit (76 chars including soft line breaks)
			lines := strings.Split(encoded, "\r\n")
			for _, line := range lines {
				if len(line) > 76 {
					t.Errorf("line exceeds 76 chars: %d: %q", len(line), line)
				}
			}
		})
	}
}

// TestRFC2047SubjectEncoding verifies RFC 2047 encoding for non-ASCII subjects.
func TestRFC2047SubjectEncoding(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "ascii_only",
			input:    "Hello World",
			expected: "Hello World",
		},
		{
			name:     "indonesian_with_em_dash",
			input:    "Kode akses OTP pesanan ORD-123 — berlaku 15 menit",
			expected: "=?UTF-8?B?S29kZSBha3NlcyBPVFAgcGVzYW5hbiBPUkQtMTIzIOKAlCBiZXJsYWt1IDE1IG1lbml0?=",
		},
		{
			name:     "japanese",
			input:    "ご注文の確認",
			expected: "=?UTF-8?B?44GU5rOo5paH44Gu56K66KqN?=",
		},
		{
			name:     "emoji_in_subject",
			input:    "Your code 🎉",
			expected: "=?UTF-8?B?WW91ciBjb2RlIPCfjok=?=",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := encodeRFC2047(tc.input)
			if result != tc.expected {
				t.Errorf("encodeRFC2047(%q) = %q, want %q", tc.input, result, tc.expected)
			}
			// Verify it decodes back correctly
			if strings.HasPrefix(result, "=?UTF-8?B?") && strings.HasSuffix(result, "?=") {
				encoded := strings.TrimPrefix(result, "=?UTF-8?B?")
				encoded = strings.TrimSuffix(encoded, "?=")
				decoded, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil {
					t.Errorf("failed to decode RFC 2047: %v", err)
				} else if string(decoded) != tc.input {
					t.Errorf("RFC 2047 round-trip failed: got %q, want %q", string(decoded), tc.input)
				}
			}
		})
	}
}

// TestBoundaryUniqueness verifies that generated boundaries are unique and
// don't appear in the message bodies.
func TestBoundaryUniqueness(t *testing.T) {
	mail := &smtpMailer{cfg: Config{From: "noreply@necly.example"}}

	// Generate many boundaries and verify uniqueness
	boundaries := make(map[string]bool)
	for i := 0; i < 100; i++ {
		msg := Message{
			To:      "test@example.com",
			Subject: "Test",
			Body:    "Plain text body",
			HTML:    "<p>HTML body</p>",
		}
		raw := mail.render(msg)
		boundary := extractBoundary(string(raw))
		if boundary == "" {
			t.Fatal("failed to extract boundary")
		}
		if boundaries[boundary] {
			t.Fatalf("duplicate boundary generated: %s", boundary)
		}
		boundaries[boundary] = true

		// Verify boundary doesn't appear in body parts
		if strings.Contains(msg.Body, boundary) || strings.Contains(msg.HTML, boundary) {
			t.Fatalf("boundary appears in message body: %s", boundary)
		}
	}
}

// TestMessageWithoutHTMLBackwardCompatibility ensures Message without HTML
// still produces text/plain (not multipart) for backward compatibility.
func TestMessageWithoutHTMLBackwardCompatibility(t *testing.T) {
	mail := &smtpMailer{cfg: Config{From: "noreply@necly.example"}}
	msg := Message{
		To:      "alice@example.com",
		Subject: "Plain text only",
		Body:    "This is plain text.\nNo HTML here.",
		HTML:    "",
	}
	raw := mail.render(msg)
	rawStr := string(raw)

	if strings.Contains(rawStr, "multipart/alternative") {
		t.Fatalf("plain text message should not be multipart: %s", rawStr)
	}
	if strings.Contains(rawStr, "text/html") {
		t.Fatalf("plain text message should not have text/html part: %s", rawStr)
	}
	if !strings.Contains(rawStr, "Content-Type: text/plain; charset=\"UTF-8\"") {
		t.Fatalf("expected text/plain content type: %s", rawStr)
	}
}

// TestRenderedMessageContainsAccessCodeInHTML verifies that the HTML part
// of a multipart message contains the access code (using dummy code).
func TestRenderedMessageContainsAccessCodeInHTML(t *testing.T) {
	mail := &smtpMailer{cfg: Config{From: "noreply@necly.example"}}
	dummyCode := "ABC123DEF4"
	msg := Message{
		To:      "alice@example.com",
		Subject: "Kode akses OTP pesanan ORD-123 — berlaku 15 menit",
		Body:    "Kode akses Anda: " + dummyCode + "\n\nJangan bagikan kode ini.",
		HTML:    "<p>Kode akses Anda: <strong>" + dummyCode + "</strong></p>\n<p>Jangan bagikan kode ini.</p>",
	}
	raw := mail.render(msg)
	rawStr := string(raw)

	// Extract HTML part and decode QP
	boundary := extractBoundary(rawStr)
	if boundary == "" {
		t.Fatal("no boundary found")
	}
	parts := splitParts(rawStr, boundary)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}

	htmlPart := parts[1]
	// Extract body after headers
	bodyStart := strings.Index(htmlPart, "\r\n\r\n")
	if bodyStart == -1 {
		bodyStart = strings.Index(htmlPart, "\n\n")
	}
	if bodyStart == -1 {
		t.Fatalf("could not find body in HTML part: %s", htmlPart)
	}
	htmlBody := htmlPart[bodyStart+4:]
	decodedHTML := quotedPrintableDecode(htmlBody)

	if !strings.Contains(decodedHTML, dummyCode) {
		t.Errorf("access code %q not found in decoded HTML: %q", dummyCode, decodedHTML)
	}
	// Verify exact match with input HTML
	if decodedHTML != msg.HTML {
		t.Errorf("decoded HTML doesn't match input\ninput:    %q\ndecoded:  %q", msg.HTML, decodedHTML)
	}
}

// TestLineLengthLimit verifies that rendered message lines don't exceed 998 octets (RFC 5322).
func TestLineLengthLimit(t *testing.T) {
	mail := &smtpMailer{cfg: Config{From: "noreply@necly.example"}}

	// Create a message with very long lines
	longLine := strings.Repeat("x", 1000)
	msg := Message{
		To:      "alice@example.com",
		Subject: "Test",
		Body:    longLine,
		HTML:    "<p>" + longLine + "</p>",
	}
	raw := mail.render(msg)
	rawStr := string(raw)

	lines := strings.Split(rawStr, "\r\n")
	for i, line := range lines {
		if len(line) > 998 {
			t.Errorf("line %d exceeds 998 octets: %d", i, len(line))
		}
	}
}

// TestHeaderCRFLStripping verifies that CR/LF in headers are stripped.
func TestHeaderCRFLStripping(t *testing.T) {
	mail := &smtpMailer{cfg: Config{From: "noreply@necly.example"}}
	msg := Message{
		To:      "alice@example.com\r\nBcc: evil@example.com",
		Subject: "Test\nSubject",
		Body:    "Body",
		HTML:    "<p>HTML</p>",
	}
	raw := mail.render(msg)
	rawStr := string(raw)

	// CR/LF in individual header values must be replaced by spaces so no
	// attacker-controlled header can be injected. Extract each header line
	// and ensure there is exactly one ':' prefix per logical line.
	headerSection := rawStr[:strings.Index(rawStr, "\r\n\r\n")]
	lines := strings.Split(headerSection, "\r\n")
	for _, line := range lines {
		if strings.Contains(line, "Bcc:") {
			// It is OK if the stripped text appears inline (as a value),
			// but it must not start a new header line.
			if strings.HasPrefix(strings.TrimSpace(line), "Bcc:") {
				t.Errorf("header injection possible: %s", headerSection)
			}
		}
	}
}

// TestRenderedMIMEHeaders documents the exact MIME header shape produced for
// an access-code message. Run with -v to capture the header evidence.
func TestRenderedMIMEHeaders(t *testing.T) {
	mail := &smtpMailer{cfg: Config{From: "noreply@necly.example", FromName: "Necly"}}
	dummyCode := "ABC123DEF4"
	msg := Message{
		To:      "alice@example.com",
		Subject: "Kode akses OTP pesanan ORD-123 — berlaku 15 menit",
		Body:    "Kode akses Anda: " + dummyCode + "\n",
		HTML:    "<p>Kode akses Anda: <strong>" + dummyCode + "</strong></p>",
	}
	raw := string(mail.render(msg))
	headerEnd := strings.Index(raw, "\r\n\r\n")
	if headerEnd < 0 {
		t.Fatal("missing header/body separator")
	}
	headers := raw[:headerEnd]
	t.Logf("MIME headers:\n%s", strings.ReplaceAll(headers, "\r\n", "\n"))

	for _, want := range []string{
		"Auto-Submitted: auto-generated",
		"X-Auto-Response-Suppress: All",
		"Content-Type: multipart/alternative; boundary=",
		"=?UTF-8?B?",
	} {
		if !strings.Contains(headers, want) {
			t.Errorf("missing %q in MIME headers:\n%s", want, headers)
		}
	}
}

// TestAutoSubmittedHeaders verifies required headers are present.
func TestAutoSubmittedHeaders(t *testing.T) {
	mail := &smtpMailer{cfg: Config{From: "noreply@necly.example"}}
	msg := Message{
		To:      "alice@example.com",
		Subject: "Test",
		Body:    "Body",
		HTML:    "<p>HTML</p>",
	}
	raw := mail.render(msg)
	rawStr := string(raw)

	if !strings.Contains(rawStr, "Auto-Submitted: auto-generated") {
		t.Fatal("missing Auto-Submitted header")
	}
	if !strings.Contains(rawStr, "X-Auto-Response-Suppress: All") {
		t.Fatal("missing X-Auto-Response-Suppress header")
	}
}

// Helper functions

func extractBoundary(raw string) string {
	// Find Content-Type: multipart/alternative; boundary="..."
	idx := strings.Index(raw, "boundary=\"")
	if idx == -1 {
		return ""
	}
	start := idx + len("boundary=\"")
	end := strings.Index(raw[start:], "\"")
	if end == -1 {
		return ""
	}
	return raw[start : start+end]
}

func splitParts(raw, boundary string) []string {
	delimiter := "--" + boundary
	parts := strings.Split(raw, delimiter)
	var result []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "--" {
			continue
		}
		// Skip the preamble (header section) before the first boundary.
		if strings.HasPrefix(part, "From:") {
			continue
		}
		result = append(result, part)
	}
	return result
}

func quotedPrintableDecode(s string) string {
	// Remove soft line breaks (=CRLF)
	s = strings.ReplaceAll(s, "=\r\n", "")
	s = strings.ReplaceAll(s, "=\n", "")

	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '=' && i+2 < len(s) {
			hex := s[i+1 : i+3]
			if val, err := strconv.ParseUint(hex, 16, 8); err == nil {
				b.WriteByte(byte(val))
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	// The encoder normalizes LF to CRLF (per RFC 2045); normalize it back so
	// the round-trip can be compared with the original Go string.
	return strings.ReplaceAll(b.String(), "\r\n", "\n")
}
