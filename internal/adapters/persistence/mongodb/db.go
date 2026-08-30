// Package mongodb implements the application's persistence ports with MongoDB.
//
// MongoDB must be configured as a replica set (including a single-node replica
// set) or accessed through mongos. Stick relies on multi-document transactions
// for its application-level atomicity guarantees.
package mongodb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

const (
	startupPingTimeout = 5 * time.Second

	sticksCollection        = "sticks"
	sessionsCollection      = "sessions"
	subscriptionsCollection = "subscriptions"
	outboxCollection        = "notification_outbox"
	migrationsCollection    = "schema_migrations"
	countersCollection      = "counters"
)

type migration struct {
	version int64
	name    string
}

// MongoDB schema changes are represented by idempotent collection/index
// initialization rather than SQL files. Keep the history record so an
// incompatible future schema cannot be silently used with this binary.
var migrations = []migration{{version: 1, name: "001_baseline"}}

type migrationDocument struct {
	Version   int64     `bson:"_id"`
	Name      string    `bson:"name"`
	AppliedAt time.Time `bson:"applied_at"`
}

// Open connects to MongoDB, verifies connectivity with a bounded ping, and
// initializes the collections and indexes required by the application. URI
// parsing errors are deliberately sanitized because MongoDB URIs commonly
// contain credentials.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("MongoDB DSN is required")
	}

	dsn := strings.TrimSpace(databaseURL)
	databaseName, err := databaseNameFromURL(dsn)
	if err != nil {
		return nil, errors.New("invalid MongoDB DSN")
	}

	clientOptions := options.Client().ApplyURI(dsn)
	if err := clientOptions.Validate(); err != nil {
		return nil, errors.New("invalid MongoDB DSN")
	}
	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("connect MongoDB: %w", ctx.Err())
		}
		return nil, errors.New("connect MongoDB database")
	}

	pingCtx, cancel := context.WithTimeout(ctx, startupPingTimeout)
	defer cancel()
	if err := client.Ping(pingCtx, readpref.Primary()); err != nil {
		_ = disconnect(client)
		return nil, fmt.Errorf("ping MongoDB database: %w", err)
	}

	store := &Store{client: client, db: client.Database(databaseName)}
	if err := verifyTransactionDeployment(ctx, store.db); err != nil {
		_ = disconnect(client)
		return nil, fmt.Errorf("verify MongoDB deployment: %w", err)
	}
	if err := store.initialize(ctx); err != nil {
		_ = disconnect(client)
		return nil, fmt.Errorf("initialize MongoDB database: %w", err)
	}
	return store, nil
}

func verifyTransactionDeployment(ctx context.Context, db *mongo.Database) error {
	var hello struct {
		SetName string `bson:"setName"`
		Message string `bson:"msg"`
	}
	if err := db.RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		return fmt.Errorf("check transaction support: %w", err)
	}
	if hello.SetName == "" && hello.Message != "isdbgrid" {
		return errors.New("MongoDB must be a replica set or mongos for transactions")
	}
	return nil
}

