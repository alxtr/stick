package postgres

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
	var delivery outbox.Delivery
	err := s.db.QueryRowContext(ctx, `
		WITH candidate AS (
			SELECT id FROM notification_outbox
			WHERE ((status IN ('pending', 'failed') AND (next_attempt_at IS NULL OR next_attempt_at <= $1))
				OR (status = 'in_progress' AND claimed_at <= $2))
			ORDER BY id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE notification_outbox AS notification
		SET status='in_progress', attempts=notification.attempts+1, claim_token=$3,
			claimed_at=$1, last_attempt_at=$1
		FROM candidate
		WHERE notification.id=candidate.id
		RETURNING notification.id, notification.stick_id, notification.stick_name,
			notification.holder_name, notification.holder_email, notification.held_since,
			notification.released_at, notification.recipient_sub, notification.recipient_name,
			notification.recipient_email, notification.subscription_generation_token,
			notification.attempts, notification.claim_token`,
		now, staleBefore, claimToken).Scan(
		&delivery.ID, &delivery.StickID, &delivery.StickName, &delivery.HolderName,
		&delivery.HolderEmail, &delivery.HeldSince, &delivery.ReleasedAt, &delivery.RecipientSub,
		&delivery.RecipientName, &delivery.RecipientEmail,
		&delivery.SubscriptionGenerationToken, &delivery.Attempts, &delivery.ClaimToken,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim notification: %w", err)
	}
	return &delivery, nil
}

// MarkNotificationSucceeded records successful delivery and removes its subscription generation.
func (s *Store) MarkNotificationSucceeded(ctx context.Context, delivery outbox.Delivery, deliveredAt time.Time) error {
	err := s.runInTransaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE notification_outbox
			SET status='succeeded', delivered_at=$1, next_attempt_at=NULL,
				claim_token=NULL, claimed_at=NULL
			WHERE id=$2 AND status='in_progress' AND claim_token=$3`,
			deliveredAt, delivery.ID, delivery.ClaimToken)
		if err != nil {
			return fmt.Errorf("update outbox row: %w", err)
		}
		if err := requireOneRow(result, outbox.ErrClaimLost); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM subscriptions
			WHERE stick_id=$1 AND user_sub=$2 AND generation_token=$3`,
			delivery.StickID, delivery.RecipientSub, delivery.SubscriptionGenerationToken); err != nil {
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
		SET status='failed', next_attempt_at=$1, last_error=$2,
			claim_token=NULL, claimed_at=NULL
		WHERE id=$3 AND status='in_progress' AND claim_token=$4`,
		nextAttemptAt, failure, delivery.ID, delivery.ClaimToken)
	if err != nil {
		return fmt.Errorf("mark notification %d failed: %w", delivery.ID, err)
	}
	if err := requireOneRow(result, outbox.ErrClaimLost); err != nil {
		return err
	}
	return nil
}
