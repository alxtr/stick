// Command stickd runs the Stick service.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"stick/internal/adapters/notification/smtp"
	"stick/internal/adapters/notification/teams"
	"stick/internal/adapters/notification/webhook"
	"stick/internal/adapters/persistence/mongodb"
	"stick/internal/adapters/persistence/postgres"
	"stick/internal/adapters/persistence/sqlite"
	"stick/internal/api/server"
	"stick/internal/application"
	"stick/internal/auth"
	"stick/internal/config"
	"stick/internal/notification"
	"stick/internal/outbox"
)

const version = "1.0.0"

var (
	commit    = "unknown"
	buildDate = "unknown"
)

const databaseStartupTimeout = 30 * time.Second

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := mainContext(context.Background(), os.Args[1:]); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "stickd: %v\n", err)
		os.Exit(1)
	}
}

func mainContext(parent context.Context, args []string) (err error) {
	flags := flag.NewFlagSet("stickd", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", "", "path to config YAML file (default: config.yaml in working directory)")
	configProviders := flags.String("config-providers", "", "comma-separated configuration providers (default: yaml,environment)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}

	providers, err := buildConfigProviders(*configPath, *configProviders)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	cfg, err := config.Load(parent, providers...)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	slog.InfoContext(parent, "application starting", "version", version, "commit", commit, "build_date", buildDate)

	notifier, err := buildNotifier(cfg.Notifications)
	if err != nil {
		return err
	}

	shutdown, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	startupCtx, startupCancel := context.WithTimeout(shutdown, databaseStartupTimeout)
	store, err := openStore(startupCtx, cfg.Database)
	startupCancel()
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close db: %w", closeErr))
		}
	}()
	service := application.NewService(store)

	serverRunner, err := server.NewRunner(service, store, server.Options{
		PublicURL:            cfg.Server.PublicURL,
		ListenAddr:           cfg.Server.ListenAddr,
		JWT:                  auth.JWTConfig{Endpoint: cfg.Auth.IDPEndpoint, Audience: cfg.Auth.Audience, Scope: cfg.Auth.Scope},
		AdminEmails:          cfg.Auth.AdminEmails,
		NotificationsEnabled: notifier != nil,
	})
	if err != nil {
		return fmt.Errorf("HTTP server: %w", err)
	}

	components := []application.Component{serverRunner}
	if notifier != nil {
		components = append(components, outbox.NewWorker(store, notifier, outbox.WorkerOptions{
			BaseURL:  cfg.Server.PublicURL,
			Location: cfg.Timezone,
		}))
	}
	return application.Run(shutdown, components...)
}

const defaultConfigProviders = "yaml,environment"

func buildConfigProviders(configPath, selection string) ([]config.Provider, error) {
	selection = strings.TrimSpace(selection)
	if selection == "" {
		selection = strings.TrimSpace(os.Getenv("STICK_CONFIG_PROVIDERS"))
	}
	if selection == "" {
		selection = defaultConfigProviders
	}

	var providers []config.Provider
	hasYAML := false
	for _, name := range strings.Split(selection, ",") {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			return nil, fmt.Errorf("configuration provider list contains an empty provider name")
		}
		switch name {
		case "yaml":
			hasYAML = true
			providers = append(providers, config.YAMLProvider{Path: configPath})
		case "environment":
			providers = append(providers, config.EnvironmentProvider{})
		case "azure-app-config":
			providers = append(providers, config.AzureAppConfigurationProvider{
				Endpoint:  os.Getenv("STICK_AZURE_APPCONFIG_ENDPOINT"),
				Label:     os.Getenv("STICK_AZURE_APPCONFIG_LABEL"),
				KeyPrefix: os.Getenv("STICK_AZURE_APPCONFIG_KEY_PREFIX"),
				KeyFilter: os.Getenv("STICK_AZURE_APPCONFIG_KEY_FILTER"),
				Separator: os.Getenv("STICK_AZURE_APPCONFIG_SEPARATOR"),
			})
		default:
			return nil, fmt.Errorf("unknown configuration provider %q", name)
		}
	}
	if configPath != "" && !hasYAML {
		return nil, fmt.Errorf("-config requires the yaml configuration provider")
	}
	return providers, nil
}

type backend interface {
	application.Store
	outbox.Store
	PingContext(context.Context) error
	Close() error
}

func openStore(ctx context.Context, cfg config.DatabaseConfig) (backend, error) {
	switch cfg.Driver {
	case config.DatabaseDriverSQLite:
		return sqlite.Open(cfg.DSN)
	case config.DatabaseDriverPostgres:
		return postgres.Open(ctx, cfg.DSN)
	case config.DatabaseDriverMongoDB:
		return mongodb.Open(ctx, cfg.DSN)
	default:
		return nil, fmt.Errorf("unsupported database driver %q", cfg.Driver)
	}
}

func buildNotifier(cfg config.NotificationsConfig) (notification.Notifier, error) {
	var notifiers []notification.Notifier
	for i, smtpConfig := range cfg.SMTP {
		notifier, err := smtp.New(smtp.Config{
			Host:     smtpConfig.Host,
			Port:     smtpConfig.Port,
			TLSMode:  smtpConfig.TLSMode,
			Username: smtpConfig.Username,
			Password: smtpConfig.Password,
			From:     smtpConfig.From,
		}, smtp.Templates{
			Subject: smtpConfig.Subject,
			Body:    smtpConfig.Body,
		})
		if err != nil {
			return nil, fmt.Errorf("%s notifier: %w", notificationBackendName("smtp", i), err)
		}
		notifiers = append(notifiers, notification.Named(notificationBackendName("smtp", i), notifier))
	}
	for i, webhookConfig := range cfg.Webhook {
		notifier, err := webhook.New(webhook.Config{URL: webhookConfig.URL})
		if err != nil {
			return nil, fmt.Errorf("%s notifier: %w", notificationBackendName("webhook", i), err)
		}
		notifiers = append(notifiers, notification.Named(notificationBackendName("webhook", i), notifier))
	}
	for i, teamsConfig := range cfg.Teams {
		notifier, err := teams.New(teams.Config{URL: teamsConfig.URL})
		if err != nil {
			return nil, fmt.Errorf("%s notifier: %w", notificationBackendName("teams", i), err)
		}
		notifiers = append(notifiers, notification.Named(notificationBackendName("teams", i), notifier))
	}
	return notification.Multi(notifiers...), nil
}

func notificationBackendName(kind string, index int) string {
	if index == 0 {
		return kind
	}
	return fmt.Sprintf("%s[%d]", kind, index)
}
