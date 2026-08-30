package webhook_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"stick/internal/adapters/notification/webhook"
	notify "stick/internal/notification"
)

func TestWebhookNotifier_PostsJSON(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("unmarshal body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n, err := webhook.New(webhook.Config{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	notif := notify.Notification{
		StickID:        "prod-deploy",
		StickName:      "prod-deploy",
		RecipientEmail: "alice@example.com",
	}

	if err := n.Notify(context.Background(), notif); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	wantKeys := []string{
		"stick_id", "stick_name", "holder_name", "holder_email", "duration",
		"released_at", "base_url", "recipient_name", "recipient_email",
	}
	if len(received) != len(wantKeys) {
		t.Fatalf("webhook keys = %v, want %v", received, wantKeys)
	}
	for _, key := range wantKeys {
		if _, ok := received[key]; !ok {
			t.Errorf("webhook payload missing key %q: %v", key, received)
		}
	}
	if received["stick_id"] != "prod-deploy" {
		t.Errorf("stick_id = %q, want prod-deploy", received["stick_id"])
	}
	if received["recipient_email"] != "alice@example.com" {
		t.Errorf("recipient_email = %q, want alice@example.com", received["recipient_email"])
	}
}

func TestWebhookNotifier_ErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n, err := webhook.New(webhook.Config{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	err = n.Notify(context.Background(), notify.Notification{})
	if err == nil {
		t.Fatal("expected error for non-2xx response")
	}
}

func TestWebhookNotifier_ExplicitTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))

	n, err := webhook.New(webhook.Config{URL: srv.URL, Timeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err = n.Notify(context.Background(), notify.Notification{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("webhook timeout took too long: %v", elapsed)
	}
	close(release)
	srv.Close()
}

func TestWebhookNotifierRejectsInvalidURL(t *testing.T) {
	for _, rawURL := range []string{"", "ftp://example.com/hook", "http://example.com/hook", "https://user@example.com/hook", "https://example.com/hook#fragment"} {
		if _, err := webhook.New(webhook.Config{URL: rawURL}); err == nil {
			t.Errorf("New(%q) succeeded", rawURL)
		}
	}
}
