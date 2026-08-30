package integration_test

import (
	"context"
	"path/filepath"
	"testing"

	"stick/internal/adapters/persistence/mongodb"
	"stick/internal/adapters/persistence/postgres"
	"stick/internal/adapters/persistence/sqlite"
	"stick/test/testsupport/mongotest"
	"stick/test/testsupport/postgrestest"
)

func TestPersistenceContract(t *testing.T) {
	forEachBackend(t, Run)
}

func TestBusinessRules(t *testing.T) {
	forEachBackend(t, RunBusinessRules)
}

func forEachBackend(t *testing.T, run func(*testing.T, Factory)) {
	t.Helper()
	t.Run("SQLite", func(t *testing.T) {
		run(t, func(t *testing.T) Backend {
			backend, err := sqlite.Open(filepath.Join(t.TempDir(), "contract.db"))
			if err != nil {
				t.Fatalf("open isolated SQLite backend: %v", err)
			}
			return backend
		})
	})

	t.Run("PostgreSQL", func(t *testing.T) {
		run(t, func(t *testing.T) Backend {
			return openPostgres(t, postgrestest.IsolatedURL(t, postgrestest.URL(), "stick_integration"))
		})
	})
	t.Run("MongoDB", func(t *testing.T) {
		run(t, func(t *testing.T) Backend {
			return openMongoDB(t, mongotest.IsolatedURL(t, mongotest.URL(), "stick_integration"))
		})
	})
}

func openPostgres(t *testing.T, databaseURL string) Backend {
	t.Helper()
	backend, err := postgres.Open(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open isolated PostgreSQL backend: %v", err)
	}
	return backend
}

func openMongoDB(t *testing.T, databaseURL string) Backend {
	t.Helper()
	backend, err := mongodb.Open(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open isolated MongoDB backend: %v", err)
	}
	return backend
}
