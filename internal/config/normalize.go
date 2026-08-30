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

func normalize(config Config) (Config, error) {
	config.Server.ListenAddr = strings.TrimSpace(config.Server.ListenAddr)
	if config.Server.ListenAddr == "" {
		config.Server.ListenAddr = ":8080"
	}
<<<<<<< HEAD
	if err := config.Server.PublicURL.Validate(); err != nil {
		if err := setPublicURL(&config, "http://localhost"); err != nil {
			return Config{}, err
=======
	database, err := normalizeDatabase(raw.Database)
	if err != nil {
		return Config{}, err
	}
	publicURLValue := strings.TrimSpace(raw.Server.PublicURL)
	if publicURLValue == "" {
		publicURLValue = "http://localhost"
	}
	publicURL, err := publicurl.Parse(publicURLValue)
	if err != nil {
		return Config{}, err
	}
	parsedPublicURL, err := url.Parse(publicURL)
	if err != nil {
		// publicurl.Parse already parsed and validated this value. Keep this
		// guard local to the configuration boundary if that implementation
		// changes in the future.
		return Config{}, fmt.Errorf("invalid PUBLIC_URL %q: %w", publicURLValue, err)
	}
	if parsedPublicURL.Scheme != "https" && !netutil.IsLoopbackHost(parsedPublicURL.Hostname()) {
		return Config{}, fmt.Errorf("invalid PUBLIC_URL %q: HTTPS is required for non-local addresses", publicURLValue)
	}

	listenAddr := strings.TrimSpace(raw.Server.ListenAddr)
	if listenAddr == "" {
		listenAddr = ":8080"
	}

	location := time.UTC
	if raw.Timezone != "" {
		location, err = time.LoadLocation(raw.Timezone)
		if err != nil {
			return Config{}, fmt.Errorf("invalid timezone %q: %w", raw.Timezone, err)
>>>>>>> 39d6435 (Trying to factor out publicurl)
		}
	}
	if config.Timezone == nil {
		config.Timezone = time.UTC
	}
	config.Auth.IDPEndpoint = strings.TrimSpace(config.Auth.IDPEndpoint)
	config.Auth.Audience = strings.TrimSpace(config.Auth.Audience)
	config.Auth.Scope = strings.TrimSpace(config.Auth.Scope)

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
	endpoint, err := parseHTTPURL("auth.idp_endpoint", config.Auth.IDPEndpoint)
	if err != nil {
		return Config{}, err
	}
	if endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return Config{}, fmt.Errorf("invalid auth.idp_endpoint %q: must not include a query or fragment", config.Auth.IDPEndpoint)
	}

	config.Auth.AdminEmails = append([]string(nil), config.Auth.AdminEmails...)
	return config, nil
}

func validateRequired(config Config) error {
	required := map[string]bool{
		"database":          strings.TrimSpace(config.Database.DSN) != "",
		"auth.idp_endpoint": strings.TrimSpace(config.Auth.IDPEndpoint) != "",
		"auth.audience":     strings.TrimSpace(config.Auth.Audience) != "",
		"auth.scope":        strings.TrimSpace(config.Auth.Scope) != "",
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

func setDatabase(config *Config, value string) error {
	database, err := normalizeDatabase(value)
	if err != nil {
		return err
	}
	config.Database = database
	return nil
}

func setPublicURL(config *Config, value string) error {
	publicURL, err := publicurl.Parse(strings.TrimSpace(value))
	if err != nil {
		return err
	}
	config.Server.PublicURL = publicURL
	return nil
}

func setTimezone(config *Config, value string) error {
	location, err := time.LoadLocation(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("invalid timezone %q: %w", value, err)
	}
	config.Timezone = location
	return nil
}
