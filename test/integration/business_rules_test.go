package integration_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
	"uuid"

	"stick/internal/application"
	domain "stick/internal/core"
)

// RunBusinessRules exercises application policy through application.Service while
// treating persistence as an opaque implementation detail.
func RunBusinessRules(t *testing.T, factory Factory) {
	t.Helper()

	t.Run("creation enforces authorization validation and initial state", func(t *testing.T) {
		backend := openBackend(t, factory)
		service := application.NewService(backend)
		ctx := context.Background()
		user := identity("user", "User", "user@example.com")

		if _, err := service.CreateStick(ctx, user, "Valid"); !errors.Is(err, application.ErrForbidden) {
			t.Fatalf("non-admin CreateStick error = %v, want ErrForbidden", err)
		}
		if _, err := service.CreateStick(ctx, user, "bad!"); !errors.Is(err, application.ErrForbidden) {
			t.Fatalf("non-admin invalid CreateStick error = %v, want ErrForbidden", err)
		}
		if _, err := service.CreateStick(ctx, admin(), "bad!"); !errors.Is(err, domain.ErrInvalidStickName) {
			t.Fatalf("invalid CreateStick error = %v, want ErrInvalidStickName", err)
		}
		assertActiveStickCount(t, ctx, service, 0)

		first := createBusinessStick(t, ctx, service, "Clé 2")
		assertUUIDv7(t, first.ID)
		if first.Version != 1 || !first.Available() || first.Archived() || first.ArchivedAt != nil || first.Holder != nil {
			t.Fatalf("new stick has unexpected state: %+v", first)
		}
		persisted, err := service.GetStick(ctx, user, first.ID)
		if err != nil {
			t.Fatalf("GetStick: %v", err)
		}
		if !sticksMatch(persisted, first) {
			t.Fatalf("persisted stick = %+v, want %+v", persisted, first)
		}

		second := createBusinessStick(t, ctx, service, first.Name)
		if second.ID == first.ID {
			t.Fatalf("duplicate names received the same ID %q", first.ID)
		}
		sticks, err := service.ListSticks(ctx)
		if err != nil {
			t.Fatalf("ListSticks: %v", err)
		}
		assertSticks(t, sticks, map[string]string{first.ID: first.Name, second.ID: second.Name})
	})

	t.Run("administrative operations reject non-admins without changing state", func(t *testing.T) {
		backend := openBackend(t, factory)
		service := application.NewService(backend)
		ctx := context.Background()
		user := identity("user", "User", "user@example.com")
		active := createBusinessStick(t, ctx, service, "Active")
		toArchive := createBusinessStick(t, ctx, service, "Archived")
		archived, err := service.ArchiveStick(ctx, admin(), toArchive.ID, toArchive.Version)
		if err != nil {
			t.Fatalf("seed archived stick: %v", err)
		}

		if _, err := service.ListArchivedSticks(ctx, user); !errors.Is(err, application.ErrForbidden) {
			t.Fatalf("non-admin ListArchivedSticks error = %v, want ErrForbidden", err)
		}
		if _, err := service.RenameStick(ctx, user, active.ID, "Renamed", active.Version); !errors.Is(err, application.ErrForbidden) {
			t.Fatalf("non-admin RenameStick error = %v, want ErrForbidden", err)
		}
		if _, err := service.ArchiveStick(ctx, user, active.ID, active.Version); !errors.Is(err, application.ErrForbidden) {
			t.Fatalf("non-admin ArchiveStick error = %v, want ErrForbidden", err)
		}
		if _, err := service.UnarchiveStick(ctx, user, archived.ID, archived.Version); !errors.Is(err, application.ErrForbidden) {
			t.Fatalf("non-admin UnarchiveStick error = %v, want ErrForbidden", err)
		}

		assertVisibleStick(t, ctx, service, admin(), active)
		assertVisibleStick(t, ctx, service, admin(), archived)
	})

	t.Run("archived sticks are hidden from users but visible to admins", func(t *testing.T) {
		backend := openBackend(t, factory)
		service := application.NewService(backend)
		ctx := context.Background()
		watcher := identity("watcher", "Watcher", "watcher@example.com")
		holder := identity("holder", "Holder", "holder@example.com")
		stick := createBusinessStick(t, ctx, service, "Visibility")

		if err := service.Subscribe(ctx, watcher, stick.ID, stick.Version); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		held, err := service.ClaimStick(ctx, holder, stick.ID, "maintenance", stick.Version)
		if err != nil {
			t.Fatalf("ClaimStick: %v", err)
		}
		released, err := service.ReleaseStick(ctx, holder, stick.ID, held.Version)
		if err != nil {
			t.Fatalf("ReleaseStick: %v", err)
		}
		archived, err := service.ArchiveStick(ctx, admin(), stick.ID, released.Version)
		if err != nil {
			t.Fatalf("ArchiveStick: %v", err)
		}

		if _, err := service.GetStick(ctx, watcher, stick.ID); !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("non-admin GetStick error = %v, want ErrNotFound", err)
		}
		if _, _, err := service.GetHistory(ctx, watcher, stick.ID, 10, 0); !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("non-admin GetHistory error = %v, want ErrNotFound", err)
		}
		assertActiveStickCount(t, ctx, service, 0)
		assertSubscribedStickIDs(t, ctx, service, watcher)

		assertVisibleStick(t, ctx, service, admin(), archived)
		history, count, err := service.GetHistory(ctx, admin(), stick.ID, 10, 0)
		if err != nil {
			t.Fatalf("admin GetHistory: %v", err)
		}
		if len(history) != 1 || count != 1 || history[0].Reason != "maintenance" {
			t.Fatalf("admin history = %+v, count=%d; want one maintenance session", history, count)
		}
		archivedSticks, err := service.ListArchivedSticks(ctx, admin())
		if err != nil {
			t.Fatalf("admin ListArchivedSticks: %v", err)
		}
		assertSticks(t, archivedSticks, map[string]string{stick.ID: stick.Name})

		restored, err := service.UnarchiveStick(ctx, admin(), stick.ID, archived.Version)
		if err != nil {
			t.Fatalf("UnarchiveStick: %v", err)
		}
		assertVisibleStick(t, ctx, service, watcher, restored)
		assertSubscribedStickIDs(t, ctx, service, watcher, stick.ID)
	})

	t.Run("release ownership uses subject and preserves claim identity", func(t *testing.T) {
		backend := openBackend(t, factory)
		service := application.NewService(backend)
		ctx := context.Background()
		stick := createBusinessStick(t, ctx, service, "Ownership")
		watcher := identity("watcher", "Watcher", "watcher@example.com")
		original := identity("holder", "Alice", "alice@example.com")
		if err := service.Subscribe(ctx, watcher, stick.ID, stick.Version); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		held, err := service.ClaimStick(ctx, original, stick.ID, "deploying", stick.Version)
		if err != nil {
			t.Fatalf("ClaimStick: %v", err)
		}

		impostor := identity("impostor", "Alice", original.Email)
		if _, err := service.ReleaseStick(ctx, impostor, stick.ID, held.Version); !errors.Is(err, domain.ErrNotHolder) {
			t.Fatalf("same-email impostor ReleaseStick error = %v, want ErrNotHolder", err)
		}
		assertVisibleStick(t, ctx, service, original, held)
		_, count, err := service.GetHistory(ctx, original, stick.ID, 10, 0)
		if err != nil || count != 0 {
			t.Fatalf("history after rejected release count=%d, err=%v; want zero", count, err)
		}

		updated := identity(original.Sub, "Alice Updated", "new-alice@example.com")
		if _, err := service.ReleaseStick(ctx, updated, stick.ID, held.Version); err != nil {
			t.Fatalf("same-subject ReleaseStick: %v", err)
		}
		history, _, err := service.GetHistory(ctx, updated, stick.ID, 10, 0)
		if err != nil {
			t.Fatalf("GetHistory: %v", err)
		}
		if len(history) != 1 || history[0].HolderSub != original.Sub || history[0].HolderName != original.Name ||
			history[0].HolderEmail != original.Email || history[0].Reason != "deploying" {
			t.Fatalf("completed session did not preserve claim identity: %+v", history)
		}
		delivery := claimNotification(t, ctx, backend, "business-ownership")
		if delivery.HolderName != original.Name || delivery.HolderEmail != original.Email ||
			delivery.RecipientSub != watcher.Sub {
			t.Fatalf("delivery did not preserve claim identity: %+v", delivery)
		}
	})

	t.Run("rename preserves held and archived lifecycle state", func(t *testing.T) {
		backend := openBackend(t, factory)
		service := application.NewService(backend)
		ctx := context.Background()
		holder := identity("holder", "Holder", "holder@example.com")
		watcher := identity("watcher", "Watcher", "watcher@example.com")
		heldStick := createBusinessStick(t, ctx, service, "Held original")
		if err := service.Subscribe(ctx, watcher, heldStick.ID, heldStick.Version); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		held, err := service.ClaimStick(ctx, holder, heldStick.ID, "in progress", heldStick.Version)
		if err != nil {
			t.Fatalf("ClaimStick: %v", err)
		}
		renamedHeld, err := service.RenameStick(ctx, admin(), held.ID, "Held renamed", held.Version)
		if err != nil {
			t.Fatalf("RenameStick held: %v", err)
		}
		if renamedHeld.Version != held.Version+1 || renamedHeld.Available() || !holdersMatch(renamedHeld.Holder, held.Holder) {
			t.Fatalf("rename changed held lifecycle state: before=%+v after=%+v", held, renamedHeld)
		}
		if _, err := service.ReleaseStick(ctx, holder, renamedHeld.ID, renamedHeld.Version); err != nil {
			t.Fatalf("ReleaseStick renamed held stick: %v", err)
		}
		delivery := claimNotification(t, ctx, backend, "business-rename")
		if delivery.StickName != renamedHeld.Name {
			t.Fatalf("delivery stick name = %q, want %q", delivery.StickName, renamedHeld.Name)
		}

		archivedStick := createBusinessStick(t, ctx, service, "Archived original")
		archived, err := service.ArchiveStick(ctx, admin(), archivedStick.ID, archivedStick.Version)
		if err != nil {
			t.Fatalf("ArchiveStick: %v", err)
		}
		renamedArchived, err := service.RenameStick(ctx, admin(), archived.ID, "Archived renamed", archived.Version)
		if err != nil {
			t.Fatalf("RenameStick archived: %v", err)
		}
		if renamedArchived.Version != archived.Version+1 || !renamedArchived.Archived() ||
			renamedArchived.ArchivedAt == nil || archived.ArchivedAt == nil ||
			!timesClose(*renamedArchived.ArchivedAt, *archived.ArchivedAt) {
			t.Fatalf("rename changed archived lifecycle state: before=%+v after=%+v", archived, renamedArchived)
		}
		assertVisibleStick(t, ctx, service, admin(), renamedArchived)
	})

	t.Run("stale subscription operations are no-ops", func(t *testing.T) {
		backend := openBackend(t, factory)
		service := application.NewService(backend)
		ctx := context.Background()
		watcher := identity("watcher", "Watcher", "watcher@example.com")
		created := createBusinessStick(t, ctx, service, "Subscriptions")
		current, err := service.RenameStick(ctx, admin(), created.ID, "Subscriptions current", created.Version)
		if err != nil {
			t.Fatalf("RenameStick: %v", err)
		}

		if err := service.Subscribe(ctx, watcher, current.ID, created.Version); !errors.Is(err, application.ErrVersionConflict) {
			t.Fatalf("stale Subscribe error = %v, want ErrVersionConflict", err)
		}
		assertSubscribedStickIDs(t, ctx, service, watcher)
		if err := service.Subscribe(ctx, watcher, current.ID, current.Version); err != nil {
			t.Fatalf("current Subscribe: %v", err)
		}
		assertVisibleStick(t, ctx, service, watcher, current)
		assertSubscribedStickIDs(t, ctx, service, watcher, current.ID)

		if err := service.Unsubscribe(ctx, watcher, current.ID, created.Version); !errors.Is(err, application.ErrVersionConflict) {
			t.Fatalf("stale Unsubscribe error = %v, want ErrVersionConflict", err)
		}
		assertSubscribedStickIDs(t, ctx, service, watcher, current.ID)
		if err := service.Unsubscribe(ctx, watcher, current.ID, current.Version); err != nil {
			t.Fatalf("current Unsubscribe: %v", err)
		}
		assertSubscribedStickIDs(t, ctx, service, watcher)
		if err := service.Unsubscribe(ctx, watcher, current.ID, current.Version); err != nil {
			t.Fatalf("repeated Unsubscribe: %v", err)
		}
		assertSubscribedStickIDs(t, ctx, service, watcher)
	})

	t.Run("release without subscribers completes history without notification", func(t *testing.T) {
		backend := openBackend(t, factory)
		service := application.NewService(backend)
		ctx := context.Background()
		holder := identity("holder", "Holder", "holder@example.com")
		stick := createBusinessStick(t, ctx, service, "No subscribers")
		held, err := service.ClaimStick(ctx, holder, stick.ID, "routine work", stick.Version)
		if err != nil {
			t.Fatalf("ClaimStick: %v", err)
		}
		released, err := service.ReleaseStick(ctx, holder, stick.ID, held.Version)
		if err != nil {
			t.Fatalf("ReleaseStick: %v", err)
		}
		if !released.Available() || released.Holder != nil || released.Version != held.Version+1 {
			t.Fatalf("released stick has unexpected state: %+v", released)
		}
		history, _, err := service.GetHistory(ctx, holder, stick.ID, 10, 0)
		if err != nil {
			t.Fatalf("GetHistory: %v", err)
		}
		if len(history) != 1 || history[0].ReleasedAt == nil || history[0].Reason != "routine work" {
			t.Fatalf("completed history = %+v, want one released session", history)
		}
		delivery, err := backend.ClaimNotification(ctx, outboxClock, outboxClock, "business-no-subscriber")
		if err != nil || delivery != nil {
			t.Fatalf("ClaimNotification = %+v, err=%v; want no delivery", delivery, err)
		}
	})
}

