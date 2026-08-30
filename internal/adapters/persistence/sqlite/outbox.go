package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"stick/internal/outbox"
)

// ClaimNotification claims the next deliverable notification.
func (s *Store) ClaimNotification(ctx context.Context, now, staleBefore time.Time, claimToken string) (*outbox.Delivery, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("claim notification: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var delivery outbox.Delivery
	err = tx.QueryRowContext(ctx, `
		SELECT id, stick_id, stick_name, holder_name, holder_email, held_since, released_at,
		       recipient_sub, recipient_name, recipient_email, subscription_generation_token, attempts
		FROM notification_outbox
		WHERE ((status IN ('pending', 'failed') AND (next_attempt_at IS NULL OR next_attempt_at <= ?))
		    OR (status = 'in_progress' AND claimed_at <= ?))
		ORDER BY id
		LIMIT 1`, now, staleBefore).Scan(
		&delivery.ID, &delivery.StickID, &delivery.StickName, &delivery.HolderName, &delivery.HolderEmail,
		&delivery.HeldSince, &delivery.ReleasedAt, &delivery.RecipientSub, &delivery.RecipientName,
		&delivery.RecipientEmail, &delivery.SubscriptionGenerationToken, &delivery.Attempts,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim notification: select outbox row: %w", err)
	}
	delivery.Attempts++
	delivery.ClaimToken = claimToken
	result, err := tx.ExecContext(ctx, `
		UPDATE notification_outbox
		SET status='in_progress', attempts=?, claim_token=?, claimed_at=?, last_attempt_at=?
		WHERE id=? AND ((status IN ('pending', 'failed') AND (next_attempt_at IS NULL OR next_attempt_at <= ?))
		    OR (status = 'in_progress' AND claimed_at <= ?))`,
		delivery.Attempts, claimToken, now, now, delivery.ID, now, staleBefore)
	if err != nil {
		return nil, fmt.Errorf("claim notification %d: update outbox row: %w", delivery.ID, err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return nil, fmt.Errorf("claim notification %d: read affected rows: %w", delivery.ID, err)
		}
		return nil, outbox.ErrClaimLost
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("claim notification %d: commit transaction: %w", delivery.ID, err)
	}
	return &delivery, nil
}

// MarkNotificationSucceeded records successful delivery and removes its subscription generation.
func (s *Store) MarkNotificationSucceeded(ctx context.Context, delivery outbox.Delivery, deliveredAt time.Time) error {
	err := s.runInTransaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE notification_outbox
			SET status='succeeded', delivered_at=?, next_attempt_at=NULL, claim_token=NULL, claimed_at=NULL
			WHERE id=? AND status='in_progress' AND claim_token=?`, deliveredAt, delivery.ID, delivery.ClaimToken)
		if err != nil {
			return fmt.Errorf("update outbox row: %w", err)
		}
		if err := requireClaim(result, delivery.ID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			DELETE FROM subscriptions
			WHERE stick_id=? AND user_sub=? AND generation_token=?`,
			delivery.StickID, delivery.RecipientSub, delivery.SubscriptionGenerationToken)
		if err != nil {
			return fmt.Errorf("delete subscription for stick %q: %w", delivery.StickID, err)
		}
		return nil
	})
	return wrapStoreError(fmt.Sprintf("mark notification %d succeeded", delivery.ID), err)
}

// MarkNotificationFailed records a failed delivery and its next retry time.
func (s *Store) MarkNotificationFailed(ctx context.Context, delivery outbox.Delivery, nextAttemptAt time.Time, failure string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE notification_outbox
		SET status='failed', next_attempt_at=?, last_error=?, claim_token=NULL, claimed_at=NULL
		WHERE id=? AND status='in_progress' AND claim_token=?`,
		nextAttemptAt, failure, delivery.ID, delivery.ClaimToken)
	if err != nil {
		return fmt.Errorf("mark notification %d failed: update outbox row: %w", delivery.ID, err)
	}
	if err := requireClaim(result, delivery.ID); err != nil {
		return err
	}
	return nil
}

func requireClaim(result sql.Result, notificationID int64) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("notification %d: read affected rows: %w", notificationID, err)
	}
	if affected != 1 {
		return outbox.ErrClaimLost
	}
	return nil
}
