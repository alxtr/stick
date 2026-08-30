package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"stick/internal/application"
	domain "stick/internal/core"
)

type transaction struct{ tx *sql.Tx }

func (t *transaction) GetStick(ctx context.Context, id string) (domain.Stick, error) {
	stick, err := scanStick(t.tx.QueryRowContext(ctx, `SELECT `+stickColumns+` FROM sticks WHERE id=?`, id))
	if err != nil {
		return domain.Stick{}, wrapStoreError(fmt.Sprintf("get stick %q in transaction", id), err)
	}
	return stick, nil
}

func (t *transaction) CreateStick(ctx context.Context, stick domain.Stick) error {
	if stick.Version != 1 {
		return fmt.Errorf("create stick %q: version is %d, want 1", stick.ID, stick.Version)
	}
	_, err := t.tx.ExecContext(ctx, `INSERT INTO sticks (id, name, version) VALUES (?, ?, ?)`, stick.ID, stick.Name, stick.Version)
	if constraintError(err) {
		return application.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("create stick %q: %w", stick.ID, err)
	}
	return nil
}

func (t *transaction) SaveStick(ctx context.Context, stick domain.Stick, expectedVersion int64) error {
	if expectedVersion < 1 || expectedVersion == math.MaxInt64 || stick.Version != expectedVersion+1 {
		return fmt.Errorf("save stick %q: version transition %d to %d is not monotonic", stick.ID, expectedVersion, stick.Version)
	}
	var holderSub, holderName, holderEmail, claimedAt, reason any
	if stick.Holder != nil {
		holderSub = stick.Holder.Sub
		holderName = stick.Holder.Name
		holderEmail = stick.Holder.Email
		claimedAt = stick.Holder.ClaimedAt
		reason = stick.Holder.Reason
	}
	result, err := t.tx.ExecContext(ctx, `
		UPDATE sticks SET name=?, version=?, holder_sub=?, holder_name=?, holder_email=?, claimed_at=?, reason=?, archived_at=?
		WHERE id=? AND version=?`,
		stick.Name, stick.Version, holderSub, holderName, holderEmail, claimedAt, reason, stick.ArchivedAt,
		stick.ID, expectedVersion)
	if err != nil {
		return fmt.Errorf("save stick %q at version %d: %w", stick.ID, expectedVersion, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("save stick %q: read affected rows: %w", stick.ID, err)
	}
	if n != 1 {
		return application.ErrVersionConflict
	}
	return nil
}

func (t *transaction) CreateSession(ctx context.Context, session domain.Session) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO sessions (stick_id, holder_sub, holder_name, holder_email, reason, claimed_at)
		VALUES (?, ?, ?, ?, ?, ?)`, session.StickID, session.HolderSub, session.HolderName,
		session.HolderEmail, session.Reason, session.ClaimedAt)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (t *transaction) CloseSession(ctx context.Context, stickID, holderSub string, releasedAt time.Time) error {
	result, err := t.tx.ExecContext(ctx, `
		UPDATE sessions SET released_at=?
		WHERE stick_id=? AND holder_sub=? AND released_at IS NULL`, releasedAt, stickID, holderSub)
	if err != nil {
		return fmt.Errorf("close session: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("close session: read affected rows: %w", err)
	}
	if n != 1 {
		return fmt.Errorf("close session: affected %d rows, want 1", n)
	}
	return nil
}

func (t *transaction) EnqueueReleaseNotifications(
	ctx context.Context,
	before domain.Stick,
	releasedAt time.Time,
) error {
	if before.Holder == nil {
		return errors.New("enqueue release notifications without holder snapshot")
	}
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO notification_outbox (
			stick_id, stick_name, holder_name, holder_email, held_since, released_at,
			recipient_sub, recipient_name, recipient_email, subscription_generation_token,
			status, attempts, next_attempt_at, created_at
		)
		SELECT ?, ?, ?, ?, ?, ?, user_sub, user_name, user_email, generation_token,
			'pending', 0, ?, ?
		FROM subscriptions WHERE stick_id=?`,
		before.ID, before.Name, before.Holder.Name, before.Holder.Email, before.Holder.ClaimedAt,
		releasedAt, releasedAt, releasedAt, before.ID)
	if err != nil {
		return fmt.Errorf("enqueue release notifications for stick %q: %w", before.ID, err)
	}
	return nil
}

func (t *transaction) Subscribe(ctx context.Context, stickID string, identity domain.Identity, generationToken string) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO subscriptions (stick_id, user_sub, user_name, user_email, generation_token)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(stick_id, user_sub) DO UPDATE SET
			user_name=excluded.user_name, user_email=excluded.user_email,
			generation_token=excluded.generation_token`,
		stickID, identity.Sub, identity.Name, identity.Email, generationToken)
	if err != nil {
		return fmt.Errorf("subscribe %q to stick %q: %w", identity.Sub, stickID, err)
	}
	return nil
}

func (t *transaction) Unsubscribe(ctx context.Context, stickID, subject string) error {
	_, err := t.tx.ExecContext(ctx, `DELETE FROM subscriptions WHERE stick_id=? AND user_sub=?`, stickID, subject)
	if err != nil {
		return fmt.Errorf("unsubscribe %q from stick %q: %w", subject, stickID, err)
	}
	return nil
}
