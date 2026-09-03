package config

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"stick/internal/netutil"
	"stick/internal/publicurl"
)

const minSessionSecretBytes = 32

func normalize(config Config) (Config, error) {
	config.Server.ListenAddr = strings.TrimSpace(config.Server.ListenAddr)
	if config.Server.ListenAddr == "" {
		config.Server.ListenAddr = ":8080"
	}
	if config.Timezone == nil {
		config.Timezone = time.UTC
	}
	config.Auth.SessionSecret = []byte(strings.TrimSpace(string(config.Auth.SessionSecret)))

	notifications, err := normalizeNotifications(config.Notifications)
	if err != nil {
		return Config{}, err
	}
	config.Notifications = notifications

	if err := validateRequired(config); err != nil {
		return Config{}, err
	}
	if err := validateDatabase(config.Database); err != nil {
		return Config{}, err
	}
	if !config.Server.PublicURL.IsHTTPS() && !config.Server.PublicURL.IsLoopback() {
		return Config{}, fmt.Errorf("invalid server.public_url %q: HTTPS is required for non-local addresses", config.Server.PublicURL)
	}
	issuer, err := parseHTTPURL("auth.oidc.issuer", config.Auth.OIDC.Issuer)
	if err != nil {
		return Config{}, err
	}
	if issuer.RawQuery != "" || issuer.Fragment != "" {
		return Config{}, fmt.Errorf("invalid auth.oidc.issuer %q: must not include a query or fragment", config.Auth.OIDC.Issuer)
	}
	if err := validateSessionSecret(config.Auth.SessionSecret); err != nil {
		return Config{}, err
	}

	config.Auth.AdminEmails = append([]string(nil), config.Auth.AdminEmails...)
	return config, nil
}

func validateRequired(config Config) error {
	required := map[string]bool{
		"database":                strings.TrimSpace(config.Database.DSN) != "",
		"server.public_url":       config.Server.PublicURL.Validate() == nil,
		"auth.oidc.issuer":        strings.TrimSpace(config.Auth.OIDC.Issuer) != "",
		"auth.oidc.client_id":     strings.TrimSpace(config.Auth.OIDC.ClientID) != "",
		"auth.oidc.client_secret": strings.TrimSpace(config.Auth.OIDC.ClientSecret) != "",
		"auth.session_secret":     len(config.Auth.SessionSecret) > 0,
	}
	var missing []string
	for field, present := range required {
		if !present {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing required config: %v", missing)
	}
	return nil
}

func validateDatabase(config DatabaseConfig) error {
	switch config.Driver {
	case DatabaseDriverSQLite, DatabaseDriverPostgres, DatabaseDriverMongoDB:
		return nil
	default:
		return fmt.Errorf("unsupported database driver %q", config.Driver)
	}
}

func normalizeDatabase(value string) (DatabaseConfig, error) {
	dsn := strings.TrimSpace(value)
	parsed, err := url.Parse(dsn)
	if err != nil {
		return DatabaseConfig{}, fmt.Errorf("invalid database configuration")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "":
		return DatabaseConfig{Driver: DatabaseDriverSQLite, DSN: dsn}, nil
	case "postgres", "postgresql":
		return DatabaseConfig{Driver: DatabaseDriverPostgres, DSN: dsn}, nil
	case "mongodb", "mongodb+srv":
		return DatabaseConfig{Driver: DatabaseDriverMongoDB, DSN: dsn}, nil
	default:
		return DatabaseConfig{}, fmt.Errorf("unsupported database scheme %q", parsed.Scheme)
	}
}

func normalizeNotifications(config NotificationsConfig) (NotificationsConfig, error) {
	result := NotificationsConfig{}
	for _, smtp := range config.SMTP {
		if smtp == nil {
			return NotificationsConfig{}, fmt.Errorf("notifications.smtp entries must be mappings")
		}
		result.SMTP = append(result.SMTP, normalizeSMTP(smtp))
	}
	for _, webhook := range config.Webhook {
		if webhook == nil {
			return NotificationsConfig{}, fmt.Errorf("notifications.webhook entries must be mappings")
		}
		result.Webhook = append(result.Webhook, &WebhookConfig{URL: strings.TrimSpace(webhook.URL)})
	}
	for _, teams := range config.Teams {
		if teams == nil {
			return NotificationsConfig{}, fmt.Errorf("notifications.teams entries must be mappings")
		}
		result.Teams = append(result.Teams, &TeamsConfig{URL: strings.TrimSpace(teams.URL)})
	}
	return result, nil
}

func normalizeSMTP(config *SMTPConfig) *SMTPConfig {
	result := *config
	result.Host = strings.TrimSpace(result.Host)
	result.TLSMode = strings.ToLower(strings.TrimSpace(result.TLSMode))
	result.From = strings.TrimSpace(result.From)
	return &result
}

func parseHTTPURL(name, value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("invalid %s %q: %w", name, value, err)
	}
	if !parsed.IsAbs() || parsed.Hostname() == "" {
		return nil, fmt.Errorf("invalid %s %q: must be an absolute URL", name, value)
	}
	if !netutil.IsHTTPSOrLoopbackHTTP(parsed) {
		return nil, fmt.Errorf("invalid %s %q: HTTPS is required except for loopback addresses", name, value)
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("invalid %s %q: user information is not supported", name, value)
	}
	return parsed, nil
}

func validateSessionSecret(secret []byte) error {
	if len(secret) < minSessionSecretBytes {
		return fmt.Errorf("auth.session_secret must be at least %d bytes", minSessionSecretBytes)
	}
	return nil
}

func setDatabase(config *Config, value string) error {
	database, err := normalizeDatabase(value)
	if err != nil {
		return err
	}
	config.Database = database
	return nil
}

func setPublicURL(config *Config, value string) error {
	publicURL, err := publicurl.Parse(value)
	if err != nil {
		return err
	}
	config.Server.PublicURL = publicURL
	return nil
}

func setTimezone(config *Config, value string) error {
	location, err := time.LoadLocation(value)
	if err != nil {
		return fmt.Errorf("invalid timezone %q: %w", value, err)
	}
	config.Timezone = location
	return nil
}

func setSessionSecret(config *Config, value string) {
	config.Auth.SessionSecret = []byte(strings.TrimSpace(value))
}
