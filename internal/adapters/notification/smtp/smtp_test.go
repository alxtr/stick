package smtp_test

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"stick/internal/adapters/notification/smtp"
	notify "stick/internal/application"
)

func TestNewSMTPNotifier_ValidatesConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  smtp.Config
	}{
		{name: "missing host", cfg: smtp.Config{Port: 587, From: "from@example.com"}},
		{name: "invalid port", cfg: smtp.Config{Host: "smtp.example.com", Port: 0, From: "from@example.com"}},
		{name: "unsupported TLS mode", cfg: smtp.Config{Host: "smtp.example.com", Port: 587, TLSMode: "none", From: "from@example.com"}},
		{name: "from injection", cfg: smtp.Config{Host: "smtp.example.com", Port: 587, From: "from@example.com\nBcc: attacker@example.com"}},
		{name: "password without username", cfg: smtp.Config{Host: "smtp.example.com", Port: 587, Password: "secret", From: "from@example.com"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := smtp.New(tt.cfg, smtp.Templates{Subject: "subject", Body: "body"}); err == nil {
				t.Fatal("expected invalid SMTP config to be rejected")
			}
		})
	}
}

func TestNewSMTPNotifier_AcceptsImplicitTLS(t *testing.T) {
	n, err := smtp.New(
		smtp.Config{
			Host:    "smtp.example.com",
			Port:    465,
			TLSMode: smtp.TLSModeImplicit,
			From:    "noreply@example.com",
		},
		smtp.Templates{Subject: "subject", Body: "body"},
	)
	if err != nil {
		t.Fatalf("NewSMTPNotifier: %v", err)
	}
	_ = n
}

func TestNewSMTPNotifier_InvalidSubjectTemplate(t *testing.T) {
	cfg := smtp.Config{Host: "h", Port: 587, From: "f@f.com"}
	_, err := smtp.New(cfg, smtp.Templates{Subject: "{{.Broken", Body: "body"})
	if err == nil {
		t.Fatal("expected error for invalid subject template")
	}
}

func TestNewSMTPNotifier_InvalidBodyTemplate(t *testing.T) {
	cfg := smtp.Config{Host: "h", Port: 587, From: "f@f.com"}
	_, err := smtp.New(cfg, smtp.Templates{Subject: "subject", Body: "{{.Broken"})
	if err == nil {
		t.Fatal("expected error for invalid body template")
	}
}

func TestSMTPNotifier_DefaultSenderHonorsDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	address := listener.Addr().String()
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("Atoi: %v", err)
	}
	n, err := smtp.New(
		smtp.Config{Host: host, Port: port, From: "noreply@example.com"},
		smtp.Templates{Subject: "subject", Body: "body"},
	)
	if err != nil {
		t.Fatalf("NewSMTPNotifier: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err = n.Notify(ctx, notify.Notification{RecipientEmail: "alice@example.com"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	select {
	case conn := <-accepted:
		conn.Close()
	case <-time.After(time.Second):
		t.Fatal("SMTP connection was not accepted")
	}
}

func TestSMTPNotifier_DefaultRequiresSTARTTLS(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		if _, err := conn.Write([]byte("220 localhost ESMTP\r\n")); err != nil {
			serverDone <- err
			return
		}
		if _, err := bufio.NewReader(conn).ReadString('\n'); err != nil {
			serverDone <- err
			return
		}
		_, err = conn.Write([]byte("250-localhost\r\n250 AUTH PLAIN\r\n"))
		serverDone <- err
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("Atoi: %v", err)
	}
	n, err := smtp.New(
		smtp.Config{Host: host, Port: port, From: "noreply@example.com"},
		smtp.Templates{Subject: "subject", Body: "body"},
	)
	if err != nil {
		t.Fatalf("NewSMTPNotifier: %v", err)
	}

	err = n.Notify(context.Background(), notify.Notification{RecipientEmail: "alice@example.com"})
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("expected required STARTTLS error, got %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("SMTP test server: %v", err)
	}
}
