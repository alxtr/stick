package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"stick/internal/application"
	"stick/internal/core"
	"stick/internal/outbox"
)

// Store owns a PostgreSQL connection pool and implements all persistence ports.
type Store struct{ db *sql.DB }

var (
	_ application.Store = (*Store)(nil)
	_ outbox.Store      = (*Store)(nil)
)

// PingContext verifies that the PostgreSQL connection is usable.
func (s *Store) PingContext(ctx context.Context) error { return s.db.PingContext(ctx) }

// Close releases the PostgreSQL connection.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) runInTransaction(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func (s *Store) runInSnapshot(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin read transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit read transaction: %w", err)
	}
	return nil
}

// WithinTransaction runs fn atomically without exposing *sql.Tx.
func (s *Store) WithinTransaction(ctx context.Context, fn func(application.Transaction) error) error {
	return s.runInTransaction(ctx, func(tx *sql.Tx) error { return fn(&transaction{tx: tx}) })
}

func wrapStoreError(operation string, err error) error {
	if err == nil {
		return nil
	}
	for _, sentinel := range []error{
		application.ErrNotFound, application.ErrAlreadyExists, application.ErrVersionConflict,
		core.ErrAlreadyHeld, core.ErrNotHolder, core.ErrAlreadyArchived, core.ErrNotArchived,
		core.ErrHeld, core.ErrVersionExhausted,
		outbox.ErrClaimLost,
	} {
		if errors.Is(err, sentinel) {
			return sentinel
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
