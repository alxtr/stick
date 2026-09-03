package config

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/AppConfiguration-GoProvider/azureappconfiguration"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azappconfig/v2"
)

const azureTestSecret = "0123456789abcdef0123456789abcdef"

type testAzureCredential struct{}

func (testAzureCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type testAzureTransport struct {
	transport http.RoundTripper
}

func (t testAzureTransport) Do(request *http.Request) (*http.Response, error) {
	return t.transport.RoundTrip(request)
}

func azureTestOptions(transport http.RoundTripper) *azureappconfiguration.Options {
	return &azureappconfiguration.Options{
		ReplicaDiscoveryEnabled: boolPointer(false),
		ClientOptions: &azappconfig.ClientOptions{
			ClientOptions: azcore.ClientOptions{
				Transport: testAzureTransport{transport: transport},
				Retry:     policy.RetryOptions{MaxRetries: -1},
			},
		},
	}
}

func boolPointer(value bool) *bool {
	return &value
}

func TestAzureAppConfigurationProviderLoadsSettings(t *testing.T) {
	settings := []map[string]any{
		{"key": "stick/database", "value": "/tmp/azure.db", "label": "production"},
		{"key": "stick/server/public_url", "value": "http://localhost:8080", "label": "production"},
		{"key": "stick/auth/oidc/issuer", "value": "https://issuer.example.com", "label": "production"},
		{"key": "stick/auth/oidc/client_id", "value": "client-id", "label": "production"},
		{"key": "stick/auth/oidc/client_secret", "value": "client-secret", "label": "production"},
		{"key": "stick/auth/session_secret", "value": azureTestSecret, "label": "production"},
		{"key": "stick/auth/admin_emails", "value": `["Alice@example.com","bob@example.com"]`, "label": "production", "content_type": "application/json"},
		{"key": "stick/timezone", "value": "America/New_York", "label": "production"},
		{"key": "stick/notifications/webhook", "value": `[{"url":"https://hooks.example.com/stick"}]`, "label": "production", "content_type": "application/json"},
	}

	var requestSeen bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestSeen = true
		if r.URL.Path != "/kv" {
			t.Errorf("request path = %q, want /kv", r.URL.Path)
		}
		if got := r.URL.Query().Get("key"); got != "stick/*" {
			t.Errorf("key filter = %q, want stick/*", got)
		}
		if got := r.URL.Query().Get("label"); got != "production" {
			t.Errorf("label filter = %q, want production", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("authorization = %q, want bearer token", got)
		}
		w.Header().Set("Sync-Token", "id=value;sn=1")
		w.Header().Set("Content-Type", "application/vnd.microsoft.appconfig.kvset+json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": settings})
	}))
	defer server.Close()

	options := azureTestOptions(server.Client().Transport)
	provider := AzureAppConfigurationProvider{
		Endpoint:   server.URL,
		Label:      "production",
		KeyPrefix:  "stick/",
		Credential: testAzureCredential{},
		Options:    options,
	}

	cfg, err := Load(context.Background(), provider)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !requestSeen {
		t.Fatal("provider did not make an HTTP request")
	}
	if cfg.Database.DSN != "/tmp/azure.db" || cfg.Database.Driver != DatabaseDriverSQLite {
		t.Fatalf("database = %+v", cfg.Database)
	}
	if cfg.Server.PublicURL.String() != "http://localhost:8080" {
		t.Errorf("PublicURL = %q", cfg.Server.PublicURL)
	}
	if got := cfg.Auth.AdminEmails; len(got) != 2 || got[0] != "Alice@example.com" || got[1] != "bob@example.com" {
		t.Errorf("AdminEmails = %v", got)
	}
	if cfg.Timezone.String() != "America/New_York" {
		t.Errorf("Timezone = %v", cfg.Timezone)
	}
	if len(cfg.Notifications.Webhook) != 1 || cfg.Notifications.Webhook[0].URL != "https://hooks.example.com/stick" {
		t.Fatalf("Webhook = %+v", cfg.Notifications.Webhook)
	}
}

func TestAzureAppConfigurationProviderUsesDefaultLabelAndFilter(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("key"); got != "*" {
			t.Errorf("key filter = %q, want *", got)
		}
		if got := r.URL.Query().Get("label"); got != "\x00" {
			t.Errorf("label filter = %q, want no-label filter", got)
		}
		w.Header().Set("Sync-Token", "id=value;sn=1")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer server.Close()

	provider := AzureAppConfigurationProvider{
		Endpoint:   server.URL,
		Credential: testAzureCredential{},
		Options:    azureTestOptions(server.Client().Transport),
	}
	if _, err := Load(context.Background(), provider); err == nil || !strings.Contains(err.Error(), "missing required config") {
		t.Fatalf("Load error = %v, want missing configuration", err)
	}
}

