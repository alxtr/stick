// Package application implements the application's stick use cases and policies.
package application

import (
	"context"
	"time"
	"uuid"

	domain "stick/internal/core"
)

// Service coordinates stick use cases and enforces application policy.
type Service struct {
	store Store
}

// NewService returns an application service backed by store.
func NewService(store Store) *Service {
	return &Service{store: store}
}

// ListSticks returns all active sticks.
func (s *Service) ListSticks(ctx context.Context) ([]domain.Stick, error) {
	return s.store.ListSticks(ctx)
}

// ListArchivedSticks returns archived sticks to administrators.
func (s *Service) ListArchivedSticks(ctx context.Context, identity domain.Identity) ([]domain.Stick, error) {
	if err := requireAdmin(identity); err != nil {
		return nil, err
	}
	return s.store.ListArchivedSticks(ctx)
}

// GetStick returns a stick if it is visible to identity.
func (s *Service) GetStick(ctx context.Context, identity domain.Identity, id string) (domain.Stick, error) {
	return s.getVisibleStick(ctx, identity, id)
}

// GetHistory returns completed sessions for a visible stick.
func (s *Service) GetHistory(
	ctx context.Context,
	identity domain.Identity,
	id string,
	limit, offset int,
) ([]domain.Session, int, error) {
	if _, err := s.getVisibleStick(ctx, identity, id); err != nil {
		return nil, 0, err
	}
	return s.store.GetHistory(ctx, id, limit, offset)
}

// CreateStick validates and creates a stick with a UUIDv7 ID.
func (s *Service) CreateStick(ctx context.Context, identity domain.Identity, name string) (domain.Stick, error) {
	if err := requireAdmin(identity); err != nil {
		return domain.Stick{}, err
	}
	stick, err := domain.NewStick(uuid.NewV7().String(), name)
	if err != nil {
		return domain.Stick{}, err
	}
	if err := s.store.WithinTransaction(ctx, func(tx Transaction) error {
		return tx.CreateStick(ctx, stick)
	}); err != nil {
		return domain.Stick{}, err
	}
	return stick, nil
}

// RenameStick validates and renames a stick for an administrator.
func (s *Service) RenameStick(
	ctx context.Context,
	identity domain.Identity,
	id, name string,
	expectedVersion int64,
) (domain.Stick, error) {
	if err := requireAdmin(identity); err != nil {
		return domain.Stick{}, err
	}
	if err := domain.ValidateStickName(name); err != nil {
		return domain.Stick{}, err
	}
	return s.transitionStick(ctx, id, expectedVersion, func(stick domain.Stick) (domain.Stick, error) {
		return domain.Rename(stick, name)
	}, nil)
}

// ArchiveStick archives an available stick for an administrator.
func (s *Service) ArchiveStick(ctx context.Context, identity domain.Identity, id string, expectedVersion int64) (domain.Stick, error) {
	if err := requireAdmin(identity); err != nil {
		return domain.Stick{}, err
	}
	return s.transitionStick(ctx, id, expectedVersion, func(stick domain.Stick) (domain.Stick, error) {
		return domain.Archive(stick, time.Now().UTC())
	}, nil)
}

// UnarchiveStick restores an archived stick for an administrator.
func (s *Service) UnarchiveStick(ctx context.Context, identity domain.Identity, id string, expectedVersion int64) (domain.Stick, error) {
	if err := requireAdmin(identity); err != nil {
		return domain.Stick{}, err
	}
	return s.transitionStick(ctx, id, expectedVersion, domain.Unarchive, nil)
}

