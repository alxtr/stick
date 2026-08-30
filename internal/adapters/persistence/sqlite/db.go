// Package sqlite implements the application's persistence ports with SQLite.
package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/001_baseline.sql
var migration001 string

type migration struct {
	version int
	name    string
	sql     string
}

// Add future migrations here in version order after embedding their SQL above.
var migrations = []migration{
	{version: 1, name: "001_baseline.sql", sql: migration001},
}

// Open opens (or creates) the SQLite database, applies pending migrations, and
// returns the store that owns its connection.
func Open(path string) (*Store, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	conn.SetMaxOpenConns(1)
	for _, pragma := range []string{"PRAGMA busy_timeout = 5000", "PRAGMA foreign_keys = ON"} {
		if _, err := conn.Exec(pragma); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("configure database: %w", err)
		}
	}
	if err := migrate(context.Background(), conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return &Store{db: conn}, nil
}

func migrate(ctx context.Context, conn *sql.DB) error {
	if err := validateMigrations(migrations); err != nil {
		return err
	}
	hasHistory, err := tableExists(ctx, conn, "schema_migrations")
	if err != nil {
		return fmt.Errorf("inspect migration history: %w", err)
	}
	version := 0
	if hasHistory {
		version, err = validateMigrationHistory(ctx, conn, migrations)
		if err != nil {
			return err
		}
	}
	for _, migration := range migrations {
		if migration.version <= version {
			continue
		}
		if err := applyMigration(ctx, conn, migration); err != nil {
			return fmt.Errorf("apply migration %03d %s: %w", migration.version, migration.name, err)
		}
	}
	return nil
}

func validateMigrations(migrations []migration) error {
	if len(migrations) == 0 {
		return errors.New("no database migrations found")
	}
	for i, migration := range migrations {
		if migration.version != i+1 {
			return fmt.Errorf("migration versions must be contiguous: got %d, want %d", migration.version, i+1)
		}
		if migration.name == "" || migration.sql == "" {
			return fmt.Errorf("migration %d must have a name and SQL", migration.version)
		}
	}
	return nil
}

func applyMigration(ctx context.Context, conn *sql.DB, migration migration) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
		return fmt.Errorf("execute migration SQL: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		migration.version, migration.name, time.Now().UTC()); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration transaction: %w", err)
	}
	return nil
}

func tableExists(ctx context.Context, conn *sql.DB, table string) (bool, error) {
	var count int
	err := conn.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check table %q: %w", table, err)
	}
	return count == 1, nil
}

func validateMigrationHistory(ctx context.Context, conn *sql.DB, migrations []migration) (int, error) {
	rows, err := conn.QueryContext(ctx, `SELECT version, name FROM schema_migrations ORDER BY version`)
	if err != nil {
		return 0, fmt.Errorf("read migration history: %w", err)
	}
	defer rows.Close()
	applied := 0
	for rows.Next() {
		var version int
		var name string
		if err := rows.Scan(&version, &name); err != nil {
			return 0, fmt.Errorf("read migration history: %w", err)
		}
		if version != applied+1 || version > len(migrations) {
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
