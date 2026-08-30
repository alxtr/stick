package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"stick/internal/application"
	"stick/internal/config"
)

func TestMainContextStopsOnCanceledParent(t *testing.T) {
	t.Setenv("STICK_DATABASE", filepath.Join(t.TempDir(), "stick.db"))
	t.Setenv("STICK_AUTH_IDP_ENDPOINT", "https://accounts.google.com")
	t.Setenv("STICK_AUTH_AUDIENCE", "stick-api")
	t.Setenv("STICK_AUTH_SCOPE", "stick:use")
	t.Setenv("STICK_SERVER_PUBLIC_URL", "http://localhost:8080")
	t.Setenv("STICK_SERVER_LISTEN_ADDR", "127.0.0.1:0")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := mainContext(ctx, nil); err != nil {
		t.Fatalf("mainContext returned %v", err)
	}
}

func TestMainContextPropagatesDatabaseStartupFailure(t *testing.T) {
	setMinimalRuntimeEnv(t, filepath.Join(t.TempDir(), "missing", "stick.db"))
	if err := mainContext(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "db:") {
		t.Fatalf("mainContext error = %v, want database startup failure", err)
	}
}

func TestMainContextLogsBuildMetadata(t *testing.T) {
	setMinimalRuntimeEnv(t, filepath.Join(t.TempDir(), "stick.db"))
	previousLogger := slog.Default()
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	previousCommit, previousDate := commit, buildDate
	commit, buildDate = "test-commit", "test-date"
	t.Cleanup(func() { commit, buildDate = previousCommit, previousDate })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := mainContext(ctx, nil); err != nil {
		t.Fatalf("mainContext returned %v", err)
	}
	if !strings.Contains(logs.String(), `"version":"1.0.0"`) ||
		!strings.Contains(logs.String(), `"commit":"test-commit"`) ||
		!strings.Contains(logs.String(), `"build_date":"test-date"`) {
		t.Fatalf("startup log = %s", logs.String())
	}
}

func TestBuildConfigProvidersDefaultsToYAMLThenEnvironment(t *testing.T) {
	t.Setenv("STICK_CONFIG_PROVIDERS", "")
	providers, err := buildConfigProviders("", "")
	if err != nil {
		t.Fatalf("buildConfigProviders: %v", err)
	}
	if len(providers) != 2 {
		t.Fatalf("providers = %d, want 2", len(providers))
	}
	if _, ok := providers[0].(config.YAMLProvider); !ok {
		t.Errorf("providers[0] = %T, want YAMLProvider", providers[0])
	}
	if _, ok := providers[1].(config.EnvironmentProvider); !ok {
		t.Errorf("providers[1] = %T, want EnvironmentProvider", providers[1])
	}
}

func TestBuildConfigProvidersReadsSelectionFromEnvironment(t *testing.T) {
	t.Setenv("STICK_CONFIG_PROVIDERS", "environment, azure-app-config")
	t.Setenv("STICK_AZURE_APPCONFIG_ENDPOINT", "https://stick.azconfig.io")
	t.Setenv("STICK_AZURE_APPCONFIG_SEPARATOR", ".")
	providers, err := buildConfigProviders("", "")
	if err != nil {
		t.Fatalf("buildConfigProviders: %v", err)
	}
	if len(providers) != 2 {
		t.Fatalf("providers = %d, want 2", len(providers))
	}
	if _, ok := providers[0].(config.EnvironmentProvider); !ok {
		t.Errorf("providers[0] = %T, want EnvironmentProvider", providers[0])
	}
	azure, ok := providers[1].(config.AzureAppConfigurationProvider)
	if !ok {
		t.Fatalf("providers[1] = %T, want AzureAppConfigurationProvider", providers[1])
	}
	if azure.Endpoint != "https://stick.azconfig.io" {
		t.Errorf("Azure endpoint = %q", azure.Endpoint)
	}
	if azure.Separator != "." {
		t.Errorf("Azure separator = %q", azure.Separator)
	}
}

