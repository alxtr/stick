// Package mongotest provisions a MongoDB replica set for tests.
package mongotest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	mongodbcontainer "github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	containerImage = "mongo:7"
	replicaSetName = "rs0"
)

var databaseURL string
var databaseSequence atomic.Uint64

// Run provisions a single-node MongoDB replica set, executes the tests, and
// removes the container afterward.
func Run(m *testing.M) int {
	return RunWith(m.Run)
}

// RunWith provisions MongoDB, invokes run, and removes the container
// afterward. It is useful when a test package also provisions another service.
func RunWith(run func() int) int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := mongodbcontainer.Run(ctx, containerImage, mongodbcontainer.WithReplicaSet(replicaSetName))
	if err != nil {
		fmt.Fprintf(os.Stderr, "start MongoDB test container: %v\n", err)
		terminateAfterSetupFailure(container)
		return 1
	}

	databaseURL, err = container.ConnectionString(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "get MongoDB test container URL: %v\n", err)
		terminateAfterSetupFailure(container)
		return 1
	}

	code := run()
	if err := terminate(container); err != nil {
		fmt.Fprintf(os.Stderr, "terminate MongoDB test container: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	return code
}

// Start starts a MongoDB replica set for an individual test and registers
// cleanup for the container.
func Start(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := mongodbcontainer.Run(ctx, containerImage, mongodbcontainer.WithReplicaSet(replicaSetName))
	if err != nil {
		terminateAfterSetupFailure(container)
		t.Fatalf("start MongoDB test container: %v", err)
	}
	t.Cleanup(func() {
		if err := terminate(container); err != nil {
			t.Errorf("terminate MongoDB test container: %v", err)
		}
	})

	endpoint, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatal("get MongoDB test container URL:", err)
	}
	return endpoint
}

// URL returns the connection URL for the container provisioned by Run.
func URL() string {
	if databaseURL == "" {
		panic("mongotest.Run must provision MongoDB before URL is called")
	}
	return databaseURL
}

// IsolatedURL returns a connection URL for a unique database and drops that
// database when the test finishes. Databases provide isolation without
// requiring a separate MongoDB container for every test.
func IsolatedURL(t *testing.T, baseURL, prefix string) string {
	t.Helper()
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "mongodb" && parsed.Scheme != "mongodb+srv") || parsed.Hostname() == "" {
		t.Fatal("invalid MongoDB test URL")
	}

	databaseName := fmt.Sprintf("%s_%d_%d", prefix, os.Getpid(), databaseSequence.Add(1))
	parsed.Path = "/" + databaseName
	parsed.RawPath = ""
	isolatedURL := parsed.String()

	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(baseURL))
	if err != nil {
		t.Fatal("connect MongoDB test client:", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.Database(databaseName).Drop(cleanupCtx); err != nil {
			t.Errorf("drop MongoDB test database %q: %v", databaseName, err)
		}
		if err := client.Disconnect(cleanupCtx); err != nil {
			t.Errorf("disconnect MongoDB test client: %v", err)
		}
	})
	return isolatedURL
}

func terminateAfterSetupFailure(container *mongodbcontainer.MongoDBContainer) {
	if container == nil {
		return
	}
	if err := terminate(container); err != nil {
		fmt.Fprintf(os.Stderr, "terminate MongoDB test container after setup failure: %v\n", err)
	}
}

func terminate(container *mongodbcontainer.MongoDBContainer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return container.Terminate(ctx)
}