func TestAzureAppConfigurationProviderSupportsCustomSeparator(t *testing.T) {
	settings := []map[string]string{
		{"key": "stick.database", "value": "/tmp/azure-dot.db"},
		{"key": "stick.server.public_url", "value": "http://localhost:8080"},
		{"key": "stick.auth.oidc.issuer", "value": "https://issuer.example.com"},
		{"key": "stick.auth.oidc.client_id", "value": "client-id"},
		{"key": "stick.auth.oidc.client_secret", "value": "client-secret"},
		{"key": "stick.auth.session_secret", "value": azureTestSecret},
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("key"); got != "stick.*" {
			t.Errorf("key filter = %q, want stick.*", got)
		}
		w.Header().Set("Sync-Token", "id=value;sn=1")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": settings})
	}))
	defer server.Close()

	provider := AzureAppConfigurationProvider{
		Endpoint:   server.URL,
		KeyPrefix:  "stick.",
		Separator:  ".",
		Credential: testAzureCredential{},
		Options:    azureTestOptions(server.Client().Transport),
	}
	config, err := Load(context.Background(), provider)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.Database.DSN != "/tmp/azure-dot.db" || config.Database.Driver != DatabaseDriverSQLite {
		t.Fatalf("database = %+v", config.Database)
	}
}

func TestAzureAppConfigurationProviderSupportsEverySeparator(t *testing.T) {
	for _, separator := range []string{".", ",", ";", "-", "_", "__", "/", ":"} {
		t.Run(separator, func(t *testing.T) {
			prefix := "stick" + separator
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got, want := r.URL.Query().Get("key"), prefix+"*"; got != want {
					t.Errorf("key filter = %q, want %q", got, want)
				}
				w.Header().Set("Sync-Token", "id=value;sn=1")
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]string{{
					"key":   prefix + "database",
					"value": "/tmp/azure-" + separator + ".db",
				}}})
			}))
			defer server.Close()

			provider := AzureAppConfigurationProvider{
				Endpoint:   server.URL,
				KeyPrefix:  "stick",
				Separator:  separator,
				Credential: testAzureCredential{},
				Options:    azureTestOptions(server.Client().Transport),
			}
			var cfg Config
			if err := provider.Apply(context.Background(), &cfg); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got, want := cfg.Database.DSN, "/tmp/azure-"+separator+".db"; got != want {
				t.Errorf("database = %q, want %q", got, want)
			}
		})
	}
}

func TestAzureAppConfigurationProviderRejectsInvalidEndpoint(t *testing.T) {
	for _, endpoint := range []string{
		"",
		"http://appconfig.example.com",
		"https://appconfig.example.com?secret=value",
		"https://appconfig.example.com/config",
	} {
		provider := AzureAppConfigurationProvider{
			Endpoint:   endpoint,
			Credential: testAzureCredential{},
		}
		if _, err := Load(context.Background(), provider); err == nil || !strings.Contains(err.Error(), "Azure App Configuration endpoint") {
			t.Errorf("endpoint %q error = %v", endpoint, err)
		}
	}
}

func TestAzureAppConfigurationProviderRejectsInvalidSeparator(t *testing.T) {
	provider := AzureAppConfigurationProvider{
		Endpoint:   "https://appconfig.example.com",
		Separator:  "invalid",
		Credential: testAzureCredential{},
	}
	if _, err := Load(context.Background(), provider); err == nil || !strings.Contains(err.Error(), "hierarchy separator") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestAzureAppConfigurationProviderRejectsUnknownKey(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Sync-Token", "id=value;sn=1")
		_, _ = w.Write([]byte(`{"items":[{"key":"stick/unknown","value":"value"}]}`))
	}))
	defer server.Close()

	provider := AzureAppConfigurationProvider{
		Endpoint:   server.URL,
		KeyPrefix:  "stick/",
		Credential: testAzureCredential{},
		Options:    azureTestOptions(server.Client().Transport),
	}
	if _, err := Load(context.Background(), provider); err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestAzureAppConfigurationProviderHonorsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider := AzureAppConfigurationProvider{Endpoint: "https://appconfig.example.com"}
	if _, err := Load(ctx, provider); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("Load error = %v, want context cancellation", err)
	}
}
