package config

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// EnvironmentProvider applies non-empty STICK_* environment variable
// overrides to the accumulated configuration.
type EnvironmentProvider struct{}

// Apply applies environment variable overrides.
func (EnvironmentProvider) Apply(_ context.Context, config *Config) error {
	if value := os.Getenv("STICK_DATABASE"); value != "" {
		if err := setDatabase(config, value); err != nil {
			return err
		}
	}
	if value := os.Getenv("STICK_SERVER_PUBLIC_URL"); value != "" {
		if err := setPublicURL(config, value); err != nil {
			return err
		}
	}
	if value := os.Getenv("STICK_SERVER_LISTEN_ADDR"); value != "" {
		config.Server.ListenAddr = strings.TrimSpace(value)
	}
	if value := os.Getenv("STICK_TIMEZONE"); value != "" {
		if err := setTimezone(config, value); err != nil {
			return err
		}
	}
	if value := os.Getenv("STICK_AUTH_OIDC_ISSUER"); value != "" {
		config.Auth.OIDC.Issuer = value
	}
	if value := os.Getenv("STICK_AUTH_OIDC_CLIENT_ID"); value != "" {
		config.Auth.OIDC.ClientID = value
	}
	if value := os.Getenv("STICK_AUTH_OIDC_CLIENT_SECRET"); value != "" {
		config.Auth.OIDC.ClientSecret = value
	}
	if value := os.Getenv("STICK_AUTH_SESSION_SECRET"); value != "" {
		setSessionSecret(config, value)
	}
	if value := os.Getenv("STICK_AUTH_ADMIN_EMAILS"); value != "" {
		config.Auth.AdminEmails = strings.Split(value, ",")
	}

	if err := applySMTPEnvOverrides(config); err != nil {
		return err
	}
	if value := os.Getenv("STICK_NOTIFICATIONS_WEBHOOK_URL"); value != "" {
		if len(config.Notifications.Webhook) == 0 {
			config.Notifications.Webhook = append(config.Notifications.Webhook, &WebhookConfig{})
		}
		if config.Notifications.Webhook[0] == nil {
			config.Notifications.Webhook[0] = &WebhookConfig{}
		}
		config.Notifications.Webhook[0].URL = strings.TrimSpace(value)
	}
	if value := os.Getenv("STICK_NOTIFICATIONS_TEAMS_URL"); value != "" {
		if len(config.Notifications.Teams) == 0 {
			config.Notifications.Teams = append(config.Notifications.Teams, &TeamsConfig{})
		}
		if config.Notifications.Teams[0] == nil {
			config.Notifications.Teams[0] = &TeamsConfig{}
		}
		config.Notifications.Teams[0].URL = strings.TrimSpace(value)
	}
	return nil
}

func applySMTPEnvOverrides(config *Config) error {
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

	var smtp SMTPConfig
	if len(config.Notifications.SMTP) == 0 {
		config.Notifications.SMTP = append(config.Notifications.SMTP, &smtp)
	} else if config.Notifications.SMTP[0] != nil {
		smtp = *config.Notifications.SMTP[0]
	} else {
		config.Notifications.SMTP[0] = &smtp
	}

	set := func(key string, dst *string, normalize func(string) string) {
		if value := os.Getenv(key); value != "" {
			*dst = normalize(value)
		}
	}
	set("STICK_NOTIFICATIONS_SMTP_HOST", &smtp.Host, strings.TrimSpace)
	set("STICK_NOTIFICATIONS_SMTP_TLS_MODE", &smtp.TLSMode, func(value string) string {
		return strings.ToLower(strings.TrimSpace(value))
	})
	set("STICK_NOTIFICATIONS_SMTP_USERNAME", &smtp.Username, func(value string) string { return value })
	set("STICK_NOTIFICATIONS_SMTP_PASSWORD", &smtp.Password, func(value string) string { return value })
	set("STICK_NOTIFICATIONS_SMTP_FROM", &smtp.From, strings.TrimSpace)
	set("STICK_NOTIFICATIONS_SMTP_SUBJECT", &smtp.Subject, func(value string) string { return value })
	set("STICK_NOTIFICATIONS_SMTP_BODY", &smtp.Body, func(value string) string { return value })
	if value := os.Getenv("STICK_NOTIFICATIONS_SMTP_PORT"); value != "" {
		port, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid STICK_NOTIFICATIONS_SMTP_PORT: %w", err)
		}
		smtp.Port = port
	}
	config.Notifications.SMTP[0] = &smtp
	return nil
}
