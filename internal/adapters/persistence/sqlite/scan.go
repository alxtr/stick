package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"stick/internal/application"
	domain "stick/internal/core"
)

type rowScanner interface {
	Scan(dest ...any) error
}

func scanStick(row rowScanner) (domain.Stick, error) {
	var (
		stickID     string
		stickName   string
		version     int64
		holderSub   sql.NullString
		holderName  sql.NullString
		holderEmail sql.NullString
		claimedAt   sql.NullTime
		reason      sql.NullString
		archivedAt  sql.NullTime
	)
	if err := row.Scan(&stickID, &stickName, &version, &holderSub, &holderName, &holderEmail, &claimedAt, &reason, &archivedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Stick{}, application.ErrNotFound
		}
		return domain.Stick{}, fmt.Errorf("scan stick: %w", err)
	}

	stick := domain.Stick{
		ID:         stickID,
		Name:       stickName,
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
			return nil, fmt.Errorf("scan stick row: %w", err)
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
