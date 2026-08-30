package teams_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"stick/internal/adapters/notification/teams"
	notify "stick/internal/application"
)

func TestTeamsNotifierPostsMessageCard(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("unmarshal body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	notifier, err := teams.New(teams.Config{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	err = notifier.Notify(context.Background(), notify.Notification{
		StickID:    "prod/deploy",
		StickName:  "prod-deploy",
		HolderName: "Alice",
		Duration:   "2 hours",
		ReleasedAt: "Jan 2 · 15:04",
		BaseURL:    "https://stick.example.com/stick/",
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if received["@type"] != "MessageCard" || received["@context"] != "http://schema.org/extensions" {
		t.Fatalf("card metadata = %v", received)
	}
	if received["title"] != "prod-deploy is available" {
		t.Errorf("title = %v", received["title"])
	}
	sections, ok := received["sections"].([]any)
	if !ok || len(sections) != 1 {
		t.Fatalf("sections = %v", received["sections"])
	}
	section := sections[0].(map[string]any)
	facts := section["facts"].([]any)
	if len(facts) != 4 || facts[0].(map[string]any)["value"] != "prod-deploy" {
		t.Fatalf("facts = %v", facts)
	}
	actions := received["potentialAction"].([]any)
	target := actions[0].(map[string]any)["targets"].([]any)[0].(map[string]any)
	if target["uri"] != "https://stick.example.com/stick/sticks/prod%2Fdeploy" {
		t.Errorf("claim URI = %v", target["uri"])
	}
}

func TestTeamsNotifierErrorsOnNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	notifier, err := teams.New(teams.Config{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := notifier.Notify(context.Background(), notify.Notification{}); err == nil || !strings.Contains(err.Error(), "teams webhook returned 502") {
		t.Fatalf("Notify error = %v", err)
	}
}

func TestTeamsNotifierExplicitTimeout(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	notifier, err := teams.New(teams.Config{URL: server.URL, Timeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := notifier.Notify(context.Background(), notify.Notification{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Notify error = %v, want deadline exceeded", err)
	}
}

func TestTeamsNotifierRejectsInvalidURL(t *testing.T) {
	for _, rawURL := range []string{"", "ftp://example.com/webhook", "http://example.com/webhook", "https://user@example.com/webhook", "https://example.com/webhook#fragment"} {
		if _, err := teams.New(teams.Config{URL: rawURL}); err == nil {
			t.Errorf("New(%q) succeeded", rawURL)
		}
	}
}
