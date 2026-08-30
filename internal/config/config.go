// Package config loads and validates application configuration.
package config

import (
	"time"

	"stick/internal/publicurl"
)

// Database driver names identify the configured persistence backend.
const (
	DatabaseDriverSQLite   = "sqlite"
	DatabaseDriverPostgres = "postgres"
	DatabaseDriverMongoDB  = "mongodb"
)

// ServerConfig contains the HTTP server's deployment settings.
type ServerConfig struct {
	PublicURL  publicurl.URL
	ListenAddr string
}

// DatabaseConfig is the selected persistence backend and its connection
// string. SQLite uses a filesystem path; PostgreSQL and MongoDB use URLs.
type DatabaseConfig struct {
	Driver string
	DSN    string
}

// AuthConfig contains authentication and authorization settings.
type AuthConfig struct {
	IDPEndpoint string
	Audience    string
	Scope       string
	AdminEmails []string
}

// SMTPConfig holds SMTP connection settings and optional email templates.
type SMTPConfig struct {
	Host     string
	Port     int
	TLSMode  string
	Username string
	Password string
	From     string
	Subject  string
	Body     string
}

// WebhookConfig holds webhook delivery settings.
type WebhookConfig struct {
	URL string
}

// TeamsConfig holds Microsoft Teams incoming webhook settings.
type TeamsConfig struct {
	URL string
}

// NotificationsConfig contains the notification backends enabled for the
// application. A notification is sent to every configured backend.
type NotificationsConfig struct {
	SMTP    []*SMTPConfig
	Webhook []*WebhookConfig
	Teams   []*TeamsConfig
}

// Config is the normalized application configuration returned by Load.
// Providers populate a Config value before Load applies final defaults and
// validation.
type Config struct {
	Server        ServerConfig
	Database      DatabaseConfig
	Auth          AuthConfig
	Timezone      *time.Location
	Notifications NotificationsConfig
}