// ClaimStick validates the reason and atomically claims a stick and opens its session.
func (s *Service) ClaimStick(
	ctx context.Context,
	identity domain.Identity,
	id, reason string,
	expectedVersion int64,
) (domain.Stick, error) {
	if err := domain.ValidateClaimReason(reason); err != nil {
		return domain.Stick{}, err
	}
	now := time.Now().UTC()
	return s.transitionStick(ctx, id, expectedVersion, func(stick domain.Stick) (domain.Stick, error) {
		return domain.Claim(stick, identity, reason, now)
	}, func(tx Transaction, _ domain.Stick, next domain.Stick) error {
		return tx.CreateSession(ctx, domain.Session{
			StickID:     next.ID,
			HolderSub:   identity.Sub,
			HolderName:  identity.Name,
			HolderEmail: identity.Email,
			Reason:      reason,
			ClaimedAt:   now,
		})
	})
}

// ReleaseStick atomically releases a stick, closes its session, snapshots
// subscribers, and writes notification outbox entries.
func (s *Service) ReleaseStick(ctx context.Context, identity domain.Identity, id string, expectedVersion int64) (domain.Stick, error) {
	releasedAt := time.Now().UTC()
	return s.transitionStick(ctx, id, expectedVersion, func(stick domain.Stick) (domain.Stick, error) {
		return domain.Release(stick, identity.Sub)
	}, func(tx Transaction, before, _ domain.Stick) error {
		if err := tx.CloseSession(ctx, id, identity.Sub, releasedAt); err != nil {
			return err
		}
		return tx.EnqueueReleaseNotifications(ctx, before, releasedAt)
	})
}

// SubscribedStickIDs returns identity's active-stick subscriptions.
func (s *Service) SubscribedStickIDs(ctx context.Context, identity domain.Identity) ([]string, error) {
	return s.store.SubscribedStickIDs(ctx, identity.Sub)
}

// Subscribe subscribes identity to release notifications for a stick.
func (s *Service) Subscribe(ctx context.Context, identity domain.Identity, id string, expectedVersion int64) error {
	return s.store.WithinTransaction(ctx, func(tx Transaction) error {
		stick, err := tx.GetStick(ctx, id)
		if err != nil {
			return err
		}
		if stick.Version != expectedVersion {
			return ErrVersionConflict
		}
		generationToken := uuid.NewV7().String()
		return tx.Subscribe(ctx, id, identity, generationToken)
	})
}

// Unsubscribe removes identity's release notification subscription for a stick.
func (s *Service) Unsubscribe(ctx context.Context, identity domain.Identity, id string, expectedVersion int64) error {
	return s.store.WithinTransaction(ctx, func(tx Transaction) error {
		stick, err := tx.GetStick(ctx, id)
		if err != nil {
			return err
		}
		if stick.Version != expectedVersion {
			return ErrVersionConflict
		}
		return tx.Unsubscribe(ctx, id, identity.Sub)
	})
}

type transitionWork func(Transaction, domain.Stick, domain.Stick) error

func (s *Service) transitionStick(
	ctx context.Context,
	id string,
	expectedVersion int64,
	transition func(domain.Stick) (domain.Stick, error),
	work transitionWork,
) (domain.Stick, error) {
	var result domain.Stick
	err := s.store.WithinTransaction(ctx, func(tx Transaction) error {
		current, err := tx.GetStick(ctx, id)
		if err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return ErrVersionConflict
		}
		next, err := transition(current)
		if err != nil {
			return err
		}
		if err := tx.SaveStick(ctx, next, expectedVersion); err != nil {
			return err
		}
		if work != nil {
			if err := work(tx, current, next); err != nil {
				return err
			}
		}
		result = next
		return nil
	})
	if err != nil {
		return domain.Stick{}, err
	}
	return result, nil
}

func (s *Service) getVisibleStick(ctx context.Context, identity domain.Identity, id string) (domain.Stick, error) {
	stick, err := s.store.GetStick(ctx, id)
	if err != nil {
		return domain.Stick{}, err
	}
	if stick.Archived() && !identity.IsAdmin {
		return domain.Stick{}, ErrNotFound
	}
	return stick, nil
}

func requireAdmin(identity domain.Identity) error {
	if !identity.IsAdmin {
		return ErrForbidden
	}
	return nil
}
