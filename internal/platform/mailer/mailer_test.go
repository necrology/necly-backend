package mailer

import (
	"bufio"
	"context"
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
