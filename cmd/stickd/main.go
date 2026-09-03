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
	"syscall"
	"time"
	_ "time/tzdata"

	"stick/internal/adapters/notification/smtp"
	"stick/internal/adapters/notification/teams"
	"stick/internal/adapters/notification/webhook"
	"stick/internal/adapters/persistence/mongodb"
	"stick/internal/adapters/persistence/postgres"
	"stick/internal/adapters/persistence/sqlite"
	"stick/internal/application"
	"stick/internal/config"
	"stick/internal/outbox"
	"stick/internal/web"
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
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}

	cfg, err := config.Load(parent,
		config.YAMLProvider{Path: *configPath},
		config.EnvironmentProvider{},
	)
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

	webRunner, err := web.NewRunner(service, store, web.Options{
		PublicURL:            cfg.Server.PublicURL,
		ListenAddr:           cfg.Server.ListenAddr,
		OIDC:                 cfg.Auth.OIDC,
		SessionSecret:        cfg.Auth.SessionSecret,
		AdminEmails:          cfg.Auth.AdminEmails,
		Timezone:             cfg.Timezone,
		NotificationsEnabled: notifier != nil,
	})
	if err != nil {
		return fmt.Errorf("web: %w", err)
	}

	components := []application.Component{webRunner}
	if notifier != nil {
		components = append(components, outbox.NewWorker(store, notifier, outbox.WorkerOptions{
			BaseURL:  cfg.Server.PublicURL.String(),
			Location: cfg.Timezone,
		}))
	}
	return application.Run(shutdown, components...)
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

func buildNotifier(cfg config.NotificationsConfig) (application.Notifier, error) {
	var notifiers []application.Notifier
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
		notifiers = append(notifiers, application.Named(notificationBackendName("smtp", i), notifier))
	}
	for i, webhookConfig := range cfg.Webhook {
		notifier, err := webhook.New(webhook.Config{URL: webhookConfig.URL})
		if err != nil {
			return nil, fmt.Errorf("%s notifier: %w", notificationBackendName("webhook", i), err)
		}
		notifiers = append(notifiers, application.Named(notificationBackendName("webhook", i), notifier))
	}
	for i, teamsConfig := range cfg.Teams {
		notifier, err := teams.New(teams.Config{URL: teamsConfig.URL})
		if err != nil {
			return nil, fmt.Errorf("%s notifier: %w", notificationBackendName("teams", i), err)
		}
		notifiers = append(notifiers, application.Named(notificationBackendName("teams", i), notifier))
	}
	return application.Multi(notifiers...), nil
}

func notificationBackendName(kind string, index int) string {
	if index == 0 {
		return kind
	}
	return fmt.Sprintf("%s[%d]", kind, index)
}
