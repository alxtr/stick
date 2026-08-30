package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	domain "stick/internal/core"
)

// GetHistory returns one page of completed sessions and the total completed count.
func (s *Store) GetHistory(ctx context.Context, id string, limit, offset int) ([]domain.Session, int, error) {
	var total int
	sessions := make([]domain.Session, 0)
	err := s.runInTransaction(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sessions WHERE stick_id = ? AND released_at IS NOT NULL`, id).Scan(&total); err != nil {
			return fmt.Errorf("count history for stick %q: %w", id, err)
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT id, stick_id, holder_sub, holder_name, holder_email, reason, claimed_at, released_at
			FROM sessions
			WHERE stick_id = ? AND released_at IS NOT NULL
			ORDER BY claimed_at DESC
			LIMIT ? OFFSET ?`, id, limit, offset)
		if err != nil {
			return fmt.Errorf("query history for stick %q: %w", id, err)
		}
		defer rows.Close()
		for rows.Next() {
			var session domain.Session
			var releasedAt time.Time
			if err := rows.Scan(
				&session.ID, &session.StickID, &session.HolderSub, &session.HolderName,
				&session.HolderEmail, &session.Reason, &session.ClaimedAt, &releasedAt,
			); err != nil {
				return fmt.Errorf("scan history for stick %q: %w", id, err)
			}
			session.ReleasedAt = &releasedAt
			sessions = append(sessions, session)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate history for stick %q: %w", id, err)
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return sessions, total, nil
}
