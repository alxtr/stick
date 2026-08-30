package postgres

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"stick/test/testsupport/postgrestest"
)

func TestOpenDoesNotExposeDatabaseURL(t *testing.T) {
	const databaseURL = "postgres://user:super-secret-password@%gh/database"
	_, err := Open(context.Background(), databaseURL)
	if err == nil {
		t.Fatal("invalid database URL unexpectedly opened")
	}
	if strings.Contains(err.Error(), databaseURL) || strings.Contains(err.Error(), "super-secret-password") {
		t.Fatalf("Open exposed database URL credentials: %v", err)
	}
}

func TestClaimNotificationSkipsLockedRows(t *testing.T) {
	config := isolatedConfig(t)
	store, err := openConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `INSERT INTO sticks (id, name, version) VALUES ('outbox', 'Outbox', 1)`); err != nil {
		t.Fatal(err)
	}
	for _, recipient := range []string{"first", "second"} {
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO notification_outbox (
				stick_id, stick_name, holder_name, holder_email, held_since, released_at,
				recipient_sub, recipient_name, recipient_email, subscription_generation_token,
				status, attempts, next_attempt_at, created_at
			) VALUES ('outbox', 'Outbox', 'Holder', 'holder@example.com', now(), now(),
				$1, $1, $1 || '@example.com', $1 || '-generation', 'pending', 0, now(), now())`, recipient); err != nil {
			t.Fatal(err)
		}
	}

	locker, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Rollback()
	var lockedID int64
	if err := locker.QueryRowContext(ctx, `
		SELECT id FROM notification_outbox ORDER BY id FOR UPDATE LIMIT 1`).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}
	claimCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	delivery, err := store.ClaimNotification(claimCtx, time.Now().UTC(), time.Now().Add(-time.Hour), "skip-locked-worker")
	if err != nil {
		t.Fatal(err)
	}
	if delivery == nil || delivery.ID == lockedID || delivery.RecipientSub != "second" {
		t.Fatalf("claim while row %d locked = %+v, want second row", lockedID, delivery)
	}
}

func TestMigrationsUseLogicalVersionsAndPostgresTypes(t *testing.T) {
	config := isolatedConfig(t)
	store, err := openConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	rows, err := store.db.Query(`SELECT version, name FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	wantNames := []string{"001_baseline.sql"}
	for i, wantName := range wantNames {
		if !rows.Next() {
			t.Fatalf("missing migration %d", i+1)
		}
		var version int64
		var name string
		if err := rows.Scan(&version, &name); err != nil {
			t.Fatal(err)
		}
		if version != int64(i+1) || name != wantName {
			t.Fatalf("migration = (%d, %q), want (%d, %q)", version, name, i+1, wantName)
		}
	}
	if rows.Next() {
		t.Fatal("unexpected extra migration")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	assertColumnType(t, store.db, "sticks", "version", "bigint")
	assertColumnType(t, store.db, "sticks", "claimed_at", "timestamp with time zone")
	assertColumnType(t, store.db, "sessions", "id", "bigint")
	var identity string
	if err := store.db.QueryRow(`
		SELECT identity_generation FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='sessions' AND column_name='id'`).Scan(&identity); err != nil {
		t.Fatal(err)
	}
	if identity != "ALWAYS" {
		t.Fatalf("sessions.id identity generation = %q, want ALWAYS", identity)
	}
}

func TestBaselineUsesGenerationTokenAsOnlySubscriptionGenerationMetadata(t *testing.T) {
	store, err := openConfig(context.Background(), isolatedConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `INSERT INTO sticks (id, name) VALUES ('existing', 'Existing')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO subscriptions (stick_id, user_sub, user_name, user_email, generation_token)
		VALUES ('existing', 'watcher', 'Watcher', 'watcher@example.com', 'generation-one')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO subscriptions (stick_id, user_sub, user_name, user_email, generation_token)
		VALUES ('existing', 'other', 'Other', 'other@example.com', 'generation-one')`); err == nil {
		t.Fatal("duplicate generation token unexpectedly succeeded")
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE subscriptions SET generation_token=''`); err == nil {
		t.Fatal("generation token constraint accepted an empty value")
	}
	var removedColumns int
	if err := store.db.QueryRowContext(ctx, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema=current_schema()
		  AND ((table_name='subscriptions' AND column_name IN ('created_at', 'version'))
		    OR (table_name='notification_outbox' AND column_name IN ('subscription_created_at', 'subscription_version')))`).Scan(&removedColumns); err != nil {
		t.Fatal(err)
	}
	if removedColumns != 0 {
		t.Fatalf("baseline retained %d obsolete subscription generation columns", removedColumns)
	}
}

func TestMigrationWaitHonorsContextCancellation(t *testing.T) {
	config, lockDB := isolatedDB(t)
	waitingDB := stdlib.OpenDB(*config)
	t.Cleanup(func() { _ = waitingDB.Close() })

	ctx := context.Background()
	locker, err := lockDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Rollback()
	if _, err := locker.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockKey); err != nil {
		t.Fatal(err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = migrate(waitCtx, waitingDB)
	if err == nil || !strings.Contains(err.Error(), "acquire migration lock") {
		t.Fatalf("migrate while lock held error = %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("canceled migration took %s", time.Since(started))
	}
}

func TestConcurrentMigrationStartupIsSerialized(t *testing.T) {
	config, firstDB := isolatedDB(t)
	secondDB := stdlib.OpenDB(*config)
	t.Cleanup(func() { _ = secondDB.Close() })

	start := make(chan struct{})
	errs := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, db := range []*sql.DB{firstDB, secondDB} {
		go func(db *sql.DB) {
			ready.Done()
			<-start
			errs <- migrate(context.Background(), db)
		}(db)
	}
	ready.Wait()
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent migrate: %v", err)
		}
	}
	var count int
	if err := firstDB.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(migrations) {
		t.Fatalf("migration history count = %d, want %d", count, len(migrations))
	}
}

func assertColumnType(t *testing.T, db *sql.DB, table, column, want string) {
	t.Helper()
	var got string
	if err := db.QueryRow(`
		SELECT data_type FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2`, table, column).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s.%s type = %q, want %q", table, column, got, want)
	}
}

func isolatedConfig(t *testing.T) *pgx.ConnConfig {
	t.Helper()
	config, err := pgx.ParseConfig(postgrestest.IsolatedURL(t, postgrestest.URL(), "stick_test"))
	if err != nil {
		t.Fatal("Testcontainers returned an invalid isolated PostgreSQL URL")
	}
	return config
}

func isolatedDB(t *testing.T) (*pgx.ConnConfig, *sql.DB) {
	t.Helper()
	config := isolatedConfig(t)
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() { _ = db.Close() })
	return config, db
}
