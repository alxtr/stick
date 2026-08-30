package mongodb

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"

	"stick/internal/application"
	"stick/internal/core"
	"stick/internal/outbox"
)

// Store owns a MongoDB client and database and implements all persistence
// ports. The client is shared by all operations and is safe for concurrent use.
type Store struct {
	client *mongo.Client
	db     *mongo.Database
}

var (
	_ application.Store = (*Store)(nil)
	_ outbox.Store      = (*Store)(nil)
)

// PingContext verifies that the MongoDB deployment is usable.
func (s *Store) PingContext(ctx context.Context) error {
	return s.client.Ping(ctx, nil)
}

// Close disconnects the owned MongoDB client.
func (s *Store) Close() error { return disconnect(s.client) }

// WithinTransaction runs fn atomically without exposing a MongoDB session.
// The transaction object retains the session context internally. This is
// important because application callbacks receive an ordinary context and
// otherwise their writes would execute outside the transaction. MongoDB may
// retry a transaction callback after a transient transaction error, so callers
// should keep callback side effects limited to the transaction ports.
func (s *Store) WithinTransaction(ctx context.Context, fn func(application.Transaction) error) error {
	if fn == nil {
		return errors.New("transaction callback is nil")
	}
	return s.runInTransaction(ctx, func(sessionCtx mongo.SessionContext) error {
		return fn(&transaction{store: s, ctx: sessionCtx})
	})
}

func (s *Store) runInTransaction(ctx context.Context, fn func(mongo.SessionContext) error) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	session, err := s.client.StartSession()
	if err != nil {
		return fmt.Errorf("start MongoDB session: %w", err)
	}
	defer session.EndSession(context.Background())

	return mongo.WithSession(ctx, session, func(sessionCtx mongo.SessionContext) error {
		_, err := session.WithTransaction(sessionCtx, func(txCtx mongo.SessionContext) (interface{}, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, fn(txCtx)
		}, options.Transaction().SetReadPreference(readpref.Primary()).SetWriteConcern(writeconcern.Majority()))
		return err
	})
}

func (s *Store) runInSnapshot(ctx context.Context, fn func(mongo.SessionContext) error) error {
	session, err := s.client.StartSession()
	if err != nil {
		return fmt.Errorf("start MongoDB read session: %w", err)
	}
	defer session.EndSession(context.Background())

	return mongo.WithSession(ctx, session, func(sessionCtx mongo.SessionContext) error {
		_, err := session.WithTransaction(sessionCtx, func(txCtx mongo.SessionContext) (interface{}, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, fn(txCtx)
		}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetReadPreference(readpref.Primary()))
		return err
	})
}

func wrapStoreError(operation string, err error) error {
	if err == nil {
		return nil
	}
	for _, sentinel := range []error{
		application.ErrNotFound, application.ErrAlreadyExists, application.ErrVersionConflict,
		core.ErrAlreadyHeld, core.ErrNotHolder, core.ErrAlreadyArchived, core.ErrNotArchived,
		core.ErrHeld, core.ErrVersionExhausted, outbox.ErrClaimLost,
	} {
		if errors.Is(err, sentinel) {
			return sentinel
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("nil context")
	}
	return ctx.Err()
}
