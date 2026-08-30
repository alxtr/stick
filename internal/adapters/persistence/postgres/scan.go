package postgres

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"stick/internal/application"
	domain "stick/internal/core"
)

const stickColumns = `id, name, version, holder_sub, holder_name, holder_email, claimed_at, reason, archived_at`

type rowScanner interface{ Scan(...any) error }

func scanStick(row rowScanner) (domain.Stick, error) {
	var id, name string
	var version int64
	var holderSub, holderName, holderEmail, reason sql.NullString
	var claimedAt, archivedAt sql.NullTime
	if err := row.Scan(&id, &name, &version, &holderSub, &holderName, &holderEmail, &claimedAt, &reason, &archivedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Stick{}, application.ErrNotFound
		}
		return domain.Stick{}, fmt.Errorf("scan stick: %w", err)
	}
	stick := domain.Stick{
		ID:         id,
		Name:       name,
		Version:    version,
		ArchivedAt: nullTimePtr(archivedAt),
	}
	if holderSub.Valid {
		stick.Holder = &domain.Holder{
			Sub:       holderSub.String,
			Name:      holderName.String,
			Email:     holderEmail.String,
			ClaimedAt: claimedAt.Time,
			Reason:    reason.String,
		}
	}
	return stick, nil
}

func scanSticks(rows *sql.Rows, err error) ([]domain.Stick, error) {
	if err != nil {
		return nil, fmt.Errorf("query sticks: %w", err)
	}
	defer rows.Close()
	sticks := make([]domain.Stick, 0)
	for rows.Next() {
		stick, err := scanStick(rows)
		if err != nil {
			return nil, err
		}
		sticks = append(sticks, stick)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sticks: %w", err)
	}
	return sticks, nil
}

func nullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time
	return &t
}
