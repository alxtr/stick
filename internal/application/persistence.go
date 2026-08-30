package application

import (
	"context"
	"time"

	domain "stick/internal/core"
)

// Transaction contains the writes and transaction-consistent reads needed by
// stick use cases.
type Transaction interface {
	GetStick(context.Context, string) (domain.Stick, error)
	CreateStick(context.Context, domain.Stick) error
	SaveStick(context.Context, domain.Stick, int64) error
	CreateSession(context.Context, domain.Session) error
	CloseSession(context.Context, string, string, time.Time) error
	EnqueueReleaseNotifications(context.Context, domain.Stick, time.Time) error
	Subscribe(context.Context, string, domain.Identity, string) error
	Unsubscribe(context.Context, string, string) error
}

// Store is the complete persistence adapter required by Service.
type Store interface {
	GetStick(context.Context, string) (domain.Stick, error)
	ListSticks(context.Context) ([]domain.Stick, error)
	ListArchivedSticks(context.Context) ([]domain.Stick, error)
	GetHistory(context.Context, string, int, int) ([]domain.Session, int, error)
	SubscribedStickIDs(context.Context, string) ([]string, error)
	WithinTransaction(context.Context, func(Transaction) error) error
}