func createBusinessStick(t *testing.T, ctx context.Context, service *application.Service, name string) domain.Stick {
	t.Helper()
	stick, err := service.CreateStick(ctx, admin(), name)
	if err != nil {
		t.Fatalf("CreateStick %q: %v", name, err)
	}
	return stick
}

func assertActiveStickCount(t *testing.T, ctx context.Context, service *application.Service, want int) {
	t.Helper()
	sticks, err := service.ListSticks(ctx)
	if err != nil {
		t.Fatalf("ListSticks: %v", err)
	}
	if len(sticks) != want {
		t.Fatalf("active stick count = %d, want %d: %+v", len(sticks), want, sticks)
	}
}

func assertVisibleStick(
	t *testing.T,
	ctx context.Context,
	service *application.Service,
	viewer domain.Identity,
	want domain.Stick,
) {
	t.Helper()
	got, err := service.GetStick(ctx, viewer, want.ID)
	if err != nil {
		t.Fatalf("GetStick %q: %v", want.ID, err)
	}
	if !sticksMatch(got, want) {
		t.Fatalf("GetStick %q = %+v, want %+v", want.ID, got, want)
	}
}

func sticksMatch(got, want domain.Stick) bool {
	if got.ID != want.ID || got.Name != want.Name || got.Version != want.Version ||
		got.Available() != want.Available() || got.Archived() != want.Archived() {
		return false
	}
	if (got.ArchivedAt == nil) != (want.ArchivedAt == nil) ||
		(got.ArchivedAt != nil && !timesClose(*got.ArchivedAt, *want.ArchivedAt)) {
		return false
	}
	return holdersMatch(got.Holder, want.Holder)
}

