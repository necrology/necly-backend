package otpinbox

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const testMailboxSecret = "app-password-should-never-leak"

func testCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots.AddCert(certificate)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, roots
}

type silentIMAPServer struct {
	listener net.Listener
	roots    *x509.CertPool
	closed   chan struct{}
	once     sync.Once
	mu       sync.Mutex
	conns    []net.Conn
}

func newSilentIMAPServer(t *testing.T) *silentIMAPServer {
	t.Helper()
	certificate, roots := testCertificate(t)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{certificate}})
	if err != nil {
		t.Fatal(err)
	}
	server := &silentIMAPServer{listener: listener, roots: roots, closed: make(chan struct{}, 8)}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			server.mu.Lock()
			server.conns = append(server.conns, conn)
			server.mu.Unlock()
			go func(conn net.Conn) {
				defer conn.Close()
				defer server.once.Do(func() { server.closed <- struct{}{} })
				_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
				buffer := make([]byte, 1024)
				for {
					if _, err := conn.Read(buffer); err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		server.mu.Lock()
		for _, conn := range server.conns {
			_ = conn.Close()
		}
		server.mu.Unlock()
	})
	return server
}

func (s *silentIMAPServer) reader(timeout time.Duration) *imapReader {
	return &imapReader{
		cfg: Config{
			Addr: s.listener.Addr().String(), User: "family@example.test",
			Password: testMailboxSecret, Mailbox: "INBOX", UseTLS: true, Timeout: timeout,
		},
		tlsConfig: &tls.Config{RootCAs: s.roots},
	}
}

func (s *silentIMAPServer) waitConnectionClosed(t *testing.T) {
	t.Helper()
	select {
	case <-s.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("server connection stayed open after reader returned")
	}
}

func TestLatestReturnsAtAbsoluteDeadlineWhenServerNeverResponds(t *testing.T) {
	server := newSilentIMAPServer(t)
	reader := server.reader(350 * time.Millisecond)

	started := time.Now()
	message, err := reader.Latest(context.Background(), "family@example.test", time.Now().Add(-time.Hour))
	elapsed := time.Since(started)

	if message != nil {
		t.Fatalf("message = %+v, want timeout", message)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("Latest took %s, want bounded by the configured timeout", elapsed)
	}
	for _, secret := range []string{testMailboxSecret, "family@example.test"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked %q: %v", secret, err)
		}
	}
	server.waitConnectionClosed(t)
}

func TestLatestReturnsPromptlyWhenContextIsCancelled(t *testing.T) {
	server := newSilentIMAPServer(t)
	reader := server.reader(30 * time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	started := time.Now()
	_, err := reader.Latest(ctx, "family@example.test", time.Now().Add(-time.Hour))
	elapsed := time.Since(started)
	cancel()

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("Latest took %s after cancellation; want a prompt return", elapsed)
	}
	server.waitConnectionClosed(t)
}

func TestLatestCancellationLeavesNoGoroutineBehind(t *testing.T) {
	server := newSilentIMAPServer(t)
	reader := server.reader(30 * time.Second)

	before := runtime.NumGoroutine()
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		if _, err := reader.Latest(ctx, "family@example.test", time.Now().Add(-time.Hour)); !errors.Is(err, context.DeadlineExceeded) {
			cancel()
			t.Fatalf("iteration %d error = %v, want deadline exceeded", i, err)
		}
		cancel()
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("goroutines before=%d after=%d; reader leaked a worker", before, runtime.NumGoroutine())
}

// fakeIMAPServer answers enough of the session for a successful lookup, then
// intentionally stays silent on LOGOUT when respondToLogout is false.
type fakeIMAPServer struct {
	listener        net.Listener
	roots           *x509.CertPool
	mu              sync.Mutex
	commands        []string
	respondToLogout bool
}

func newFakeIMAPServer(t *testing.T, respondToLogout bool) *fakeIMAPServer {
	t.Helper()
	certificate, roots := testCertificate(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &fakeIMAPServer{listener: listener, roots: roots, respondToLogout: respondToLogout}
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

func (s *fakeIMAPServer) serve(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	writer := bufio.NewWriter(conn)
	reader := bufio.NewReader(conn)
	if _, err := writer.WriteString("* OK [CAPABILITY IMAP4rev1] fake ready\r\n"); err != nil {
		return
	}
	if err := writer.Flush(); err != nil {
		return
	}
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
		command := strings.ToUpper(fields[1])
		s.mu.Lock()
		s.commands = append(s.commands, command)
		s.mu.Unlock()

		switch command {
		case "LOGIN":
			_, _ = writer.WriteString(tag + " OK LOGIN completed\r\n")
		case "SELECT", "EXAMINE":
			_, _ = writer.WriteString("* 0 EXISTS\r\n* 0 RECENT\r\n" + tag + " OK [READ-ONLY] completed\r\n")
		case "UID":
			// SEARCH with no matches; FETCH never follows in this test.
			_, _ = writer.WriteString("* SEARCH\r\n" + tag + " OK SEARCH completed\r\n")
		case "LOGOUT":
			if !s.respondToLogout {
				return
			}
			_, _ = writer.WriteString(tag + " OK LOGOUT completed\r\n* BYE\r\n")
		default:
			_, _ = writer.WriteString(tag + " BAD unsupported\r\n")
		}
		if err := writer.Flush(); err != nil {
			return
		}
	}
}

func (s *fakeIMAPServer) commandList() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

func TestLatestUsesReadOnlyExamineAndBoundsLogout(t *testing.T) {
	server := newFakeIMAPServer(t, false)
	reader := &imapReader{
		cfg: Config{
			Addr: server.listener.Addr().String(), User: "family@example.test",
			Password: testMailboxSecret, Mailbox: "INBOX", UseTLS: true, Timeout: 5 * time.Second,
		},
		tlsConfig:     &tls.Config{RootCAs: server.roots},
		logoutTimeout: 150 * time.Millisecond,
	}

	started := time.Now()
	_, err := reader.Latest(context.Background(), "family@example.test", time.Now().Add(-time.Hour))
	elapsed := time.Since(started)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("Latest took %s, logging out against a silent server must be bounded", elapsed)
	}
	commands := server.commandList()
	foundExamine := false
	for _, command := range commands {
		if command == "EXAMINE" {
			foundExamine = true
		}
		if command == "SELECT" {
			t.Fatalf("mailbox was opened with SELECT, commands = %v", commands)
		}
	}
	if !foundExamine {
		t.Fatalf("mailbox was not opened read-only, commands = %v", commands)
	}
}

func TestSessionErrorNormalizesSocketDeadlineToContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()

	err := &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}
	if got := sessionError(ctx, err); !errors.Is(got, context.DeadlineExceeded) {
		t.Fatalf("sessionError = %v, want context.DeadlineExceeded", got)
	}
}
