// Package postgres implements the application's persistence ports with PostgreSQL.
package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const (
	startupPingTimeout        = 5 * time.Second
	migrationLockTimeout      = 5 * time.Second
	migrationStatementTimeout = 30 * time.Second
	migrationLockKey          = int64(0x746865737469636b) // stable advisory lock namespace
)

//go:embed migrations/001_baseline.sql
var migration001 string

type migration struct {
	version int64
	name    string
	sql     string
}

var migrations = []migration{
	{version: 1, name: "001_baseline.sql", sql: migration001},
}

// Open connects to PostgreSQL, verifies connectivity with a bounded startup
// ping, applies pending migrations, and returns the connection-owning store.
// Errors never include databaseURL itself.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("PostgreSQL DSN is required")
	}
	connConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		// pgx parse errors may quote their input, which can contain credentials.
		return nil, errors.New("invalid PostgreSQL DSN")
	}
	return openConfig(ctx, connConfig)
}

func openConfig(ctx context.Context, connConfig *pgx.ConnConfig) (*Store, error) {
	db := stdlib.OpenDB(*connConfig)
	db.SetConnMaxLifetime(time.Hour)
	db.SetMaxIdleConns(5)
	db.SetMaxOpenConns(20)

	pingCtx, cancel := context.WithTimeout(ctx, startupPingTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping PostgreSQL database: %w", err)
	}
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate PostgreSQL database: %w", err)
	}
	return &Store{db: db}, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	if err := validateMigrations(migrations); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		SELECT set_config('lock_timeout', $1, true),
		       set_config('statement_timeout', $2, true)`,
		migrationLockTimeout.String(), migrationStatementTimeout.String()); err != nil {
		return fmt.Errorf("configure migration timeouts: %w", err)
	}

	// A transaction-scoped advisory lock serializes inspection and application
	// without leaking a session lock if startup is canceled or fails.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}

	version, err := migrationVersion(ctx, tx)
	if err != nil {
		return err
	}
	for _, migration := range migrations {
		if migration.version <= version {
			continue
		}
		if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
			return fmt.Errorf("apply migration %03d %s: %w", migration.version, migration.name, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO schema_migrations (version, name, applied_at)
			VALUES ($1, $2, $3)`, migration.version, migration.name, time.Now().UTC()); err != nil {
			return fmt.Errorf("record migration %03d %s: %w", migration.version, migration.name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration transaction: %w", err)
	}
	return nil
}

func validateMigrations(items []migration) error {
	if len(items) == 0 {
		return errors.New("no PostgreSQL migrations found")
	}
	for i, migration := range items {
		want := int64(i + 1)
		if migration.version != want {
			return fmt.Errorf("migration versions must be contiguous: got %d, want %d", migration.version, want)
		}
		if migration.name == "" || migration.sql == "" {
			return fmt.Errorf("migration %d must have a name and SQL", migration.version)
		}
	}
	return nil
}

func migrationVersion(ctx context.Context, tx *sql.Tx) (int64, error) {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&exists); err != nil {
		return 0, fmt.Errorf("inspect migration history: %w", err)
	}
	if !exists {
		return 0, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT version, name FROM schema_migrations ORDER BY version`)
	if err != nil {
		return 0, fmt.Errorf("read migration history: %w", err)
	}
	defer rows.Close()

	var applied int64
	for rows.Next() {
		var version int64
		var name string
		if err := rows.Scan(&version, &name); err != nil {
			return 0, fmt.Errorf("read migration history: %w", err)
		}
		if version != applied+1 || version > int64(len(migrations)) {
			return 0, fmt.Errorf("unsupported database migration version %d", version)
		}
		if name != migrations[version-1].name {
			return 0, fmt.Errorf("database migration %d is %q, want %q", version, name, migrations[version-1].name)
		}
		applied = version
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read migration history: %w", err)
	}
	return applied, nil
}
