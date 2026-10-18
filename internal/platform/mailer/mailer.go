// Package mailer sends transactional email (password reset links, report
// notifications).
//
// When no SMTP host is configured the package falls back to a log-only sender
// so a self-hosted deployment without mail credentials keeps working: nothing
// is silently dropped, every message is written to the server log instead.
package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Message is a single plain-text transactional email.
type Message struct {
	To      string
	Subject string
	Body    string
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
	log.Printf("mailer: email suppressed (no SMTP configured) to=%q subject=%q\n%s", msg.To, msg.Subject, msg.Body)
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
func (m *smtpMailer) render(msg Message) []byte {
	from := m.cfg.From
	if m.cfg.FromName != "" {
		from = fmt.Sprintf("%s <%s>", m.cfg.FromName, m.cfg.From)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", headerValue(from))
	fmt.Fprintf(&b, "To: %s\r\n", headerValue(msg.To))
	fmt.Fprintf(&b, "Subject: %s\r\n", headerValue(msg.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(msg.Body, "\n", "\r\n"))
	return []byte(b.String())
}

func headerValue(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}