func databaseNameFromURL(databaseURL string) (string, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil || parsed.Hostname() == "" {
		return "", errors.New("invalid MongoDB URL")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "mongodb" && scheme != "mongodb+srv" {
		return "", errors.New("invalid MongoDB URL scheme")
	}
	path := strings.Trim(parsed.EscapedPath(), "/")
	if path == "" {
		return "", errors.New("MongoDB database name is required")
	}
	name, err := url.PathUnescape(path)
	if err != nil || name == "" || strings.Contains(name, "/") {
		return "", errors.New("invalid MongoDB database name")
	}
	return name, nil
}

func (s *Store) initialize(ctx context.Context) error {
	if err := validateMigrations(migrations); err != nil {
		return err
	}
	if err := ensureCollections(ctx, s.db); err != nil {
		return err
	}
	if err := ensureIndexes(ctx, s.db); err != nil {
		return err
	}
	return ensureMigrationHistory(ctx, s.db)
}

func ensureCollections(ctx context.Context, db *mongo.Database) error {
	for _, name := range []string{
		sticksCollection,
		sessionsCollection,
		subscriptionsCollection,
		outboxCollection,
		migrationsCollection,
		countersCollection,
	} {
		if err := db.CreateCollection(ctx, name); err != nil && !isNamespaceExists(err) {
			return fmt.Errorf("create collection %q: %w", name, err)
		}
	}
	return nil
}

func ensureIndexes(ctx context.Context, db *mongo.Database) error {
	// A boolean is used instead of a partial-filter $exists expression. MongoDB
	// does not allow $exists:false in partial index filters because it is
	// represented internally as $not.
	activeSession := bson.D{{Key: "active", Value: true}}
	completedSession := bson.D{{Key: "released_at", Value: bson.D{{Key: "$exists", Value: true}}}}
	indexes := []struct {
		collection string
		models     []mongo.IndexModel
	}{
		{sessionsCollection, []mongo.IndexModel{
			{Keys: bson.D{{Key: "stick_id", Value: 1}}, Options: options.Index().SetName("idx_sessions_stick")},
			{Keys: bson.D{{Key: "stick_id", Value: 1}}, Options: options.Index().SetName("idx_sessions_one_active_per_stick").SetUnique(true).SetPartialFilterExpression(activeSession)},
			{Keys: bson.D{{Key: "stick_id", Value: 1}, {Key: "claimed_at", Value: -1}}, Options: options.Index().SetName("idx_sessions_completed_history").SetPartialFilterExpression(completedSession)},
		}},
		{subscriptionsCollection, []mongo.IndexModel{
			{Keys: bson.D{{Key: "stick_id", Value: 1}, {Key: "user_sub", Value: 1}}, Options: options.Index().SetName("idx_subscriptions_stick_user").SetUnique(true)},
			{Keys: bson.D{{Key: "generation_token", Value: 1}}, Options: options.Index().SetName("idx_subscriptions_generation").SetUnique(true)},
			{Keys: bson.D{{Key: "user_sub", Value: 1}, {Key: "stick_id", Value: 1}}, Options: options.Index().SetName("idx_subscriptions_user_stick")},
		}},
		{outboxCollection, []mongo.IndexModel{
			{Keys: bson.D{{Key: "status", Value: 1}, {Key: "next_attempt_at", Value: 1}, {Key: "_id", Value: 1}}, Options: options.Index().SetName("idx_notification_outbox_ready")},
			{Keys: bson.D{{Key: "status", Value: 1}, {Key: "claimed_at", Value: 1}, {Key: "_id", Value: 1}}, Options: options.Index().SetName("idx_notification_outbox_stale")},
		}},
	}
	for _, group := range indexes {
		if _, err := db.Collection(group.collection).Indexes().CreateMany(ctx, group.models); err != nil {
			return fmt.Errorf("create %s indexes: %w", group.collection, err)
		}
	}
	return nil
}

func ensureMigrationHistory(ctx context.Context, db *mongo.Database) error {
	collection := db.Collection(migrationsCollection)
	version, err := validateMigrationHistory(ctx, collection)
	if err != nil {
		return err
	}
	for _, migration := range migrations {
		if migration.version <= version {
			continue
		}
		_, err := collection.InsertOne(ctx, migrationDocument{
			Version:   migration.version,
			Name:      migration.name,
			AppliedAt: time.Now().UTC(),
		})
		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				version, err = validateMigrationHistory(ctx, collection)
				if err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("record migration %03d %s: %w", migration.version, migration.name, err)
		}
		version = migration.version
	}
	return nil
}

func validateMigrations(items []migration) error {
	if len(items) == 0 {
		return errors.New("no MongoDB migrations found")
	}
	for i, item := range items {
		if item.version != int64(i+1) {
			return fmt.Errorf("migration versions must be contiguous: got %d, want %d", item.version, i+1)
		}
		if item.name == "" {
			return fmt.Errorf("migration %d must have a name", item.version)
		}
	}
	return nil
}

func validateMigrationHistory(ctx context.Context, collection *mongo.Collection) (int64, error) {
	cursor, err := collection.Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return 0, fmt.Errorf("read migration history: %w", err)
	}
	defer cursor.Close(ctx)

	var applied int64
	for cursor.Next(ctx) {
		var item migrationDocument
		if err := cursor.Decode(&item); err != nil {
			return 0, fmt.Errorf("read migration history: %w", err)
		}
		if item.Version != applied+1 || item.Version > int64(len(migrations)) {
			return 0, fmt.Errorf("unsupported database migration version %d", item.Version)
		}
		if item.Name != migrations[item.Version-1].name {
			return 0, fmt.Errorf("database migration %d is %q, want %q", item.Version, item.Name, migrations[item.Version-1].name)
		}
		applied = item.Version
	}
	if err := cursor.Err(); err != nil {
		return 0, fmt.Errorf("read migration history: %w", err)
	}
	return applied, nil
}

func isNamespaceExists(err error) bool {
	var commandErr mongo.CommandError
	return errors.As(err, &commandErr) && commandErr.Code == 48
}

func disconnect(client *mongo.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return client.Disconnect(ctx)
}