func TestBuildConfigProvidersCLISelectionOverridesEnvironment(t *testing.T) {
	t.Setenv("STICK_CONFIG_PROVIDERS", "azure-app-config")
	providers, err := buildConfigProviders("", "environment")
	if err != nil {
		t.Fatalf("buildConfigProviders: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(providers))
	}
	if _, ok := providers[0].(config.EnvironmentProvider); !ok {
		t.Errorf("provider = %T, want EnvironmentProvider", providers[0])
	}
}

func TestBuildConfigProvidersRequiresYAMLForExplicitConfigPath(t *testing.T) {
	if _, err := buildConfigProviders("/etc/stick/config.yaml", "environment"); err == nil || !strings.Contains(err.Error(), "requires the yaml") {
		t.Fatalf("buildConfigProviders error = %v", err)
	}
}

func TestBuildConfigProvidersRejectsUnknownProvider(t *testing.T) {
	if _, err := buildConfigProviders("", "unknown"); err == nil || !strings.Contains(err.Error(), "unknown configuration provider") {
		t.Fatalf("buildConfigProviders error = %v", err)
	}
}

func setMinimalRuntimeEnv(t *testing.T, databasePath string) {
	t.Helper()
	t.Setenv("STICK_DATABASE", databasePath)
	t.Setenv("STICK_AUTH_IDP_ENDPOINT", "https://accounts.google.com")
	t.Setenv("STICK_AUTH_AUDIENCE", "stick-api")
	t.Setenv("STICK_AUTH_SCOPE", "stick:use")
	t.Setenv("STICK_SERVER_PUBLIC_URL", "http://localhost:8080")
	t.Setenv("STICK_SERVER_LISTEN_ADDR", "127.0.0.1:0")
}

func TestBuildNotifierWithNoBackendsSucceeds(t *testing.T) {
	notifier, err := buildNotifier(config.NotificationsConfig{})
	if err != nil {
		t.Fatalf("buildNotifier: %v", err)
	}
	if notifier != nil {
		t.Fatalf("empty notifier = %T, want nil", notifier)
	}
}

func TestBuildNotifierComposesWebhook(t *testing.T) {
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		received <- string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	notifier, err := buildNotifier(config.NotificationsConfig{
		Webhook: []*config.WebhookConfig{{URL: server.URL}},
	})
	if err != nil {
		t.Fatalf("buildNotifier: %v", err)
	}
	if err := notifier.Notify(context.Background(), application.Notification{StickID: "aa001"}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if payload := <-received; !strings.Contains(payload, `"stick_id":"aa001"`) {
		t.Fatalf("webhook payload = %s", payload)
	}
}

func TestBuildNotifierComposesTeams(t *testing.T) {
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		received <- string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	notifier, err := buildNotifier(config.NotificationsConfig{
		Teams: []*config.TeamsConfig{{URL: server.URL}},
	})
	if err != nil {
		t.Fatalf("buildNotifier: %v", err)
	}
	if err := notifier.Notify(context.Background(), application.Notification{StickName: "aa001"}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if payload := <-received; !strings.Contains(payload, `"@type":"MessageCard"`) || !strings.Contains(payload, `"title":"aa001 is available"`) {
		t.Fatalf("Teams payload = %s", payload)
	}
}

func TestBuildNotifierComposesMultipleBackends(t *testing.T) {
	receivedWebhook := make(chan struct{}, 1)
	webhookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		receivedWebhook <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhookServer.Close()

	receivedTeams := make(chan struct{}, 1)
	teamsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		receivedTeams <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer teamsServer.Close()

	notifier, err := buildNotifier(config.NotificationsConfig{
		Webhook: []*config.WebhookConfig{{URL: webhookServer.URL}},
		Teams:   []*config.TeamsConfig{{URL: teamsServer.URL}},
	})
	if err != nil {
		t.Fatalf("buildNotifier: %v", err)
	}
	if err := notifier.Notify(context.Background(), application.Notification{}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	select {
	case <-receivedWebhook:
	default:
		t.Fatal("webhook backend was not called")
	}
	select {
	case <-receivedTeams:
	default:
		t.Fatal("Teams backend was not called")
	}
}

func TestBuildNotifierComposesMultipleInstancesOfOneBackend(t *testing.T) {
	received := make(chan struct{}, 2)
	newServer := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			received <- struct{}{}
			w.WriteHeader(http.StatusNoContent)
		}))
	}
	first := newServer()
	defer first.Close()
	second := newServer()
	defer second.Close()

	notifier, err := buildNotifier(config.NotificationsConfig{
		Webhook: []*config.WebhookConfig{{URL: first.URL}, {URL: second.URL}},
	})
	if err != nil {
		t.Fatalf("buildNotifier: %v", err)
	}
	if err := notifier.Notify(context.Background(), application.Notification{}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-received:
		case <-time.After(time.Second):
			t.Fatalf("webhook backend %d was not called", i+1)
		}
	}
}

func TestBuildNotifierAttributesConstructionErrors(t *testing.T) {
	_, err := buildNotifier(config.NotificationsConfig{
		SMTP:    []*config.SMTPConfig{{}},
		Webhook: []*config.WebhookConfig{{URL: "://invalid"}},
	})
	if err == nil || !strings.Contains(err.Error(), "smtp notifier: SMTP host is required") {
		t.Fatalf("SMTP construction error = %v", err)
	}

	_, err = buildNotifier(config.NotificationsConfig{
		Webhook: []*config.WebhookConfig{{URL: "://invalid"}},
	})
	if err == nil || !strings.Contains(err.Error(), "webhook notifier: invalid webhook URL") {
		t.Fatalf("webhook construction error = %v", err)
	}

	_, err = buildNotifier(config.NotificationsConfig{
		Teams: []*config.TeamsConfig{{URL: "://invalid"}},
	})
	if err == nil || !strings.Contains(err.Error(), "teams notifier: invalid Teams webhook URL") {
		t.Fatalf("Teams construction error = %v", err)
	}
}

func TestBuildNotifierAttributesRuntimeErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	notifier, err := buildNotifier(config.NotificationsConfig{
		Webhook: []*config.WebhookConfig{{URL: server.URL}},
	})
	if err != nil {
		t.Fatalf("buildNotifier: %v", err)
	}
	err = notifier.Notify(context.Background(), application.Notification{})
	if err == nil || !strings.Contains(err.Error(), "webhook: webhook returned 502") {
		t.Fatalf("runtime error = %v", err)
	}
}
