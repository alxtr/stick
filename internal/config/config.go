// Package config loads and validates application configuration.
package config

import (
	"time"

	"stick/internal/auth"
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
	OIDC          auth.OIDCConfig
	SessionSecret []byte
	AdminEmails   []string
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

// Config is the normalized application configuration.
type Config struct {
	Server        ServerConfig
	Database      DatabaseConfig
	Auth          AuthConfig
	Timezone      *time.Location
	Notifications NotificationsConfig
}

// rawConfig and its nested types are the YAML representation. Keeping these
// separate from Config prevents unvalidated source values from escaping the
// configuration boundary.
type rawConfig struct {
	Server        rawServerConfig        `yaml:"server"`
	Database      string                 `yaml:"database"`
	Auth          rawAuthConfig          `yaml:"auth"`
	Timezone      string                 `yaml:"timezone"`
	Notifications rawNotificationsConfig `yaml:"notifications"`
}

type rawServerConfig struct {
	PublicURL  string `yaml:"public_url"`
	ListenAddr string `yaml:"listen_addr"`
}

type rawAuthConfig struct {
	OIDC          rawOIDCConfig `yaml:"oidc"`
	SessionSecret string        `yaml:"session_secret"`
	AdminEmails   []string      `yaml:"admin_emails"`
}

type rawOIDCConfig struct {
	Issuer       string `yaml:"issuer"`
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
}

type rawNotificationsConfig struct {
	SMTP    rawSMTPConfigs    `yaml:"smtp"`
	Webhook rawWebhookConfigs `yaml:"webhook"`
	Teams   rawTeamsConfigs   `yaml:"teams"`
}

type rawSMTPConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	TLSMode  string `yaml:"tls_mode"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	From     string `yaml:"from"`
	Subject  string `yaml:"subject"`
	Body     string `yaml:"body"`
}

type rawWebhookConfig struct {
	URL string `yaml:"url"`
}

type rawTeamsConfig struct {
	URL string `yaml:"url"`
}
