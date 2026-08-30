package sqlite

import (
	"context"
	"fmt"
)

// SubscribedStickIDs returns active sticks subscribed to by subject.
func (s *Store) SubscribedStickIDs(ctx context.Context, subject string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT subscriptions.stick_id
		FROM subscriptions
		JOIN sticks ON sticks.id = subscriptions.stick_id
		WHERE subscriptions.user_sub = ? AND sticks.archived_at IS NULL
		ORDER BY subscriptions.stick_id`, subject)
	if err != nil {
		return nil, fmt.Errorf("query subscriptions for %q: %w", subject, err)
	}
	defer rows.Close()
	var ids []string
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
