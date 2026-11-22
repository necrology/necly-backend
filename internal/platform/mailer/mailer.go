// Package mailer sends transactional email (password reset links, report
// notifications, OTP access codes, delivery credentials).
//
// When no SMTP host is configured the package falls back to a log-only sender
// so a self-hosted deployment without mail credentials keeps working: nothing
// is silently dropped, every message is written to the server log instead.
//
// The mailer supports multipart/alternative (text/plain + text/html) for rich
// transactional emails. The Message struct carries both Body (plain text) and
// HTML (HTML). When HTML is non-empty, a multipart/alternative message is
// constructed with text/plain first, then text/html.
package mailer

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Message is a transactional email with optional HTML alternative.
// Both Body (text/plain) and HTML (text/html) should be provided for
// best compatibility. If HTML is empty, only text/plain is sent.
type Message struct {
	To      string
	Subject string
	Body    string // text/plain version (required)
	HTML    string // text/html version (optional)
}

// Mailer delivers transactional email.
type Mailer interface {
	// Send delivers msg. Implementations must never include credentials in the
	// returned error.
	Send(ctx context.Context, msg Message) error
	// Enabled reports whether mail actually leaves the process.
	Enabled() bool
}

// Config mirrors the SMTP_* environment variables.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
	FromName string
	UseTLS   bool
	// Production disables the log-only fallback. In production a missing or
	// unsafe transport must report Enabled() == false without ever writing the
	// message body (which can contain activation/reset tokens) to the log.
	Production bool
}

// New returns an SMTP mailer when a host is configured with TLS, and a log-only
// mailer with a loud startup warning otherwise. In production it never returns
// the log-only sender. The returned Mailer is always usable.
func New(cfg Config) Mailer {
	if strings.TrimSpace(cfg.Host) == "" {
		if cfg.Production {
			log.Printf("ERROR mailer: SMTP_HOST is empty; transactional email is DISABLED and " +
				"token-bearing authentication flows will fail closed. Set SMTP_HOST, SMTP_PORT, " +
				"SMTP_USER, SMTP_PASSWORD, EMAIL_FROM and SMTP_TLS=true to enable delivery.")
			return &disabledMailer{}
		}
		log.Printf("WARN mailer: SMTP_HOST is empty, so transactional email is DISABLED. "+
			"Password reset links and issue-report notifications will only be written to this log. "+
			"Set SMTP_HOST, SMTP_USER, SMTP_PASSWORD and EMAIL_FROM to enable delivery (from=%q).", cfg.From)
		return &logMailer{}
	}
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	if !cfg.UseTLS {
		// SMTP without TLS would put AUTH credentials and token-bearing mail on
		// the wire in clear text. Refuse to construct a sender that can do that.
		log.Printf("ERROR mailer: SMTP_HOST is configured but SMTP_TLS is false; mail delivery is DISABLED")
		return &disabledMailer{}
	}
	return &smtpMailer{cfg: cfg}
}

// disabledMailer keeps the process running while guaranteeing that no message
// is delivered or logged. Enabled() is false so token-bearing flows fail closed.
type disabledMailer struct{}

func (m *disabledMailer) Enabled() bool { return false }

func (m *disabledMailer) Send(context.Context, Message) error {
	return errors.New("mail delivery is disabled")
}

// logMailer keeps the surrounding flows working when SMTP is unavailable. It
// reports Enabled() == false so callers can tell delivery apart from a no-op.
type logMailer struct{}

func (m *logMailer) Enabled() bool { return false }

func (m *logMailer) Send(_ context.Context, msg Message) error {
	log.Printf("mailer: email suppressed (no SMTP configured) to=%q subject=%q\n--- TEXT ---\n%s\n--- HTML ---\n%s", msg.To, msg.Subject, msg.Body, msg.HTML)
	return nil
}

type smtpMailer struct {
	cfg Config
}

func (m *smtpMailer) Enabled() bool { return true }

func (m *smtpMailer) Send(ctx context.Context, msg Message) error {
	addr := net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port))
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}

	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer func() { _ = client.Close() }()

	// TLS is mandatory whenever SMTP is configured. Never fall back to a
	// cleartext session when the server does not advertise STARTTLS: doing so
	// would expose credentials and token-bearing mail.
	ok, _ := client.Extension("STARTTLS")
	if !ok {
		return errors.New("smtp server does not offer STARTTLS")
	}
	if err := client.StartTLS(&tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
		return fmt.Errorf("smtp starttls: %w", err)
	}

	if m.cfg.User != "" {
		if ok, _ := client.Extension("AUTH"); ok {
			if err := client.Auth(smtp.PlainAuth("", m.cfg.User, m.cfg.Password, m.cfg.Host)); err != nil {
				return fmt.Errorf("smtp auth: %w", err)
			}
		}
	}

	if err := client.Mail(m.cfg.From); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := writer.Write(m.render(msg)); err != nil {
		return fmt.Errorf("smtp write body: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp close body: %w", err)
	}
	return client.Quit()
}

