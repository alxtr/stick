// Package postgrestest provisions PostgreSQL for test packages.
package postgrestest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

const (
	containerImage = "postgres:17-alpine"
	databaseName   = "stick"
	databaseUser   = "stick"
	databasePass   = "stick"
)

var databaseURL string

var schemaSequence atomic.Uint64

// Run provisions PostgreSQL with Testcontainers, executes the tests, and
// removes the container afterward.
func Run(m *testing.M) int {
	return RunWith(m.Run)
}

// RunWith provisions PostgreSQL, invokes run, and removes the container
// afterward. It lets a test package provision more than one service while
// keeping each container's cleanup scoped to its owner.
func RunWith(run func() int) int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := postgrescontainer.Run(
		ctx,
		containerImage,
		postgrescontainer.WithDatabase(databaseName),
		postgrescontainer.WithUsername(databaseUser),
		postgrescontainer.WithPassword(databasePass),
		postgrescontainer.BasicWaitStrategies(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start PostgreSQL test container: %v\n", err)
		terminateAfterSetupFailure(container)
		return 1
	}

	databaseURL, err = container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "get PostgreSQL test container URL: %v\n", err)
		terminateAfterSetupFailure(container)
		return 1
	}

	code := run()
	if err := terminate(container); err != nil {
		fmt.Fprintf(os.Stderr, "terminate PostgreSQL test container: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	return code
}

// URL returns the connection URL for the container provisioned by Run.
func URL() string {
	if databaseURL == "" {
		panic("postgrestest.Run must provision PostgreSQL before URL is called")
	}
	return databaseURL
}

// IsolatedURL creates a schema in the test container and returns a connection
// URL whose search path targets that schema. The schema is dropped during test
// cleanup.
func IsolatedURL(t *testing.T, databaseURL, prefix string) string {
	t.Helper()
	base, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("Testcontainers returned an invalid PostgreSQL connection string")
	}
	adminDB := stdlib.OpenDB(*base)
	t.Cleanup(func() { _ = adminDB.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := adminDB.PingContext(ctx); err != nil {
		t.Fatalf("connect to PostgreSQL test container: %v", err)
	}

	schema := fmt.Sprintf("%s_%d_%d", prefix, os.Getpid(), schemaSequence.Add(1))
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+identifier); err != nil {
		t.Fatalf("create isolated PostgreSQL schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := adminDB.ExecContext(cleanupCtx, `DROP SCHEMA `+identifier+` CASCADE`); err != nil {
			t.Errorf("drop isolated PostgreSQL schema: %v", err)
		}
	})

	parsed, err := url.Parse(databaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Fatal("Testcontainers returned an invalid PostgreSQL URL")
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func terminateAfterSetupFailure(container *postgrescontainer.PostgresContainer) {
	if container == nil {
		return
	}
	if err := terminate(container); err != nil {
		fmt.Fprintf(os.Stderr, "terminate PostgreSQL test container after setup failure: %v\n", err)
	}
}

func terminate(container *postgrescontainer.PostgresContainer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return container.Terminate(ctx)
}
