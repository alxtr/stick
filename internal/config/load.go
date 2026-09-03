package config

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"stick/internal/auth"
	"stick/internal/netutil"
	"stick/internal/publicurl"

	"gopkg.in/yaml.v3"
)

// Provider applies configuration values to the accumulated raw configuration.
// Providers are applied in the order supplied to Load. A provider must only
// change values present in its source and must not reset the accumulated
// configuration.
type Provider interface {
	Apply(context.Context, *rawConfig) error
}

// Load applies providers in order and normalizes the resulting configuration.
// Later providers override values supplied by earlier providers.
func Load(ctx context.Context, providers ...Provider) (Config, error) {
	raw := rawConfig{}
	for _, provider := range providers {
		if err := provider.Apply(ctx, &raw); err != nil {
			return Config{}, fmt.Errorf("%T: %w", provider, err)
		}
	}
	return normalize(raw)
}

const minSessionSecretBytes = 32

func normalize(raw rawConfig) (Config, error) {
	if err := validateRequired(raw); err != nil {
		return Config{}, err
	}
	database, err := normalizeDatabase(raw.Database)
	if err != nil {
		return Config{}, err
	}
	publicURL, err := publicurl.Parse(raw.Server.PublicURL)
	if err != nil {
		return Config{}, err
	}
	if !publicURL.IsHTTPS() && !publicURL.IsLoopback() {
		return Config{}, fmt.Errorf("invalid PUBLIC_URL %q: HTTPS is required for non-local addresses", raw.Server.PublicURL)
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
		}
	}

	notifications, err := normalizeNotifications(raw.Notifications)
	if err != nil {
		return Config{}, err
	}
	return Config{
		Server: ServerConfig{
			PublicURL:  publicURL,
			ListenAddr: listenAddr,
		},
		Database: database,
		Auth: AuthConfig{
			OIDC: auth.OIDCConfig{
				Issuer:       raw.Auth.OIDC.Issuer,
				ClientID:     raw.Auth.OIDC.ClientID,
				ClientSecret: raw.Auth.OIDC.ClientSecret,
			},
			SessionSecret: []byte(strings.TrimSpace(raw.Auth.SessionSecret)),
			AdminEmails:   append([]string(nil), raw.Auth.AdminEmails...),
		},
		Timezone:      location,
		Notifications: notifications,
	}, nil
}

func validateRequired(raw rawConfig) error {
	required := map[string]string{
		"STICK_DATABASE":                raw.Database,
		"STICK_SERVER_PUBLIC_URL":       raw.Server.PublicURL,
		"STICK_AUTH_OIDC_ISSUER":        raw.Auth.OIDC.Issuer,
		"STICK_AUTH_OIDC_CLIENT_ID":     raw.Auth.OIDC.ClientID,
		"STICK_AUTH_OIDC_CLIENT_SECRET": raw.Auth.OIDC.ClientSecret,
		"STICK_AUTH_SESSION_SECRET":     raw.Auth.SessionSecret,
	}
	var missing []string
	for key, value := range required {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing required config: %v", missing)
	}
	if err := validateSessionSecret(raw.Auth.SessionSecret); err != nil {
		return err
	}
	issuer, err := parseHTTPURL("STICK_AUTH_OIDC_ISSUER", raw.Auth.OIDC.Issuer)
	if err != nil {
		return err
	}
	if issuer.RawQuery != "" || issuer.Fragment != "" {
		return fmt.Errorf("invalid STICK_AUTH_OIDC_ISSUER %q: must not include a query or fragment", raw.Auth.OIDC.Issuer)
	}
	return nil
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

func normalizeNotifications(raw rawNotificationsConfig) (NotificationsConfig, error) {
	result := NotificationsConfig{}
	for _, smtp := range raw.SMTP {
		if smtp == nil {
			return NotificationsConfig{}, fmt.Errorf("notifications.smtp entries must be mappings")
		}
		result.SMTP = append(result.SMTP, normalizeSMTP(smtp))
	}
	for _, webhook := range raw.Webhook {
		if webhook == nil {
			return NotificationsConfig{}, fmt.Errorf("notifications.webhook entries must be mappings")
		}
		result.Webhook = append(result.Webhook, &WebhookConfig{URL: strings.TrimSpace(webhook.URL)})
	}
	for _, teams := range raw.Teams {
		if teams == nil {
			return NotificationsConfig{}, fmt.Errorf("notifications.teams entries must be mappings")
		}
		result.Teams = append(result.Teams, &TeamsConfig{URL: strings.TrimSpace(teams.URL)})
	}
	return result, nil
}

func normalizeSMTP(raw *rawSMTPConfig) *SMTPConfig {
	return &SMTPConfig{
		Host:     strings.TrimSpace(raw.Host),
		Port:     raw.Port,
		TLSMode:  strings.ToLower(strings.TrimSpace(raw.TLSMode)),
		Username: raw.Username,
		Password: raw.Password,
		From:     strings.TrimSpace(raw.From),
		Subject:  raw.Subject,
		Body:     raw.Body,
	}
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

func validateSessionSecret(secret string) error {
	secret = strings.TrimSpace(secret)
	if len([]byte(secret)) < minSessionSecretBytes {
		return fmt.Errorf("STICK_AUTH_SESSION_SECRET must be at least %d bytes", minSessionSecretBytes)
	}
	return nil
}

// The notification fields accept both the original mapping form for one
// backend and a sequence form for multiple instances of that backend.
type rawSMTPConfigs []*rawSMTPConfig
type rawWebhookConfigs []*rawWebhookConfig
type rawTeamsConfigs []*rawTeamsConfig

func (c *rawSMTPConfigs) UnmarshalYAML(node *yaml.Node) error {
	items, err := decodeNotificationConfigs[rawSMTPConfig](node, "smtp", []string{
		"host", "port", "tls_mode", "username", "password", "from", "subject", "body",
	})
	if err != nil {
		return err
	}
	*c = items
	return nil
}

func (c *rawWebhookConfigs) UnmarshalYAML(node *yaml.Node) error {
	items, err := decodeNotificationConfigs[rawWebhookConfig](node, "webhook", []string{"url"})
	if err != nil {
		return err
	}
	*c = items
	return nil
}

func (c *rawTeamsConfigs) UnmarshalYAML(node *yaml.Node) error {
	items, err := decodeNotificationConfigs[rawTeamsConfig](node, "teams", []string{"url"})
	if err != nil {
		return err
	}
	*c = items
	return nil
}

func decodeNotificationConfigs[T any](node *yaml.Node, name string, fields []string) ([]*T, error) {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
		return nil, nil
	}

	if node.Kind == yaml.MappingNode {
		if err := validateNotificationFields(node, name, fields); err != nil {
			return nil, err
		}
		var item T
		if err := node.Decode(&item); err != nil {
			return nil, err
		}
		return []*T{&item}, nil
	}

	if node.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("notifications.%s must be a mapping or sequence", name)
	}
	for _, item := range node.Content {
		if item.Kind == yaml.ScalarNode && item.Tag == "!!null" {
			continue
		}
		if item.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("notifications.%s entries must be mappings", name)
		}
		if err := validateNotificationFields(item, name, fields); err != nil {
			return nil, err
		}
	}

	var items []*T
	if err := node.Decode(&items); err != nil {
		return nil, err
	}
	return items, nil
}

func validateNotificationFields(node *yaml.Node, name string, fields []string) error {
	known := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		known[field] = struct{}{}
	}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if _, ok := known[key]; !ok {
			return fmt.Errorf("field %s not found in notifications.%s", key, name)
		}
	}
	return nil
}
