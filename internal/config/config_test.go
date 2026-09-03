package config_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"stick/internal/config"
)

const testSecret = "0123456789abcdef0123456789abcdef"

var configEnvKeys = []string{
	"STICK_DATABASE",
	"STICK_SERVER_PUBLIC_URL",
	"STICK_SERVER_LISTEN_ADDR",
	"STICK_TIMEZONE",
	"STICK_AUTH_OIDC_ISSUER",
	"STICK_AUTH_OIDC_CLIENT_ID",
	"STICK_AUTH_OIDC_CLIENT_SECRET",
	"STICK_AUTH_SESSION_SECRET",
	"STICK_AUTH_ADMIN_EMAILS",
	"STICK_NOTIFICATIONS_SMTP_HOST",
	"STICK_NOTIFICATIONS_SMTP_PORT",
	"STICK_NOTIFICATIONS_SMTP_TLS_MODE",
	"STICK_NOTIFICATIONS_SMTP_USERNAME",
	"STICK_NOTIFICATIONS_SMTP_PASSWORD",
	"STICK_NOTIFICATIONS_SMTP_FROM",
	"STICK_NOTIFICATIONS_SMTP_SUBJECT",
	"STICK_NOTIFICATIONS_SMTP_BODY",
	"STICK_NOTIFICATIONS_WEBHOOK_URL",
	"STICK_NOTIFICATIONS_TEAMS_URL",
}

func setRequiredEnv(t *testing.T) {
	t.Helper()
	clearConfigEnv(t)
	t.Setenv("STICK_DATABASE", "/tmp/test.db")
	t.Setenv("STICK_SERVER_PUBLIC_URL", "http://localhost:8080")
	t.Setenv("STICK_AUTH_OIDC_ISSUER", "https://accounts.google.com")
	t.Setenv("STICK_AUTH_OIDC_CLIENT_ID", "client-id")
	t.Setenv("STICK_AUTH_OIDC_CLIENT_SECRET", "client-secret")
	t.Setenv("STICK_AUTH_SESSION_SECRET", testSecret)
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range configEnvKeys {
		t.Setenv(key, "")
	}
}

func loadFromEnv(t *testing.T) config.Config {
	t.Helper()
	dir := t.TempDir()
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })
	cfg, err := config.Load(context.Background(), config.YAMLProvider{}, config.EnvironmentProvider{})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad_FromEnvironment(t *testing.T) {
	setRequiredEnv(t)
	cfg := loadFromEnv(t)

	if cfg.Database.Driver != config.DatabaseDriverSQLite || cfg.Database.DSN != "/tmp/test.db" {
		t.Fatalf("database = %+v", cfg.Database)
	}
	if cfg.Server.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want :8080", cfg.Server.ListenAddr)
	}
	if cfg.Server.PublicURL.String() != "http://localhost:8080" {
		t.Errorf("PublicURL = %q", cfg.Server.PublicURL.String())
	}
	if cfg.Timezone != time.UTC {
		t.Errorf("Timezone = %v, want UTC", cfg.Timezone)
	}
	if len(cfg.Auth.AdminEmails) != 0 {
		t.Errorf("AdminEmails = %v, want empty", cfg.Auth.AdminEmails)
	}
}

func TestLoad_InfersPostgresFromURL(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("STICK_DATABASE", "postgres://user:secret@database.example/stick")
	cfg := loadFromEnv(t)

	if cfg.Database.Driver != config.DatabaseDriverPostgres || cfg.Database.DSN != "postgres://user:secret@database.example/stick" {
		t.Fatalf("database = %+v", cfg.Database)
	}
}

func TestLoad_InfersMongoDBFromURL(t *testing.T) {
	for _, dsn := range []string{
		"mongodb://user:secret@database.example/stick",
		"mongodb+srv://user:secret@cluster.example/stick",
	} {
		t.Run(dsn[:strings.IndexByte(dsn, ':')], func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("STICK_DATABASE", dsn)
			cfg := loadFromEnv(t)
			if cfg.Database.Driver != config.DatabaseDriverMongoDB || cfg.Database.DSN != dsn {
				t.Fatalf("database = %+v", cfg.Database)
			}
		})
	}
}

