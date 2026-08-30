package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	domain "stick/internal/core"
)

// GetStick returns the stick identified by id.
func (s *Store) GetStick(ctx context.Context, id string) (domain.Stick, error) {
	stick, err := scanStick(s.db.QueryRowContext(ctx, `SELECT `+stickColumns+` FROM sticks WHERE id=$1`, id))
	return stick, wrapStoreError(fmt.Sprintf("get stick %q", id), err)
}

// ListSticks returns all active sticks ordered by name.
func (s *Store) ListSticks(ctx context.Context) ([]domain.Stick, error) {
	sticks, err := scanSticks(s.db.QueryContext(ctx, `SELECT `+stickColumns+` FROM sticks WHERE archived_at IS NULL ORDER BY name`))
	return sticks, wrapStoreError("list sticks", err)
}

// ListArchivedSticks returns all archived sticks ordered by name.
func (s *Store) ListArchivedSticks(ctx context.Context) ([]domain.Stick, error) {
	sticks, err := scanSticks(s.db.QueryContext(ctx, `SELECT `+stickColumns+` FROM sticks WHERE archived_at IS NOT NULL ORDER BY name`))
	return sticks, wrapStoreError("list archived sticks", err)
}

// GetHistory returns one page of completed sessions and the total completed count.
func (s *Store) GetHistory(ctx context.Context, id string, limit, offset int) ([]domain.Session, int, error) {
	var total int
	sessions := make([]domain.Session, 0)
	err := s.runInSnapshot(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE stick_id=$1 AND released_at IS NOT NULL`, id).Scan(&total); err != nil {
			return fmt.Errorf("count history for stick %q: %w", id, err)
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT id, stick_id, holder_sub, holder_name, holder_email, reason, claimed_at, released_at
			FROM sessions WHERE stick_id=$1 AND released_at IS NOT NULL
			ORDER BY claimed_at DESC LIMIT $2 OFFSET $3`, id, limit, offset)
		if err != nil {
			return fmt.Errorf("query history for stick %q: %w", id, err)
		}
		defer rows.Close()
		for rows.Next() {
			var session domain.Session
			var releasedAt time.Time
			if err := rows.Scan(&session.ID, &session.StickID, &session.HolderSub, &session.HolderName,
				&session.HolderEmail, &session.Reason, &session.ClaimedAt, &releasedAt); err != nil {
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

// SubscribedStickIDs returns active sticks subscribed to by subject.
func (s *Store) SubscribedStickIDs(ctx context.Context, subject string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT subscriptions.stick_id FROM subscriptions
		JOIN sticks ON sticks.id=subscriptions.stick_id
		WHERE subscriptions.user_sub=$1 AND sticks.archived_at IS NULL
		ORDER BY subscriptions.stick_id`, subject)
	if err != nil {
		return nil, fmt.Errorf("query subscriptions for %q: %w", subject, err)
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan subscription for %q: %w", subject, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate subscriptions for %q: %w", subject, err)
	}
	return ids, nil
}
