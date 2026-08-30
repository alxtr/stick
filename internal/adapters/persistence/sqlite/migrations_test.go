package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestFailedMigrationRollsBackSchemaAndHistory(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	defer conn.Close()

	err = applyMigration(context.Background(), conn, migration{
		version: 1,
		name:    "broken.sql",
		sql:     `CREATE TABLE should_rollback (id INTEGER); INSERT INTO missing_table VALUES (1);`,
	})
	if err == nil {
		t.Fatal("broken migration unexpectedly succeeded")
	}
	var tableCount int
	if err := conn.QueryRow(`
		SELECT count(*) FROM sqlite_master
		WHERE name IN ('should_rollback', 'schema_migrations')`).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 0 {
		t.Fatalf("failed migration left %d schema or history tables", tableCount)
	}
}

func TestBaselineMigrationEnforcesHolderAndActiveSessionInvariants(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	conn := store.db
	if _, err := conn.Exec(`INSERT INTO sticks (id, name) VALUES ('aa001', 'prod')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE sticks SET holder_sub='u1' WHERE id='aa001'`); err == nil {
		t.Fatal("partial holder update unexpectedly succeeded")
	}
	if _, err := conn.Exec(`INSERT INTO sticks (id, name, holder_sub) VALUES ('bb002', 'stage', 'u1')`); err == nil {
		t.Fatal("partial holder insert unexpectedly succeeded")
	}
	if _, err := conn.Exec(`
		INSERT INTO sessions (stick_id, holder_sub, holder_name, holder_email, reason, claimed_at)
		VALUES ('aa001', 'u1', 'Alice', 'alice@example.com', 'one', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO sessions (stick_id, holder_sub, holder_name, holder_email, reason, claimed_at)
		VALUES ('aa001', 'u2', 'Bob', 'bob@example.com', 'two', CURRENT_TIMESTAMP)`); err == nil {
		t.Fatal("second active session unexpectedly succeeded")
	}
}

func TestCompletedHistoryQueryUsesMatchingIndex(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	conn := store.db
	rows, err := conn.Query(`EXPLAIN QUERY PLAN
		SELECT id, stick_id, holder_sub, holder_name, holder_email, reason, claimed_at, released_at
		FROM sessions
		WHERE stick_id = ? AND released_at IS NOT NULL
		ORDER BY claimed_at DESC
		LIMIT ? OFFSET ?`, "aa001", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan, "\n"), "idx_sessions_completed_history") {
		t.Fatalf("query plan does not use completed-history index: %v", plan)
	}
}

func TestMigrationHistoryInsertFailureRollsBackMigrationSQL(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	defer conn.Close()
	if _, err := conn.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at DATETIME NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO schema_migrations (version, name, applied_at)
		VALUES (1, 'existing.sql', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}

	err = applyMigration(context.Background(), conn, migration{
		version: 1,
		name:    "001_conflict.sql",
		sql:     `CREATE TABLE should_rollback (id INTEGER);`,
	})
	if err == nil {
		t.Fatal("migration with conflicting history unexpectedly succeeded")
	}
	var schemaCount, historyCount int
	if err := conn.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='should_rollback'`).Scan(&schemaCount); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&historyCount); err != nil {
		t.Fatal(err)
	}
	if schemaCount != 0 || historyCount != 1 {
		t.Fatalf("failed history insert left schema count %d and history count %d", schemaCount, historyCount)
	}
}

func TestBaselineStickVersionConstraint(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { conn.Close() })

	if err := applyMigration(context.Background(), conn, migrations[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO sticks (id, name) VALUES ('aa001', 'existing')`); err != nil {
		t.Fatal(err)
	}
	var version int64
	if err := conn.QueryRow(`SELECT version FROM sticks WHERE id='aa001'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("initial version = %d, want 1", version)
	}
	if _, err := conn.Exec(`UPDATE sticks SET version=0 WHERE id='aa001'`); err == nil {
		t.Fatal("version constraint accepted zero")
	}
	if _, err := conn.Exec(`UPDATE sticks SET version=1.5 WHERE id='aa001'`); err == nil {
		t.Fatal("version constraint accepted non-integer")
	}
}

func TestBaselineUsesGenerationTokenAsOnlySubscriptionGenerationMetadata(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	conn := store.db
	if _, err := conn.Exec(`INSERT INTO sticks (id, name) VALUES ('aa001', 'existing')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO subscriptions (stick_id, user_sub, user_name, user_email, generation_token)
		VALUES ('aa001', 'watcher', 'Watcher', 'watcher@example.com', 'generation-one')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO subscriptions (stick_id, user_sub, user_name, user_email, generation_token)
		VALUES ('aa001', 'other', 'Other', 'other@example.com', 'generation-one')`); err == nil {
		t.Fatal("duplicate generation token unexpectedly succeeded")
	}
	if _, err := conn.Exec(`UPDATE subscriptions SET generation_token=''`); err == nil {
		t.Fatal("generation token constraint accepted an empty value")
	}
	var removedColumns int
	if err := conn.QueryRow(`
		SELECT count(*) FROM pragma_table_info('subscriptions')
		WHERE name IN ('created_at', 'version')`).Scan(&removedColumns); err != nil {
		t.Fatal(err)
	}
	if removedColumns != 0 {
		t.Fatalf("subscriptions retained %d obsolete generation metadata columns", removedColumns)
	}
	if err := conn.QueryRow(`
		SELECT count(*) FROM pragma_table_info('notification_outbox')
		WHERE name IN ('subscription_created_at', 'subscription_version')`).Scan(&removedColumns); err != nil {
		t.Fatal(err)
	}
	if removedColumns != 0 {
		t.Fatalf("notification_outbox retained %d obsolete generation metadata columns", removedColumns)
	}
}