// render builds the raw RFC 5322 message. Header values are stripped of CR/LF
// so a future caller cannot smuggle extra headers through a subject or address.
// If HTML is non-empty, constructs multipart/alternative with text/plain
// first, then text/html (per RFC 2046). Otherwise sends text/plain only.
func (m *smtpMailer) render(msg Message) []byte {
	from := m.cfg.From
	if m.cfg.FromName != "" {
		from = fmt.Sprintf("%s <%s>", m.cfg.FromName, m.cfg.From)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", headerValue(from))
	fmt.Fprintf(&b, "To: %s\r\n", headerValue(msg.To))
	// RFC 2047 encoding for subject if non-ASCII
	fmt.Fprintf(&b, "Subject: %s\r\n", encodeRFC2047(headerValue(msg.Subject)))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	// Transactional mail from this store never accepts replies. Mark it as
	// auto-generated and suppress common out-of-office/auto-reply handlers.
	b.WriteString("Auto-Submitted: auto-generated\r\n")
	b.WriteString("X-Auto-Response-Suppress: All\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")

	if msg.HTML != "" {
		// multipart/alternative
		boundary := generateBoundary(msg.Body, msg.HTML)
		fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n", boundary)
		b.WriteString("\r\n")

		// Part 1: text/plain
		fmt.Fprintf(&b, "--%s\r\n", boundary)
		b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
		b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
		b.WriteString("\r\n")
		plainPart := quotedPrintableEncode(msg.Body)
		b.WriteString(plainPart)
		if !strings.HasSuffix(plainPart, "\r\n") {
			b.WriteString("\r\n")
		}

		// Part 2: text/html
		fmt.Fprintf(&b, "--%s\r\n", boundary)
		b.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
		b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
		b.WriteString("\r\n")
		htmlPart := quotedPrintableEncode(msg.HTML)
		b.WriteString(htmlPart)
		if !strings.HasSuffix(htmlPart, "\r\n") {
			b.WriteString("\r\n")
		}

		// End boundary
		fmt.Fprintf(&b, "--%s--\r\n", boundary)
	} else {
		// text/plain only
		b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
		b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
		b.WriteString("\r\n")
		b.WriteString(quotedPrintableEncode(msg.Body))
	}

	return []byte(b.String())
}

// generateBoundary generates a random MIME boundary string that does not
// appear in either the plain text or HTML body.
func generateBoundary(plainText, htmlText string) string {
	const prefix = "=_necly_"
	for {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			// Fallback to timestamp-based if rand fails
			return fmt.Sprintf("%s%x", prefix, time.Now().UnixNano())
		}
		boundary := prefix + fmt.Sprintf("%x", buf)
		// Ensure boundary doesn't appear in either body
		if !strings.Contains(plainText, boundary) && !strings.Contains(htmlText, boundary) {
			return boundary
		}
		// Extremely unlikely collision; retry
	}
}

// quotedPrintableEncode encodes a string using quoted-printable encoding (RFC 2045).
// Lines are limited to 76 octets, with soft line breaks represented as "=\r\n".
// Real line breaks in the input are preserved as literal CRLF (normalized from
// LF where needed). A trailing space/tab before either a real or a soft line
// break is encoded as =20/=09 so it cannot be stripped by a mail client.
func quotedPrintableEncode(s string) string {
	var b strings.Builder
	lineLen := 0
	softBreak := func() {
		b.WriteString("=\r\n")
		lineLen = 0
	}
	for i := 0; i < len(s); i++ {
		c := s[i]

		// Preserve real line breaks as literal CRLF, never as =0D=0A.
		if c == '\r' && i+1 < len(s) && s[i+1] == '\n' {
			b.WriteString("\r\n")
			lineLen = 0
			i++
			continue
		}
		if c == '\n' {
			b.WriteString("\r\n")
			lineLen = 0
			continue
		}

		// '=' and any non-printable or non-ASCII byte are encoded as =XX.
		if c == '=' || c < 32 || c > 126 {
			if lineLen > 72 { // leave room for =XX plus the soft-break '='
				softBreak()
			}
			fmt.Fprintf(&b, "=%02X", c)
			lineLen += 3
			continue
		}

		// A space or tab directly before a line break (real or soft) would be
		// stripped by some clients, so encode it explicitly.
		if c == ' ' || c == '\t' {
			nextIsBreak := i+1 >= len(s) || s[i+1] == '\r' || s[i+1] == '\n'
			if nextIsBreak || lineLen >= 75 {
				if lineLen > 72 {
					softBreak()
				}
				fmt.Fprintf(&b, "=%02X", c)
				lineLen += 3
				continue
			}
		}

		if lineLen >= 75 { // leave the 76th octet for the soft-break '='
			softBreak()
		}
		b.WriteByte(c)
		lineLen++
	}
	return b.String()
}

// encodeRFC2047 encodes a header value using RFC 2047 (encoded-word) if it contains non-ASCII.
// Uses =?UTF-8?B?...?= format (base64 encoding).
func encodeRFC2047(s string) string {
	// Check if string contains non-ASCII
	needsEncoding := false
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			needsEncoding = true
			break
		}
	}
	if !needsEncoding {
		return s
	}
	// Use UTF-8 base64 encoding: =?UTF-8?B?...?=
	encoded := base64.StdEncoding.EncodeToString([]byte(s))
	return "=?UTF-8?B?" + encoded + "?="
}

func headerValue(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}
