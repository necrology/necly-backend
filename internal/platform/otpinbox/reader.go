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
	// ErrMailboxHostNotAllowed means an inventory account points at a mailbox
	// destination outside the operator-approved allowlist. It fails closed
	// before any dial or credential exchange happens.
	ErrMailboxHostNotAllowed = errors.New("otp inbox host is not allowed")
)

const (
	// DefaultMailboxIMAPHost is used when an account does not store an explicit
	// mailbox host.
	DefaultMailboxIMAPHost = "imap.gmail.com"
	// mailboxIMAPPort is the only port the account reader may use. OTP mailbox
	// credentials are Gmail app passwords and IMAPS is always 993.
	mailboxIMAPPort = "993"
	// maxMailboxHostLength keeps a stored host bounded before validation.
	maxMailboxHostLength = 255
)

// mailboxIMAPHostAllowlist is the complete set of destinations an account
// mailbox may contact. A host copied from an admin record is never trusted on
// its own: allowing arbitrary hosts would turn the reader into an SSRF
// primitive that also sends the mailbox username and app password.
var mailboxIMAPHostAllowlist = map[string]struct{}{
	"imap.gmail.com":      {},
	"imap.googlemail.com": {},
}

// NormalizeMailboxIMAPHost validates and canonicalizes an account mailbox host.
// It accepts an empty value (the Gmail default), an allowlisted Gmail host, or
// that host with the explicit :993 IMAPS port. It returns the canonical bare
// hostname. Any other host, port, scheme, path or address literal is rejected.
func NormalizeMailboxIMAPHost(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultMailboxIMAPHost, nil
	}
	if len(value) > maxMailboxHostLength || strings.ContainsAny(value, "/@ \t\r\n") || strings.Contains(value, "://") {
		return "", ErrMailboxHostNotAllowed
	}

	host := value
	switch strings.Count(value, ":") {
	case 0:
	case 1:
		parsedHost, port, err := net.SplitHostPort(value)
		if err != nil || port != mailboxIMAPPort {
			return "", ErrMailboxHostNotAllowed
		}
		host = parsedHost
	default:
		// IPv6 literals, bracketed addresses and URL-like values are not part
		// of the Gmail allowlist.
		return "", ErrMailboxHostNotAllowed
	}

	host = strings.ToLower(strings.TrimSpace(host))
	if _, ok := mailboxIMAPHostAllowlist[host]; !ok {
		return "", ErrMailboxHostNotAllowed
	}
	return host, nil
}

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
// password; the factory never persists either value. The host is validated
// again here against the allowlist, so a stale or tampered database row can
// never reach the dialer unvalidated.
type AccountReaderFactory struct {
	Mailbox string
	Timeout time.Duration
}

func (f AccountReaderFactory) NewReader(accountEmail, appPassword, imapHost string) Reader {
	host, err := NormalizeMailboxIMAPHost(imapHost)
	if err != nil {
		// Fail closed without dialing or touching the credential. The caller may
		// still fall back to the operator-configured central mailbox.
		return disabledReader{}
	}
	addr := net.JoinHostPort(host, mailboxIMAPPort)
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
	// tlsConfig is only set by tests that need a private test CA. Production
	// readers always verify the real Gmail certificate chain.
	tlsConfig *tls.Config
	// logoutTimeout is overridable by tests; production uses logoutTimeout.
	logoutTimeout time.Duration
}

func (r *imapReader) Enabled() bool { return true }

func (r *imapReader) clientTLSConfig(addr string) *tls.Config {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName(addr)}
	if r.tlsConfig != nil {
		tlsConfig = r.tlsConfig.Clone()
		if tlsConfig.MinVersion < tls.VersionTLS12 {
			tlsConfig.MinVersion = tls.VersionTLS12
		}
		if tlsConfig.ServerName == "" {
			tlsConfig.ServerName = serverName(addr)
		}
	}
	return tlsConfig
}

