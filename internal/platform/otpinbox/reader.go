// Package otpinbox reads one-time-password (OTP) messages from a shared IMAP
// mailbox. Purchased subscription accounts receive their login OTP by email;
// the customer never gets the mailbox password. The mailbox is therefore an
// infrastructure secret and is configured through the OTP_IMAP_* environment
// variables, not stored on a customer record.
//
// The reader only returns a code that was addressed to the exact account email
// the caller asked for. A forwarded copy must keep the original recipient in
// Delivered-To / X-Original-To / To; otherwise the message is ignored, so one
// customer can never read another customer's OTP.
package otpinbox

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

var (
	// ErrNotConfigured means no OTP mailbox is configured. Callers surface this
	// as an explicit "layanan OTP belum tersedia" state instead of a fake code.
	ErrNotConfigured = errors.New("otp inbox is not configured")
	// ErrNotFound means the mailbox has no recent OTP for that recipient.
	ErrNotFound = errors.New("no recent otp message")
)

// Message is the minimal, non-sensitive result of a mailbox lookup. The raw
// email body, sender address and mailbox credentials never leave this package.
type Message struct {
	Code       string
	ReceivedAt time.Time
	Subject    string
}

// Reader finds the newest OTP addressed to one account email.
type Reader interface {
	Enabled() bool
	Latest(ctx context.Context, recipient string, since time.Time) (*Message, error)
}

// Config mirrors the OTP_IMAP_* environment variables.
type Config struct {
	Addr     string
	User     string
	Password string
	Mailbox  string
	UseTLS   bool
	Timeout  time.Duration
}

// New returns a reader. An empty address returns a disabled reader that fails
// closed; the rest of the application keeps working without OTP retrieval.
func New(cfg Config) Reader {
	if strings.TrimSpace(cfg.Addr) == "" || strings.TrimSpace(cfg.User) == "" || cfg.Password == "" {
		return disabledReader{}
	}
	if cfg.Mailbox == "" {
		cfg.Mailbox = "INBOX"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 20 * time.Second
	}
	return &imapReader{cfg: cfg}
}

// AccountReaderFactory builds a TLS reader for a single inventory account's
// own mailbox. The caller supplies the login email and the decrypted Gmail app
// password; the factory never persists either value. Host may be a hostname or
// hostname:port and defaults to Gmail's IMAPS port when no port is given.
type AccountReaderFactory struct {
	Mailbox string
	Timeout time.Duration
}

func (f AccountReaderFactory) NewReader(accountEmail, appPassword, imapHost string) Reader {
	host := strings.TrimSpace(imapHost)
	if host == "" {
		host = "imap.gmail.com"
	}
	addr := host
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(host, "993")
	}
	mailbox := f.Mailbox
	if mailbox == "" {
		mailbox = "INBOX"
	}
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return New(Config{
		Addr: addr, User: strings.TrimSpace(accountEmail), Password: appPassword,
		Mailbox: mailbox, UseTLS: true, Timeout: timeout,
	})
}

type disabledReader struct{}

func (disabledReader) Enabled() bool { return false }

func (disabledReader) Latest(context.Context, string, time.Time) (*Message, error) {
	return nil, ErrNotConfigured
}

type imapReader struct {
	cfg Config
}

func (r *imapReader) Enabled() bool { return true }

const (
	// maxFetchedMessages bounds the work and memory for one lookup. An OTP
	// normally arrives within seconds; a larger window only costs latency.
	maxFetchedMessages = 25
	maxRawMessageBytes = 2 << 20
)

