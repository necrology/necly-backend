package otpinbox

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

type testIMAPMessage struct {
	uid          uint32
	internalDate time.Time
	raw          string
}

type mailboxIMAPServer struct {
	listener net.Listener
	roots    *tls.Config
	messages []testIMAPMessage
}

func newMailboxIMAPServer(t *testing.T, messages []testIMAPMessage) *mailboxIMAPServer {
	t.Helper()
	certificate, roots := testCertificate(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &mailboxIMAPServer{
		listener: listener,
		roots:    &tls.Config{RootCAs: roots},
		messages: messages,
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.serve(tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{certificate}}))
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return server
}

func (s *mailboxIMAPServer) serve(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	_, _ = writer.WriteString("* OK [CAPABILITY IMAP4rev1] test ready\r\n")
	_ = writer.Flush()

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		tag := fields[0]
		upper := strings.ToUpper(line)
		switch {
		case strings.Contains(upper, " LOGIN "):
			_, _ = writer.WriteString(tag + " OK LOGIN completed\r\n")
		case strings.Contains(upper, " EXAMINE "):
			_, _ = fmt.Fprintf(writer, "* %d EXISTS\r\n* 0 RECENT\r\n%s OK [READ-ONLY] completed\r\n", len(s.messages), tag)
		case strings.Contains(upper, " UID SEARCH "):
			_, _ = writer.WriteString("* SEARCH")
			for _, message := range s.messages {
				_, _ = fmt.Fprintf(writer, " %d", message.uid)
			}
			_, _ = writer.WriteString("\r\n" + tag + " OK SEARCH completed\r\n")
		case strings.Contains(upper, " UID FETCH "):
			selector := fields[3]
			for sequence, message := range s.messages {
				if !testUIDSelected(selector, message.uid) {
					continue
				}
				_, _ = fmt.Fprintf(writer, "* %d FETCH (UID %d INTERNALDATE %q BODY[] {%d}\r\n%s)\r\n",
					sequence+1, message.uid, message.internalDate.Format("02-Jan-2006 15:04:05 -0700"), len(message.raw), message.raw)
			}
			_, _ = writer.WriteString(tag + " OK FETCH completed\r\n")
		case strings.Contains(upper, " LOGOUT"):
			_, _ = writer.WriteString("* BYE logging out\r\n" + tag + " OK LOGOUT completed\r\n")
			_ = writer.Flush()
			return
		default:
			_, _ = writer.WriteString(tag + " BAD unsupported\r\n")
		}
		if err := writer.Flush(); err != nil {
			return
		}
	}
}

func testUIDSelected(selector string, uid uint32) bool {
	var first, last uint32
	if _, err := fmt.Sscanf(selector, "%d:%d", &first, &last); err == nil {
		return uid >= first && uid <= last
	}
	for _, value := range strings.Split(selector, ",") {
		var selected uint32
		if _, err := fmt.Sscanf(value, "%d", &selected); err == nil && uid == selected {
			return true
		}
	}
	return false
}

func TestLatestFindsNewestExactRecipientBeyondTwentyFiveMessages(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	messages := []testIMAPMessage{
		{
			uid:          1,
			internalDate: now.Add(-4 * time.Minute),
			raw: strings.Join([]string{
				"To: sold-account@example.test",
				"Subject: Login code",
				"Content-Type: text/plain",
				"",
				"Your login code is 111111.",
			}, "\r\n"),
		},
		{
			uid:          2,
			internalDate: now.Add(-2 * time.Minute),
			raw: strings.Join([]string{
				"To: =?UTF-8?Q?Akun_Terjual?= <SOLD-ACCOUNT@example.test>",
				"Subject: =?UTF-8?Q?Kode_masuk_terbaru?=",
				"Date: invalid-sender-date",
				"Content-Type: multipart/alternative; boundary=otp-boundary",
				"",
				"--otp-boundary",
				"Content-Type: text/plain; charset=UTF-8",
				"Content-Transfer-Encoding: base64",
				"",
				"S29kZSBsb2dpbiBBbmRhIGFkYWxhaCAyMjIyMjIu",
				"--otp-boundary--",
			}, "\r\n"),
		},
		{
			uid:          3,
			internalDate: now.Add(-time.Minute),
			raw: strings.Join([]string{
				"To: sold-account@example.test",
				"Subject: Reset your password",
				"Content-Type: text/plain",
				"",
				"Use code 333333 to reset your password.",
			}, "\r\n"),
		},
	}
	for uid := uint32(4); uid <= 31; uid++ {
		messages = append(messages, testIMAPMessage{
			uid:          uid,
			internalDate: now.Add(-time.Duration(uid) * time.Second),
			raw: strings.Join([]string{
				"To: another-account@example.test",
				"Subject: Login code",
				"Content-Type: text/plain",
				"",
				fmt.Sprintf("Your login code is %06d.", uid),
			}, "\r\n"),
		})
	}

	server := newMailboxIMAPServer(t, messages)
	reader := &imapReader{
		cfg: Config{
			Addr: server.listener.Addr().String(), User: "mailbox-user",
			Password: testMailboxSecret, Mailbox: "INBOX", UseTLS: true, Timeout: 5 * time.Second,
		},
		tlsConfig: server.roots,
	}

	message, err := reader.Latest(context.Background(), "sold-account@example.test", now.Add(-30*time.Minute))
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if message.Code != "222222" {
		t.Fatalf("code = %q, want newest login OTP 222222", message.Code)
	}
	if !message.ReceivedAt.Equal(now.Add(-2 * time.Minute)) {
		t.Fatalf("received_at = %s, want INTERNALDATE %s", message.ReceivedAt, now.Add(-2*time.Minute))
	}
	if message.Subject != "Kode masuk terbaru" {
		t.Fatalf("subject = %q", message.Subject)
	}
}