const (
	// maxFetchedMessages bounds the work and memory for one lookup. An OTP
	// normally arrives within seconds; a larger window only costs latency.
	maxFetchedMessages = 25
	maxRawMessageBytes = 2 << 20
	// logoutTimeout bounds the best-effort LOGOUT so a server that stops
	// responding cannot extend the session past the caller's deadline.
	logoutTimeout = 2 * time.Second
)

func (r *imapReader) Latest(ctx context.Context, recipient string, since time.Time) (*Message, error) {
	recipient = normalizeAddress(recipient)
	if recipient == "" {
		return nil, ErrNotFound
	}
	// fetchLatest is intentionally synchronous. The context watcher below
	// closes the socket on cancellation, so every blocking IMAP operation
	// returns instead of leaving a goroutine or connection behind.
	return r.fetchLatest(ctx, recipient, since)
}

func (r *imapReader) fetchLatest(ctx context.Context, recipient string, since time.Time) (*Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	timeout := r.cfg.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	sessionCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return nil, context.DeadlineExceeded
	}

	dialer := &net.Dialer{Timeout: remaining}
	rawConn, err := dialer.DialContext(sessionCtx, "tcp", r.cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("otp inbox dial: %w", sessionError(sessionCtx, err))
	}
	// The absolute deadline covers the greeting, TLS handshake and every IMAP
	// command. deadlineConn below prevents the client from clearing it when it
	// manages its own per-command deadline.
	if err := rawConn.SetDeadline(deadline); err != nil {
		_ = rawConn.Close()
		return nil, fmt.Errorf("otp inbox deadline: %w", err)
	}
	tlsConn := tls.Client(rawConn, r.clientTLSConfig(r.cfg.Addr))
	bounded := &deadlineConn{Conn: tlsConn, deadline: deadline}
	// Register the watchdog before the greeting/TLS handshake: client.New can
	// block there, so the connection must be closable even before a client
	// object exists.
	stopClose := context.AfterFunc(sessionCtx, func() { _ = rawConn.Close() })
	defer stopClose()
	c, err := client.New(bounded)
	if err != nil {
		_ = bounded.Close()
		return nil, fmt.Errorf("otp inbox handshake: %w", sessionError(sessionCtx, err))
	}

	defer func() {
		// LOGOUT is bounded too: on cancellation the socket is already closed,
		// otherwise it gets a short budget that never exceeds the session deadline.
		logoutLimit := r.logoutTimeout
		if logoutLimit <= 0 {
			logoutLimit = logoutTimeout
		}
		logoutBudget := time.Until(deadline)
		if logoutBudget <= 0 || logoutBudget > logoutLimit {
			logoutBudget = logoutLimit
		}
		c.Timeout = logoutBudget
		_ = c.Logout()
		_ = c.Terminate()
	}()

	c.Timeout = time.Until(deadline)
	if c.Timeout <= 0 {
		return nil, context.DeadlineExceeded
	}
	if err := c.Login(r.cfg.User, r.cfg.Password); err != nil {
		return nil, fmt.Errorf("otp inbox login: %w", sessionError(sessionCtx, err))
	}
	if _, err := c.Select(r.cfg.Mailbox, true); err != nil {
		return nil, fmt.Errorf("otp inbox select: %w", sessionError(sessionCtx, err))
	}

	criteria := imap.NewSearchCriteria()
	criteria.Since = since.Add(-2 * time.Minute)
	uids, err := c.UidSearch(criteria)
	if err != nil {
		return nil, fmt.Errorf("otp inbox search: %w", sessionError(sessionCtx, err))
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
	fetchDone := make(chan error, 1)
	go func() {
		fetchDone <- c.UidFetch(seqset, items, messages)
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
	if err := <-fetchDone; err != nil {
		return nil, fmt.Errorf("otp inbox fetch: %w", sessionError(sessionCtx, err))
	}
	if best == nil {
		return nil, ErrNotFound
	}
	if err := sessionCtx.Err(); err != nil {
		return nil, err
	}
	return best, nil
}

// deadlineConn enforces one absolute session deadline even though the IMAP
// client sets a fresh per-command deadline (including the zero value, which
// would otherwise clear the deadline) before every command.
type deadlineConn struct {
	net.Conn
	deadline time.Time
}

func (c *deadlineConn) capDeadline(value time.Time) time.Time {
	if value.IsZero() || value.After(c.deadline) {
		return c.deadline
	}
	return value
}

func (c *deadlineConn) SetDeadline(value time.Time) error {
	return c.Conn.SetDeadline(c.capDeadline(value))
}

func (c *deadlineConn) SetReadDeadline(value time.Time) error {
	return c.Conn.SetReadDeadline(c.capDeadline(value))
}

func (c *deadlineConn) SetWriteDeadline(value time.Time) error {
	return c.Conn.SetWriteDeadline(c.capDeadline(value))
}

// sessionError preserves the caller-visible cancellation reason instead of
// surfacing a transport error caused by the socket watchdog. When the socket
// deadline fires before the context timer is observed, net.Error.Timeout() is
// already true while ctx.Err() is still nil; normalize that race to the
// caller's deadline so callers and tests see one stable timeout reason.
func sessionError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		if _, ok := ctx.Deadline(); ok {
			return context.DeadlineExceeded
		}
	}
	return err
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

	// passwordChangeMail matches mail that asks the reader to reset or change a
	// password. Those messages often carry a numeric code as well, but the code
	// unlocks a password change instead of a login, so it must never be returned
	// as the account's login OTP. The patterns are deliberately phrase-based so
	// ordinary login mail that merely warns "never share your password" or
	// mentions a password policy is still accepted.
	passwordChangeMail = regexp.MustCompile(`(?i)(?:` +
		`\b(?:reset|change[ds]?|update[ds]?|set(?:\s+up)?|create|choose)\s+(?:(?:a|your|the|my|account)\s+)?(?:new\s+)?password\b` +
		`|\bpassword\s+(?:reset|change[ds]?|update[ds]?|recovery|assistance)\b` +
		`|\bpassword\s+(?:was|were|is|has\s+been|have\s+been)\s+(?:successfully\s+)?(?:changed|reset|updated)\b` +
		`|\b(?:forgot|forgotten|recover)\s+(?:(?:your|the)\s+)?password\b` +
		`|\bnew\s+password\b` +
		`|\b(?:atur\s+ulang|reset|ubah|mengubah|ganti|mengganti|perbarui|memperbarui)\s+(?:kata\s+sandi|sandi|password)\b` +
		`|\b(?:kata\s+sandi|sandi|password)\s+(?:anda\s+)?(?:yang\s+)?baru\b` +
		`|\b(?:kata\s+sandi|sandi|password)\b.{0,40}?\bdi(?:ubah|ganti|perbarui|reset)\b` +
		`|\b(?:lupa|pemulihan|memulihkan|pulihkan)\s+(?:kata\s+sandi|sandi|password)\b)`)
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
	// Skip password reset/change mail entirely. Its code is a password-change
	// credential, not a login OTP, so an older valid login code may still win
	// the lookup in fetchLatest.
	if passwordChangeMail.MatchString(strings.Join(strings.Fields(subject+" "+body), " ")) {
		return nil, false
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
	recipient = normalizeAddress(recipient)
	if recipient == "" {
		return false
	}

	// To/Cc may legitimately contain an address list. Parse every value and
	// compare the mailbox address exactly; never inspect a display name, a
	// comment or the raw header text.
	for _, key := range []string{"To", "Cc"} {
		for _, value := range header[key] {
			addresses, err := mail.ParseAddressList(value)
			if err != nil {
				// Malformed values are ignored, not matched by substring.
				continue
			}
			for _, address := range addresses {
				if normalizeAddress(address.Address) == recipient {
					return true
				}
			}
		}
	}

	// Envelope headers carry exactly one original recipient. Malformed or
	// multi-address values fail closed so a crafted forwarding header cannot
	// smuggle in a different mailbox.
	for _, key := range []string{"Delivered-To", "X-Original-To", "Envelope-To", "X-Forwarded-To"} {
		for _, value := range header[key] {
			addresses, err := mail.ParseAddressList(value)
			if err != nil || len(addresses) != 1 {
				continue
			}
			if normalizeAddress(addresses[0].Address) == recipient {
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