func TestLoad_FromYAML(t *testing.T) {
	clearConfigEnv(t)
	path := writeConfig(t, `
database: /tmp/yaml.db
server:
  public_url: https://yaml.example.com/ops/stick/
  listen_addr: :9090
auth:
  oidc:
    issuer: https://yaml.example.com
    client_id: yaml-client
    client_secret: yaml-secret
  session_secret: yaml-session-secret-that-is-long-enough
  admin_emails:
    - Alice@example.com
    - bob@example.com
timezone: America/New_York
`)

	cfg, err := config.Load(context.Background(), config.YAMLProvider{Path: path}, config.EnvironmentProvider{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Database.DSN != "/tmp/yaml.db" || cfg.Server.ListenAddr != ":9090" {
		t.Fatalf("config = %+v", cfg)
	}
	if got := cfg.Server.PublicURL.String(); got != "https://yaml.example.com/ops/stick" {
		t.Errorf("PublicURL = %q", got)
	}
	if got := cfg.Auth.AdminEmails; len(got) != 2 || got[0] != "Alice@example.com" || got[1] != "bob@example.com" {
		t.Errorf("AdminEmails = %v", got)
	}
	want, _ := time.LoadLocation("America/New_York")
	if cfg.Timezone.String() != want.String() {
		t.Errorf("Timezone = %v, want %v", cfg.Timezone, want)
	}
}

func TestLoad_EnvironmentOverridesYAML(t *testing.T) {
	clearConfigEnv(t)
	path := writeConfig(t, `
database: /tmp/from-yaml.db
server:
  public_url: http://yaml.example.com
auth:
  oidc:
    issuer: https://yaml.example.com
    client_id: yaml-client
    client_secret: yaml-secret
  session_secret: yaml-session-secret-that-is-long-enough
`)
	t.Setenv("STICK_DATABASE", "/tmp/from-env.db")
	t.Setenv("STICK_SERVER_PUBLIC_URL", "https://env.example.com/stick/")
	t.Setenv("STICK_AUTH_ADMIN_EMAILS", " Alice@Example.com, bob@example.com ")

	cfg, err := config.Load(context.Background(), config.YAMLProvider{Path: path}, config.EnvironmentProvider{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Database.DSN != "/tmp/from-env.db" {
		t.Errorf("Database.DSN = %q", cfg.Database.DSN)
	}
	if got := cfg.Server.PublicURL.String(); got != "https://env.example.com/stick" {
		t.Errorf("PublicURL = %q", got)
	}
	if got := cfg.Auth.AdminEmails; len(got) != 2 || got[0] != " Alice@Example.com" || got[1] != " bob@example.com " {
		t.Errorf("AdminEmails = %v", got)
	}
}

func TestLoad_ProvidersApplyInSuppliedOrder(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("STICK_SERVER_LISTEN_ADDR", ":7070")
	path := writeConfig(t, `
server:
  listen_addr: :9090
`)

	cfg, err := config.Load(
		context.Background(),
		config.YAMLProvider{Path: path},
		config.EnvironmentProvider{},
	)
	if err != nil {
		t.Fatalf("Load with YAML then environment: %v", err)
	}
	if cfg.Server.ListenAddr != ":7070" {
		t.Errorf("ListenAddr = %q, want environment value", cfg.Server.ListenAddr)
	}

	cfg, err = config.Load(
		context.Background(),
		config.EnvironmentProvider{},
		config.YAMLProvider{Path: path},
	)
	if err != nil {
		t.Fatalf("Load with environment then YAML: %v", err)
	}
	if cfg.Server.ListenAddr != ":9090" {
		t.Errorf("ListenAddr = %q, want YAML value", cfg.Server.ListenAddr)
	}
	if cfg.Database.DSN != "/tmp/test.db" {
		t.Errorf("Database.DSN = %q, want value preserved from environment", cfg.Database.DSN)
	}
}

func TestLoad_NotificationConfig(t *testing.T) {
	clearConfigEnv(t)
	path := writeConfig(t, `
database: /tmp/test.db
server:
  public_url: http://localhost:8080
auth:
  oidc:
    issuer: https://example.com
    client_id: c
    client_secret: s
  session_secret: yaml-session-secret-that-is-long-enough
notifications:
  smtp:
    host: smtp.example.com
    port: 465
    tls_mode: implicit
    username: user@example.com
    password: secret
    from: noreply@example.com
    subject: "{{.StickName}} ready"
    body: "Hello {{.RecipientName}}"
`)
	cfg, err := config.Load(context.Background(), config.YAMLProvider{Path: path}, config.EnvironmentProvider{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Notifications.SMTP) != 1 || cfg.Notifications.SMTP[0].Port != 465 || cfg.Notifications.SMTP[0].TLSMode != "implicit" {
		t.Fatalf("SMTP = %+v", cfg.Notifications.SMTP)
	}
	if cfg.Notifications.SMTP[0].Subject == "" || cfg.Notifications.SMTP[0].Body == "" {
		t.Fatal("SMTP templates were not loaded")
	}
}

func TestLoad_TeamsNotificationConfig(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("STICK_NOTIFICATIONS_TEAMS_URL", " https://teams.example.com/webhook ")
	cfg := loadFromEnv(t)
	if len(cfg.Notifications.Teams) != 1 || cfg.Notifications.Teams[0].URL != "https://teams.example.com/webhook" {
		t.Fatalf("Teams = %+v", cfg.Notifications.Teams)
	}
}

func TestLoad_AllowsMultipleNotificationBackends(t *testing.T) {
	clearConfigEnv(t)
	path := writeConfig(t, `
database: /tmp/test.db
server:
  public_url: http://localhost:8080
auth:
  oidc:
    issuer: https://example.com
    client_id: c
    client_secret: s
  session_secret: yaml-session-secret-that-is-long-enough
notifications:
  smtp:
    - host: smtp.example.com
    - host: backup-smtp.example.com
  webhook:
    - url: https://hooks.example.com/first-notify
    - url: https://hooks.example.com/second-notify
  teams:
    url: https://teams.example.com/webhook
`)
	cfg, err := config.Load(context.Background(), config.YAMLProvider{Path: path}, config.EnvironmentProvider{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Notifications.SMTP) != 2 || len(cfg.Notifications.Webhook) != 2 || len(cfg.Notifications.Teams) != 1 {
		t.Fatalf("notification backends = %+v, want all configured backends", cfg.Notifications)
	}
	if cfg.Notifications.Webhook[0].URL != "https://hooks.example.com/first-notify" ||
		cfg.Notifications.Webhook[1].URL != "https://hooks.example.com/second-notify" {
		t.Fatalf("webhook backends = %+v, want both configured instances", cfg.Notifications.Webhook)
	}
}

func TestLoad_TimezoneInvalid(t *testing.T) {
	clearConfigEnv(t)
	path := writeConfig(t, `
database: /tmp/test.db
server:
  public_url: http://localhost:8080
auth:
  oidc:
    issuer: https://example.com
    client_id: c
    client_secret: s
  session_secret: yaml-session-secret-that-is-long-enough
timezone: Not/ATimezone
`)
	if _, err := config.Load(context.Background(), config.YAMLProvider{Path: path}, config.EnvironmentProvider{}); err == nil || !strings.Contains(err.Error(), "timezone") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoad_RejectsShortSessionSecret(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("STICK_AUTH_SESSION_SECRET", "too-short")
	if _, err := config.Load(context.Background(), config.YAMLProvider{}, config.EnvironmentProvider{}); err == nil || !strings.Contains(err.Error(), "SESSION_SECRET") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoad_RejectsUnknownYAMLFields(t *testing.T) {
	clearConfigEnv(t)
	path := writeConfig(t, `
database: /tmp/test.db
server:
  public_url: https://stick.example.com
auth:
  oidc:
    issuer: https://issuer.example.com
    client_id: client
    client_secret: secret
  session_secret: 0123456789abcdef0123456789abcdef
unknown_setting: true
`)
	if _, err := config.Load(context.Background(), config.YAMLProvider{Path: path}, config.EnvironmentProvider{}); err == nil || !strings.Contains(err.Error(), "field unknown_setting not found") {
		t.Fatalf("unknown YAML field error = %v", err)
	}
}

func TestLoad_RejectsInvalidURLs(t *testing.T) {
	for _, test := range []struct {
		name  string
		key   string
		value string
	}{
		{name: "public URL scheme", key: "STICK_SERVER_PUBLIC_URL", value: "ftp://example.com"},
		{name: "public URL transport", key: "STICK_SERVER_PUBLIC_URL", value: "http://example.com"},
		{name: "OIDC issuer scheme", key: "STICK_AUTH_OIDC_ISSUER", value: "file:///issuer"},
		{name: "OIDC issuer transport", key: "STICK_AUTH_OIDC_ISSUER", value: "http://issuer.example.com"},
	} {
		t.Run(test.name, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv(test.key, test.value)
			if _, err := config.Load(context.Background(), config.YAMLProvider{}, config.EnvironmentProvider{}); err == nil {
				t.Fatal("expected invalid URL to be rejected")
			}
		})
	}
}

func TestLoad_ExplicitPathNotFound(t *testing.T) {
	if _, err := config.Load(context.Background(), config.YAMLProvider{Path: "/nonexistent/path/config.yaml"}, config.EnvironmentProvider{}); err == nil {
		t.Fatal("expected error for explicit missing config")
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	clearConfigEnv(t)
	if _, err := loadFromEnvWithoutRequired(t); err == nil || !strings.Contains(err.Error(), "missing required config") {
		t.Fatalf("Load error = %v", err)
	}
}

func loadFromEnvWithoutRequired(t *testing.T) (config.Config, error) {
	t.Helper()
	dir := t.TempDir()
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })
	return config.Load(context.Background(), config.YAMLProvider{}, config.EnvironmentProvider{})
}