func (r *imapReader) Latest(ctx context.Context, recipient string, since time.Time) (*Message, error) {
	recipient = normalizeAddress(recipient)
	if recipient == "" {
		return nil, ErrNotFound
	}

	type result struct {
		msg *Message
		err error
	}
	done := make(chan result, 1)
	go func() {
		msg, err := r.fetchLatest(recipient, since)
		done <- result{msg: msg, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case out := <-done:
		if out.err != nil {
			return nil, out.err
		}
		return out.msg, nil
	}
}

func (r *imapReader) fetchLatest(recipient string, since time.Time) (*Message, error) {
	dialer := &net.Dialer{Timeout: r.cfg.Timeout}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName(r.cfg.Addr)}
	c, err := client.DialWithDialerTLS(dialer, r.cfg.Addr, tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("otp inbox dial: %w", err)
	}
	defer func() { _ = c.Logout() }()

	if err := c.Login(r.cfg.User, r.cfg.Password); err != nil {
		return nil, fmt.Errorf("otp inbox login: %w", err)
	}
	if _, err := c.Select(r.cfg.Mailbox, true); err != nil {
		return nil, fmt.Errorf("otp inbox select: %w", err)
	}

	criteria := imap.NewSearchCriteria()
	criteria.Since = since.Add(-2 * time.Minute)
	uids, err := c.UidSearch(criteria)
	if err != nil {
		return nil, fmt.Errorf("otp inbox search: %w", err)
	}
	if len(uids) == 0 {
		return nil, ErrNotFound
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	start := 0
	if len(uids) > maxFetchedMessages {
		start = len(uids) - maxFetchedMessages
	}
	seqset := new(imap.SeqSet)
	seqset.AddNum(uids[start:]...)

	section := &imap.BodySectionName{Peek: true}
	items := []imap.FetchItem{imap.FetchUid, imap.FetchInternalDate, section.FetchItem()}
	messages := make(chan *imap.Message, 16)
	done := make(chan error, 1)
	go func() {
		done <- c.UidFetch(seqset, items, messages)
	}()

	var best *Message
	for msg := range messages {
		raw := msg.GetBody(section)
		if raw == nil {
			continue
		}
		content, err := io.ReadAll(io.LimitReader(raw, maxRawMessageBytes))
		if err != nil {
			continue
		}
		candidate, ok := parseMessage(content, recipient, since)
		if !ok {
			continue
		}
		if candidate.ReceivedAt.IsZero() {
			candidate.ReceivedAt = msg.InternalDate
		}
		if best == nil || candidate.ReceivedAt.After(best.ReceivedAt) {
			best = candidate
		}
	}
	if err := <-done; err != nil {
		return nil, fmt.Errorf("otp inbox fetch: %w", err)
	}
	if best == nil {
		return nil, ErrNotFound
	}
	return best, nil
}

func serverName(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

var (
	digitRun  = regexp.MustCompile(`\b\d{4,8}\b`)
	otpWords  = regexp.MustCompile(`(?i)\b(otp|kode|code|verification|verifikasi|passcode|one[- ]time|login|masuk)\b`)
	htmlTag   = regexp.MustCompile(`(?s)<[^>]*>`)
	yearValue = regexp.MustCompile(`^20\d{2}$`)
)

// parseMessage is exported to tests through parseMessageForTest. It returns ok
// only when the message was addressed to recipient and contains a plausible
// OTP. The recipient check is the isolation boundary between customers.
func parseMessage(raw []byte, recipient string, since time.Time) (*Message, bool) {
	msg, err := mail.ReadMessage(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		return nil, false
	}
	if !addressedTo(msg.Header, recipient) {
		return nil, false
	}

	receivedAt := parseDate(msg.Header.Get("Date"))
	if !receivedAt.IsZero() && receivedAt.Before(since.Add(-2*time.Minute)) {
		return nil, false
	}
	subject := decodeHeader(msg.Header.Get("Subject"))
	body := extractText(msg.Header.Get("Content-Type"), msg.Body)
	if len(body) > 200_000 {
		body = body[:200_000]
	}

	code, ok := extractCode(subject, body)
	if !ok {
		return nil, false
	}
	return &Message{Code: code, ReceivedAt: receivedAt, Subject: subject}, true
}

func normalizeAddress(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func addressedTo(header mail.Header, recipient string) bool {
	keys := []string{"Delivered-To", "X-Original-To", "Envelope-To", "X-Forwarded-To", "To", "Cc"}
	for _, key := range keys {
		for _, value := range header[key] {
			if strings.Contains(strings.ToLower(value), recipient) {
				return true
			}
		}
	}
	return false
}

func decodeHeader(value string) string {
	decoder := new(mime.WordDecoder)
	decoded, err := decoder.DecodeHeader(value)
	if err != nil {
		return value
	}
	return decoded
}

func parseDate(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := mail.ParseDate(value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// extractText walks a MIME tree and returns the visible text. It deliberately
// ignores attachments and stops at the first text/plain part, falling back to
// HTML when no plain part exists.
func extractText(contentType string, body io.Reader) string {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = "text/plain"
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return ""
		}
		reader := multipart.NewReader(body, boundary)
		var fallback string
		for {
			part, err := reader.NextPart()
			if err != nil {
				break
			}
			text := extractText(part.Header.Get("Content-Type"), part)
			if strings.HasPrefix(strings.ToLower(part.Header.Get("Content-Type")), "text/plain") && text != "" {
				return text
			}
			if text != "" && fallback == "" {
				fallback = text
			}
		}
		return fallback
	}

	decoded, err := decodeTransferEncoding(params["content-transfer-encoding"], body)
	if err != nil {
		return ""
	}
	text, err := io.ReadAll(io.LimitReader(decoded, maxRawMessageBytes))
	if err != nil {
		return ""
	}
	value := string(text)
	if strings.HasPrefix(mediaType, "text/html") {
		value = htmlTag.ReplaceAllString(value, " ")
	}
	return strings.Join(strings.Fields(value), " ")
}

func decodeTransferEncoding(encoding string, body io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, body), nil
	case "quoted-printable":
		return quotedprintable.NewReader(body), nil
	default:
		return body, nil
	}
}

// extractCode returns the most plausible OTP in a message. Real provider mail
// usually puts the code near words such as "code", "kode" or "verification".
// Requiring that context (or an exact 6-digit token) keeps order numbers,
// prices and years from being returned as a login code.
func extractCode(subject, body string) (string, bool) {
	subjectHasKeyword := otpWords.MatchString(subject)
	combined := subject + " " + body
	matches := digitRun.FindAllStringIndex(combined, -1)
	if len(matches) == 0 {
		return "", false
	}

	type candidate struct {
		code  string
		score int
	}
	best := candidate{}
	for _, match := range matches {
		value := combined[match[0]:match[1]]
		start := match[0] - 80
		if start < 0 {
			start = 0
		}
		end := match[1] + 80
		if end > len(combined) {
			end = len(combined)
		}
		nearbyKeyword := otpWords.MatchString(combined[start:end])

		score := 0
		if subjectHasKeyword {
			score += 3
		}
		if nearbyKeyword {
			score += 2
		}
		switch len(value) {
		case 6:
			score += 2
		case 7, 8:
			score++
		}
		if yearValue.MatchString(value) && !nearbyKeyword && !subjectHasKeyword {
			continue
		}
		if score > best.score {
			best = candidate{code: value, score: score}
		}
	}
	// A code without any OTP context is too likely to be a reference number.
	if best.code == "" || best.score < 2 {
		return "", false
	}
	return best.code, true
}
