package smtp

import (
	"context"
	"errors"
	"net/smtp"
	"strings"
	"testing"

	notify "stick/internal/notification"
)

func TestSMTPNotifierInjectedSenderRendersMessage(t *testing.T) {
	var capturedMsg []byte
	var capturedTo []string
	n, err := newNotifier(Config{
		Host:     "smtp.example.com",
		Port:     587,
		Username: "user",
		Password: "pass",
		From:     "noreply@example.com",
	}, Templates{Subject: "{{.StickName}} is available", Body: "Hey {{.RecipientName}}, {{.StickName}} is free."},
		func(_ context.Context, _ string, _ string, _ smtp.Auth, _ string, to []string, msg []byte) error {
			capturedTo = to
			capturedMsg = append([]byte(nil), msg...)
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Notify(context.Background(), notify.Notification{
		StickName:      "prod-deploy",
		RecipientName:  "Alice",
		RecipientEmail: "alice@example.com",
	}); err != nil {
		t.Fatal(err)
	}
	if len(capturedTo) != 1 || capturedTo[0] != "alice@example.com" {
		t.Fatalf("recipients = %v", capturedTo)
	}
	message := string(capturedMsg)
	if !strings.Contains(message, "prod-deploy is available") || !strings.Contains(message, "Hey Alice") {
		t.Fatalf("rendered message = %q", message)
	}
}

func TestSMTPNotifierInjectedSenderRejectsHeaderInjection(t *testing.T) {
	called := false
	n, err := newNotifier(
		Config{Host: "smtp.example.com", Port: 587, From: "noreply@example.com"},
		Templates{Subject: "{{.StickName}} ready", Body: "body"},
		func(context.Context, string, string, smtp.Auth, string, []string, []byte) error {
			called = true
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, notification := range []notify.Notification{
		{StickName: "foo\r\nBcc: attacker@example.com", RecipientEmail: "alice@example.com"},
		{StickName: "safe", RecipientEmail: "alice@example.com\r\nBcc: attacker@example.com"},
	} {
		if err := n.Notify(context.Background(), notification); err == nil {
			t.Fatal("expected header injection to be rejected")
		}
	}
	if called {
		t.Fatal("sender was called for invalid headers")
	}
}

func TestSMTPNotifierInjectedSenderReceivesContextAndTLSMode(t *testing.T) {
	wantErr := errors.New("send failed")
	var cancel context.CancelFunc
	n, err := newNotifier(
		Config{Host: "smtp.example.com", Port: 465, TLSMode: TLSModeImplicit, From: "noreply@example.com"},
		Templates{Subject: "subject", Body: "body"},
		func(ctx context.Context, _ string, tlsMode string, _ smtp.Auth, _ string, _ []string, _ []byte) error {
			if tlsMode != TLSModeImplicit {
				t.Errorf("TLS mode = %q", tlsMode)
			}
			cancel()
			<-ctx.Done()
			return errors.Join(wantErr, ctx.Err())
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = n.Notify(ctx, notify.Notification{RecipientEmail: "alice@example.com"})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, wantErr) {
		t.Fatalf("Notify error = %v", err)
	}
}
