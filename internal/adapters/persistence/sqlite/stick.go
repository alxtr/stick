package sqlite

import (
	"context"
	"errors"
	"fmt"

	"stick/internal/core"

	"modernc.org/sqlite"
)

const stickColumns = `id, name, version, holder_sub, holder_name, holder_email, claimed_at, reason, archived_at`

// GetStick returns the stick identified by id.
func (s *Store) GetStick(ctx context.Context, id string) (core.Stick, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+stickColumns+` FROM sticks WHERE id = ?`, id)
	stick, err := scanStick(row)
	if err != nil {
		return core.Stick{}, wrapStoreError(fmt.Sprintf("get stick %q", id), err)
	}
	return stick, nil
}

// ListSticks returns all active sticks ordered by name.
func (s *Store) ListSticks(ctx context.Context) ([]core.Stick, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+stickColumns+` FROM sticks WHERE archived_at IS NULL ORDER BY name`)
	sticks, err := scanSticks(rows, err)
	if err != nil {
		return nil, fmt.Errorf("list sticks: %w", err)
	}
	return sticks, nil
}

// ListArchivedSticks returns all archived sticks ordered by name.
func (s *Store) ListArchivedSticks(ctx context.Context) ([]core.Stick, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+stickColumns+` FROM sticks WHERE archived_at IS NOT NULL ORDER BY name`)
	sticks, err := scanSticks(rows, err)
	if err != nil {
		return nil, fmt.Errorf("list archived sticks: %w", err)
	}
	return sticks, nil
}

func constraintError(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xFF == 19 // SQLITE_CONSTRAINT
}