func assertUUIDv7(t *testing.T, id string) {
	t.Helper()
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		t.Fatalf("generated stick ID = %q, want a canonical UUID: %v", id, err)
	}
	if version := parsed[6] >> 4; version != 7 {
		t.Fatalf("generated stick ID = %q, UUID version = %d, want 7", id, version)
	}
}

func holdersMatch(got, want *domain.Holder) bool {
	if (got == nil) != (want == nil) {
		return false
	}
	return got == nil || (got.Sub == want.Sub && got.Name == want.Name && got.Email == want.Email &&
		got.Reason == want.Reason && timesClose(got.ClaimedAt, want.ClaimedAt))
}

func timesClose(got, want time.Time) bool {
	difference := got.Sub(want)
	if difference < 0 {
		difference = -difference
	}
	// BSON dates have millisecond precision, so MongoDB legitimately drops
	// sub-millisecond portions of application timestamps.
	return difference <= time.Millisecond
}

func assertSubscribedStickIDs(
	t *testing.T,
	ctx context.Context,
	service *application.Service,
	viewer domain.Identity,
	want ...string,
) {
	t.Helper()
	got, err := service.SubscribedStickIDs(ctx, viewer)
	if err != nil {
		t.Fatalf("SubscribedStickIDs: %v", err)
	}
	if !reflect.DeepEqual(got, want) && (len(got) != 0 || len(want) != 0) {
		t.Fatalf("SubscribedStickIDs = %v, want %v", got, want)
	}
}
