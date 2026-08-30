package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"stick/internal/application"
	domain "stick/internal/core"
)

type transaction struct{ tx *sql.Tx }

func (t *transaction) GetStick(ctx context.Context, id string) (domain.Stick, error) {
	stick, err := scanStick(t.tx.QueryRowContext(ctx, `SELECT `+stickColumns+` FROM sticks WHERE id=$1 FOR UPDATE`, id))
	return stick, wrapStoreError(fmt.Sprintf("get stick %q in transaction", id), err)
}

func (t *transaction) CreateStick(ctx context.Context, stick domain.Stick) error {
	if stick.Version != 1 {
		return fmt.Errorf("create stick %q: version is %d, want 1", stick.ID, stick.Version)
	}
	_, err := t.tx.ExecContext(ctx, `INSERT INTO sticks (id, name, version) VALUES ($1, $2, $3)`, stick.ID, stick.Name, stick.Version)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
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
		holderSub, holderName, holderEmail = stick.Holder.Sub, stick.Holder.Name, stick.Holder.Email
		claimedAt, reason = stick.Holder.ClaimedAt, stick.Holder.Reason
	}
	result, err := t.tx.ExecContext(ctx, `
		UPDATE sticks SET name=$1, version=$2, holder_sub=$3, holder_name=$4, holder_email=$5,
			claimed_at=$6, reason=$7, archived_at=$8
		WHERE id=$9 AND version=$10`, stick.Name, stick.Version, holderSub, holderName, holderEmail,
		claimedAt, reason, stick.ArchivedAt, stick.ID, expectedVersion)
	if err != nil {
		return fmt.Errorf("save stick %q at version %d: %w", stick.ID, expectedVersion, err)
	}
	if err := requireOneRow(result, application.ErrVersionConflict); err != nil {
		return err
	}
	return nil
}

func (t *transaction) CreateSession(ctx context.Context, session domain.Session) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO sessions (stick_id, holder_sub, holder_name, holder_email, reason, claimed_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, session.StickID, session.HolderSub, session.HolderName,
		session.HolderEmail, session.Reason, session.ClaimedAt)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (t *transaction) CloseSession(ctx context.Context, stickID, holderSub string, releasedAt time.Time) error {
	result, err := t.tx.ExecContext(ctx, `
		UPDATE sessions SET released_at=$1
		WHERE stick_id=$2 AND holder_sub=$3 AND released_at IS NULL`, releasedAt, stickID, holderSub)
	if err != nil {
		return fmt.Errorf("close session: %w", err)
	}
	if err := requireOneRow(result, errors.New("active session not found")); err != nil {
		return fmt.Errorf("close session: %w", err)
	}
	return nil
}

func (t *transaction) EnqueueReleaseNotifications(ctx context.Context, before domain.Stick, releasedAt time.Time) error {
	if before.Holder == nil {
		return errors.New("enqueue release notifications without holder snapshot")
	}
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO notification_outbox (
			stick_id, stick_name, holder_name, holder_email, held_since, released_at,
			recipient_sub, recipient_name, recipient_email, subscription_generation_token,
			status, attempts, next_attempt_at, created_at
		)
		SELECT $1, $2, $3, $4, $5, $6, user_sub, user_name, user_email, generation_token,
			'pending', 0, $6, $6
		FROM subscriptions WHERE stick_id=$1`,
		before.ID, before.Name, before.Holder.Name, before.Holder.Email, before.Holder.ClaimedAt,
		releasedAt)
	if err != nil {
		return fmt.Errorf("enqueue release notifications for stick %q: %w", before.ID, err)
	}
	return nil
}

func (t *transaction) Subscribe(ctx context.Context, stickID string, identity domain.Identity, generationToken string) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO subscriptions (stick_id, user_sub, user_name, user_email, generation_token)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (stick_id, user_sub) DO UPDATE SET
			user_name=EXCLUDED.user_name, user_email=EXCLUDED.user_email,
			generation_token=EXCLUDED.generation_token`,
		stickID, identity.Sub, identity.Name, identity.Email, generationToken)
	if err != nil {
		return fmt.Errorf("subscribe %q to stick %q: %w", identity.Sub, stickID, err)
	}
	return nil
}

func (t *transaction) Unsubscribe(ctx context.Context, stickID, subject string) error {
	if _, err := t.tx.ExecContext(ctx, `DELETE FROM subscriptions WHERE stick_id=$1 AND user_sub=$2`, stickID, subject); err != nil {
		return fmt.Errorf("unsubscribe %q from stick %q: %w", subject, stickID, err)
	}
	return nil
}

func requireOneRow(result sql.Result, zeroRows error) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read affected rows: %w", err)
	}
	if affected != 1 {
		return zeroRows
	}
	return nil
}
