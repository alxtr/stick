package sqlite

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

const latestSchemaVersion = 1

func TestOpenMigratesFreshDatabase(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	conn := store.db

	var count int
	if err := conn.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN
		('sticks','sessions','subscriptions','notification_outbox')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("application tables = %d, want 4", count)
	}
	assertMigrationHistory(t, conn)
}

func TestOpenRejectsNewerSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(migrationHistorySchema + `
		INSERT INTO schema_migrations (version, name, applied_at)
		VALUES (99, '099_future.sql', CURRENT_TIMESTAMP);
		CREATE TABLE future_data (value TEXT);`); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	opened, err := Open(path)
	if opened != nil {
		opened.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("Open error = %v, want newer schema rejection", err)
	}
	check, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var exists int
	if err := check.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='future_data'`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists != 1 {
		t.Fatal("newer database was modified")
	}
}

func TestOpenRejectsNoncontiguousMigrationHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gap.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(migrationHistorySchema + `
		INSERT INTO schema_migrations (version, name, applied_at)
		VALUES (2, '002_notification_outbox.sql', CURRENT_TIMESTAMP);`); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	opened, err := Open(path)
	if opened != nil {
		opened.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("Open error = %v, want noncontiguous history rejection", err)
	}
}

func TestOpenRejectsMigrationNameMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "renamed.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(migrationHistorySchema + `
		INSERT INTO schema_migrations (version, name, applied_at)
		VALUES (1, '001_different.sql', CURRENT_TIMESTAMP);`); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	opened, err := Open(path)
	if opened != nil {
		opened.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "want \"001_baseline.sql\"") {
		t.Fatalf("Open error = %v, want migration name mismatch rejection", err)
	}
}

func assertMigrationHistory(t *testing.T, conn *sql.DB) {
	t.Helper()
	rows, err := conn.Query(`SELECT version, name, applied_at IS NOT NULL FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	wantNames := []string{"001_baseline.sql"}
	seen := 0
	for rows.Next() {
		var version, hasTimestamp int
		var name string
		if err := rows.Scan(&version, &name, &hasTimestamp); err != nil {
			t.Fatal(err)
		}
		if version != seen+1 || version > latestSchemaVersion || name != wantNames[seen] || hasTimestamp != 1 {
			t.Fatalf("migration row %d = version %d, name %q, timestamp %d", seen, version, name, hasTimestamp)
		}
		seen++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen != latestSchemaVersion {
		t.Fatalf("migration history rows = %d, want %d", seen, latestSchemaVersion)
	}
}

const migrationHistorySchema = `
CREATE TABLE schema_migrations (
    version INTEGER PRIMARY KEY CHECK (version > 0),
    name TEXT NOT NULL,
    applied_at DATETIME NOT NULL
);`
