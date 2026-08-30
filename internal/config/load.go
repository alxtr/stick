package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load reads configuration from YAML and applies non-empty STICK_* environment
// variable overrides. If path is empty, config.yaml in the working directory
// is optional and environment variables may provide the complete config.
func Load(path string) (Config, error) {
	raw := rawConfig{}

	filePath := path
	if filePath == "" {
		filePath = "config.yaml"
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		if !os.IsNotExist(err) || path != "" {
			return Config{}, fmt.Errorf("reading config file %q: %w", filePath, err)
		}
	} else {
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(&raw); err != nil {
			return Config{}, fmt.Errorf("parsing config file %q: %w", filePath, err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			if err == nil {
				err = fmt.Errorf("multiple YAML documents are not supported")
			}
			return Config{}, fmt.Errorf("parsing config file %q: %w", filePath, err)
		}
	}

	if err := applyEnvOverrides(&raw); err != nil {
		return Config{}, err
	}
	return normalize(raw)
}

func applyEnvOverrides(raw *rawConfig) error {
	set := func(key string, dst *string) {
		if value := os.Getenv(key); value != "" {
			*dst = value
		}
	}

	set("STICK_DATABASE", &raw.Database)
	set("STICK_SERVER_PUBLIC_URL", &raw.Server.PublicURL)
	set("STICK_SERVER_LISTEN_ADDR", &raw.Server.ListenAddr)
	set("STICK_TIMEZONE", &raw.Timezone)
	set("STICK_AUTH_OIDC_ISSUER", &raw.Auth.OIDC.Issuer)
	set("STICK_AUTH_OIDC_CLIENT_ID", &raw.Auth.OIDC.ClientID)
	set("STICK_AUTH_OIDC_CLIENT_SECRET", &raw.Auth.OIDC.ClientSecret)
	set("STICK_AUTH_SESSION_SECRET", &raw.Auth.SessionSecret)
	if value := os.Getenv("STICK_AUTH_ADMIN_EMAILS"); value != "" {
		raw.Auth.AdminEmails = strings.Split(value, ",")
	}

	if err := applySMTPEnvOverrides(raw); err != nil {
		return err
	}
	if value := os.Getenv("STICK_NOTIFICATIONS_WEBHOOK_URL"); value != "" {
		if len(raw.Notifications.Webhook) == 0 {
			raw.Notifications.Webhook = append(raw.Notifications.Webhook, &rawWebhookConfig{})
		}
		raw.Notifications.Webhook[0].URL = value
	}
	if value := os.Getenv("STICK_NOTIFICATIONS_TEAMS_URL"); value != "" {
		if len(raw.Notifications.Teams) == 0 {
			raw.Notifications.Teams = append(raw.Notifications.Teams, &rawTeamsConfig{})
		}
		raw.Notifications.Teams[0].URL = value
	}
	return nil
}

func applySMTPEnvOverrides(raw *rawConfig) error {
	keys := []string{
		"STICK_NOTIFICATIONS_SMTP_HOST",
		"STICK_NOTIFICATIONS_SMTP_PORT",
		"STICK_NOTIFICATIONS_SMTP_TLS_MODE",
		"STICK_NOTIFICATIONS_SMTP_USERNAME",
		"STICK_NOTIFICATIONS_SMTP_PASSWORD",
		"STICK_NOTIFICATIONS_SMTP_FROM",
		"STICK_NOTIFICATIONS_SMTP_SUBJECT",
		"STICK_NOTIFICATIONS_SMTP_BODY",
	}
	configured := false
	for _, key := range keys {
		if os.Getenv(key) != "" {
			configured = true
			break
		}
	}
	if !configured {
		return nil
	}
	if len(raw.Notifications.SMTP) == 0 {
		raw.Notifications.SMTP = append(raw.Notifications.SMTP, &rawSMTPConfig{})
	}
	smtp := raw.Notifications.SMTP[0]
	set := func(key string, dst *string) {
		if value := os.Getenv(key); value != "" {
			*dst = value
		}
	}
	set("STICK_NOTIFICATIONS_SMTP_HOST", &smtp.Host)
	set("STICK_NOTIFICATIONS_SMTP_TLS_MODE", &smtp.TLSMode)
	set("STICK_NOTIFICATIONS_SMTP_USERNAME", &smtp.Username)
	set("STICK_NOTIFICATIONS_SMTP_PASSWORD", &smtp.Password)
	set("STICK_NOTIFICATIONS_SMTP_FROM", &smtp.From)
	set("STICK_NOTIFICATIONS_SMTP_SUBJECT", &smtp.Subject)
	set("STICK_NOTIFICATIONS_SMTP_BODY", &smtp.Body)
	if value := os.Getenv("STICK_NOTIFICATIONS_SMTP_PORT"); value != "" {
		port, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid STICK_NOTIFICATIONS_SMTP_PORT: %w", err)
		}
		smtp.Port = port
	}
	return nil
}
