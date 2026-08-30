package integration_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"stick/internal/application"
	domain "stick/internal/core"
	"stick/internal/outbox"
)

// Factory returns a newly initialized, isolated backend. Run closes each
// backend after its contract subtest. A factory may register additional cleanup
// (for example, dropping an isolated PostgreSQL database) with t.Cleanup.
// Backend is the aggregate contract consumed by this black-box test suite.
type Backend interface {
	application.Store
	outbox.Store
	PingContext(context.Context) error
	Close() error
}

type Factory func(t *testing.T) Backend

var outboxClock = time.Date(2100, time.January, 1, 12, 0, 0, 0, time.UTC)

// Run executes the persistence contract against a fresh backend per subtest.
// It deliberately tests application and port behavior rather than storage IDs,
// schemas, queries, or other backend implementation details.
func Run(t *testing.T, factory Factory) {
	t.Helper()

	t.Run("create get list and duplicate IDs", func(t *testing.T) {
		backend := openBackend(t, factory)
		ctx := context.Background()

		first := createStick(t, ctx, backend, "contract-create-1", "Zulu")
		second := createStick(t, ctx, backend, "contract-create-2", "Alpha")
		if first.Version != 1 || !first.Available() || first.Archived() || first.Holder != nil {
			t.Fatalf("new stick has unexpected state: %+v", first)
		}

		got, err := backend.GetStick(ctx, first.ID)
		if err != nil {
			t.Fatalf("GetStick: %v", err)
		}
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("GetStick = %+v, want %+v", got, first)
		}
		if _, err := backend.GetStick(ctx, "contract-missing"); !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("GetStick missing error = %v, want ErrNotFound", err)
		}

		duplicate, err := domain.NewStick(first.ID, "Duplicate")
		if err != nil {
			t.Fatal(err)
		}
		err = backend.WithinTransaction(ctx, func(tx application.Transaction) error {
			return tx.CreateStick(ctx, duplicate)
		})
		if !errors.Is(err, application.ErrAlreadyExists) {
			t.Fatalf("duplicate CreateStick error = %v, want ErrAlreadyExists", err)
		}

		active, err := backend.ListSticks(ctx)
		if err != nil {
			t.Fatalf("ListSticks: %v", err)
		}
		assertSticks(t, active, map[string]string{first.ID: first.Name, second.ID: second.Name})

		service := application.NewService(backend)
		archived, err := service.ArchiveStick(ctx, admin(), second.ID, second.Version)
		if err != nil {
			t.Fatalf("ArchiveStick: %v", err)
		}
		active, err = backend.ListSticks(ctx)
		if err != nil {
			t.Fatalf("ListSticks after archive: %v", err)
		}
		assertSticks(t, active, map[string]string{first.ID: first.Name})
		archivedSticks, err := backend.ListArchivedSticks(ctx)
		if err != nil {
			t.Fatalf("ListArchivedSticks: %v", err)
		}
		assertSticks(t, archivedSticks, map[string]string{archived.ID: archived.Name})
		if !archivedSticks[0].Archived() || archivedSticks[0].ArchivedAt == nil {
			t.Fatalf("archived list returned active state: %+v", archivedSticks[0])
		}
	})

	t.Run("domain lifecycle conflicts", func(t *testing.T) {
		backend := openBackend(t, factory)
		ctx := context.Background()
		service := application.NewService(backend)
		stick := createStick(t, ctx, backend, "contract-lifecycle", "Lifecycle")
		holder := identity("holder", "Holder", "holder@example.com")

		archived, err := service.ArchiveStick(ctx, admin(), stick.ID, stick.Version)
		if err != nil {
			t.Fatal(err)
		}
		_, err = service.ArchiveStick(ctx, admin(), stick.ID, archived.Version)
		assertError(t, err, domain.ErrAlreadyArchived)
		restored, err := service.UnarchiveStick(ctx, admin(), stick.ID, archived.Version)
		if err != nil {
			t.Fatal(err)
		}
		_, err = service.UnarchiveStick(ctx, admin(), stick.ID, restored.Version)
		assertError(t, err, domain.ErrNotArchived)

		held, err := service.ClaimStick(ctx, holder, stick.ID, "deploying", restored.Version)
		if err != nil {
			t.Fatal(err)
		}
		_, err = service.ClaimStick(ctx, identity("other", "Other", "other@example.com"), stick.ID, "also deploying", held.Version)
		assertError(t, err, domain.ErrAlreadyHeld)
		_, err = service.ArchiveStick(ctx, admin(), stick.ID, held.Version)
		assertError(t, err, domain.ErrHeld)
		_, err = service.ReleaseStick(ctx, identity("other", "Other", "other@example.com"), stick.ID, held.Version)
		assertError(t, err, domain.ErrNotHolder)

		released, err := service.ReleaseStick(ctx, holder, stick.ID, held.Version)
		if err != nil {
			t.Fatal(err)
		}
		archived, err = service.ArchiveStick(ctx, admin(), stick.ID, released.Version)
		if err != nil {
			t.Fatal(err)
		}
		_, err = service.ClaimStick(ctx, holder, stick.ID, "archived claim", archived.Version)
		assertError(t, err, domain.ErrAlreadyArchived)

		current, err := backend.GetStick(ctx, stick.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Version != archived.Version || !current.Archived() || !current.Available() {
			t.Fatalf("failed lifecycle operations changed state: %+v", current)
		}
	})

	t.Run("monotonic versions and stale CAS no-op", func(t *testing.T) {
		backend := openBackend(t, factory)
		ctx := context.Background()
		service := application.NewService(backend)
		stick := createStick(t, ctx, backend, "contract-version", "Original")
		if stick.Version != 1 {
			t.Fatalf("created version = %d, want 1", stick.Version)
		}
		stale := stick

		stick = rename(t, ctx, service, stick, "Renamed")
		assertVersion(t, stick, 2)
		if err := service.Subscribe(ctx, identity("watcher", "Watcher", "watcher@example.com"), stick.ID, stick.Version); err != nil {
			t.Fatal(err)
		}
		afterSubscribe, err := backend.GetStick(ctx, stick.ID)
		if err != nil {
			t.Fatal(err)
		}
		assertVersion(t, afterSubscribe, stick.Version)

		holder := identity("holder", "Holder", "holder@example.com")
		stick, err = service.ClaimStick(ctx, holder, stick.ID, "working", stick.Version)
		if err != nil {
			t.Fatal(err)
		}
		assertVersion(t, stick, 3)
		stick, err = service.ReleaseStick(ctx, holder, stick.ID, stick.Version)
		if err != nil {
			t.Fatal(err)
		}
		assertVersion(t, stick, 4)
		stick, err = service.ArchiveStick(ctx, admin(), stick.ID, stick.Version)
		if err != nil {
			t.Fatal(err)
		}
		assertVersion(t, stick, 5)
		stick, err = service.UnarchiveStick(ctx, admin(), stick.ID, stick.Version)
		if err != nil {
			t.Fatal(err)
		}
		assertVersion(t, stick, 6)

		staleUpdate, err := domain.Rename(stale, "Stale write")
		if err != nil {
			t.Fatal(err)
		}
		err = backend.WithinTransaction(ctx, func(tx application.Transaction) error {
			return tx.SaveStick(ctx, staleUpdate, stale.Version)
		})
		if !errors.Is(err, application.ErrVersionConflict) {
			t.Fatalf("stale SaveStick error = %v, want ErrVersionConflict", err)
		}
		current, err := backend.GetStick(ctx, stick.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Name != stick.Name || current.Version != stick.Version || current.Archived() {
			t.Fatalf("stale CAS changed current state: %+v", current)
		}
	})

	t.Run("concurrent same-version mutation has one winner", func(t *testing.T) {
		backend := openBackend(t, factory)
		ctx := context.Background()
		service := application.NewService(backend)
		stick := createStick(t, ctx, backend, "contract-concurrent", "Original")

		start := make(chan struct{})
		results := make(chan error, 2)
		var ready sync.WaitGroup
		ready.Add(2)
		for _, name := range []string{"First winner", "Second winner"} {
			go func() {
				ready.Done()
				<-start
				_, err := service.RenameStick(ctx, admin(), stick.ID, name, stick.Version)
				results <- err
			}()
		}
		ready.Wait()
		close(start)

		var succeeded, conflicted int
		for range 2 {
			switch err := <-results; {
			case err == nil:
				succeeded++
			case errors.Is(err, application.ErrVersionConflict):
				conflicted++
			default:
				t.Fatalf("concurrent mutation error = %v", err)
			}
		}
		if succeeded != 1 || conflicted != 1 {
			t.Fatalf("concurrent results: succeeded=%d conflicted=%d", succeeded, conflicted)
		}
		current, err := backend.GetStick(ctx, stick.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Version != stick.Version+1 {
			t.Fatalf("version after concurrent mutation = %d, want %d", current.Version, stick.Version+1)
		}
	})

	t.Run("claim creates active session excluded from completed history", func(t *testing.T) {
		backend := openBackend(t, factory)
		ctx := context.Background()
		service := application.NewService(backend)
		stick := createStick(t, ctx, backend, "contract-active-session", "Active session")
		holder := identity("holder", "Holder", "holder@example.com")

		held, err := service.ClaimStick(ctx, holder, stick.ID, "in progress", stick.Version)
		if err != nil {
			t.Fatal(err)
		}
		persisted, err := backend.GetStick(ctx, stick.ID)
		if err != nil {
			t.Fatal(err)
		}
		if persisted.Available() || persisted.Holder == nil || persisted.Holder.Sub != holder.Sub || persisted.Version != held.Version {
			t.Fatalf("claim was not persisted: %+v", persisted)
		}
		history, count, err := backend.GetHistory(ctx, stick.ID, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(history) != 0 || count != 0 {
			t.Fatalf("active session appeared in completed history: rows=%d count=%d", len(history), count)
		}
	})

	t.Run("release closes one session and creates immutable delivery snapshots", func(t *testing.T) {
		backend := openBackend(t, factory)
		ctx := context.Background()
		service := application.NewService(backend)
		stick := createStick(t, ctx, backend, "contract-release", "Production")
		holder := identity("holder", "Alice", "alice@example.com")
		watchers := []domain.Identity{
			identity("watcher-1", "Bob", "bob@example.com"),
			identity("watcher-2", "Carol", "carol@example.com"),
		}

		held, err := service.ClaimStick(ctx, holder, stick.ID, "deploying release", stick.Version)
		if err != nil {
			t.Fatal(err)
		}
		persistedHeld, err := backend.GetStick(ctx, stick.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, watcher := range watchers {
			if err := service.Subscribe(ctx, watcher, stick.ID, held.Version); err != nil {
				t.Fatal(err)
			}
		}
		released, err := service.ReleaseStick(ctx, holder, stick.ID, held.Version)
		if err != nil {
			t.Fatal(err)
		}

		history, count, err := backend.GetHistory(ctx, stick.ID, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(history) != 1 || count != 1 {
			t.Fatalf("completed history rows=%d count=%d, want one", len(history), count)
		}
		session := history[0]
		if session.ReleasedAt == nil || session.StickID != stick.ID || session.HolderSub != holder.Sub ||
			session.HolderName != holder.Name || session.HolderEmail != holder.Email || session.Reason != "deploying release" {
			t.Fatalf("completed session snapshot is incorrect: %+v", session)
		}

		renamed := rename(t, ctx, service, released, "Renamed after release")
		for _, watcher := range watchers {
			updated := watcher
			updated.Name += " Updated"
			updated.Email = "updated-" + watcher.Email
			if err := service.Subscribe(ctx, updated, stick.ID, renamed.Version); err != nil {
				t.Fatal(err)
			}
		}

		deliveries := make(map[string]outbox.Delivery)
		for i := range watchers {
			delivery := claimNotification(t, ctx, backend, fmt.Sprintf("snapshot-token-%d", i))
			deliveries[delivery.RecipientSub] = delivery
		}
		if delivery, err := backend.ClaimNotification(ctx, outboxClock, outboxClock.Add(-time.Hour), "no-third-delivery"); err != nil || delivery != nil {
			t.Fatalf("third ClaimNotification = %+v, err=%v; want no delivery", delivery, err)
		}
		if len(deliveries) != len(watchers) {
			t.Fatalf("delivery recipient count = %d, want %d", len(deliveries), len(watchers))
		}
		for _, watcher := range watchers {
			delivery, ok := deliveries[watcher.Sub]
			if !ok {
				t.Fatalf("missing delivery for %q", watcher.Sub)
			}
			if delivery.StickID != stick.ID || delivery.StickName != stick.Name ||
				delivery.HolderName != holder.Name || delivery.HolderEmail != holder.Email ||
				delivery.RecipientName != watcher.Name || delivery.RecipientEmail != watcher.Email ||
				delivery.SubscriptionGenerationToken == "" || delivery.Attempts != 1 {
				t.Fatalf("incorrect immutable delivery snapshot: %+v", delivery)
			}
			if persistedHeld.Holder == nil || !delivery.HeldSince.Equal(persistedHeld.Holder.ClaimedAt) ||
				!delivery.ReleasedAt.Equal(*session.ReleasedAt) {
				t.Fatalf("delivery session times do not match persisted snapshots: delivery=%+v session=%+v", delivery, session)
			}
		}
	})

	t.Run("history ordering count and pagination", func(t *testing.T) {
		backend := openBackend(t, factory)
		ctx := context.Background()
		stick := createStick(t, ctx, backend, "contract-history", "History")
		base := time.Date(2040, time.March, 1, 8, 0, 0, 0, time.UTC)

		for i := 1; i <= 4; i++ {
			claimedAt := base.Add(time.Duration(i) * time.Hour)
			session := domain.Session{
				StickID:     stick.ID,
				HolderSub:   fmt.Sprintf("holder-%d", i),
				HolderName:  fmt.Sprintf("Holder %d", i),
				HolderEmail: fmt.Sprintf("holder-%d@example.com", i),
				Reason:      fmt.Sprintf("session-%d", i),
				ClaimedAt:   claimedAt,
			}
			err := backend.WithinTransaction(ctx, func(tx application.Transaction) error {
				if err := tx.CreateSession(ctx, session); err != nil {
					return err
				}
				return tx.CloseSession(ctx, stick.ID, session.HolderSub, claimedAt.Add(30*time.Minute))
			})
			if err != nil {
				t.Fatalf("create completed session %d: %v", i, err)
			}
		}

		firstPage, firstTotal, err := backend.GetHistory(ctx, stick.ID, 2, 0)
		if err != nil {
			t.Fatal(err)
		}
		secondPage, secondTotal, err := backend.GetHistory(ctx, stick.ID, 2, 2)
		if err != nil {
			t.Fatal(err)
		}
		if firstTotal != 4 || secondTotal != 4 {
			t.Fatalf("GetHistory totals = %d and %d, want 4", firstTotal, secondTotal)
		}
		assertReasons(t, firstPage, "session-4", "session-3")
		assertReasons(t, secondPage, "session-2", "session-1")
		for _, session := range append(firstPage, secondPage...) {
			if session.StickID != stick.ID || session.ReleasedAt == nil || !session.ReleasedAt.After(session.ClaimedAt) {
				t.Fatalf("invalid completed history row: %+v", session)
			}
		}
	})

	t.Run("subscription generations protect unsubscribe and resubscribe", func(t *testing.T) {
		backend := openBackend(t, factory)
		ctx := context.Background()
		service := application.NewService(backend)
		stick := createStick(t, ctx, backend, "contract-generation", "Generation")
		holder := identity("holder", "Holder", "holder@example.com")
		original := identity("watcher", "Original Watcher", "original@example.com")

		held, err := service.ClaimStick(ctx, holder, stick.ID, "first hold", stick.Version)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.Subscribe(ctx, original, stick.ID, held.Version); err != nil {
			t.Fatal(err)
		}
		released, err := service.ReleaseStick(ctx, holder, stick.ID, held.Version)
		if err != nil {
			t.Fatal(err)
		}

		firstDelivery := claimNotification(t, ctx, backend, "generation-one")
		if firstDelivery.RecipientName != original.Name || firstDelivery.RecipientEmail != original.Email {
			t.Fatalf("first delivery did not retain captured subscription: %+v", firstDelivery)
		}
		if firstDelivery.SubscriptionGenerationToken == "" {
			t.Fatal("first delivery has an empty subscription generation token")
		}

		if err := service.Unsubscribe(ctx, original, stick.ID, released.Version); err != nil {
			t.Fatal(err)
		}
		updated := identity(original.Sub, "Updated Watcher", "updated@example.com")
		if err := service.Subscribe(ctx, updated, stick.ID, released.Version); err != nil {
			t.Fatal(err)
		}
		if err := backend.MarkNotificationSucceeded(ctx, firstDelivery, outboxClock.Add(time.Minute)); err != nil {
			t.Fatalf("MarkNotificationSucceeded first generation: %v", err)
		}
		assertSubscribed(t, ctx, backend, updated.Sub, stick.ID, true)

		held, err = service.ClaimStick(ctx, holder, stick.ID, "second hold", released.Version)
		if err != nil {
			t.Fatal(err)
		}
		released, err = service.ReleaseStick(ctx, holder, stick.ID, held.Version)
		if err != nil {
			t.Fatal(err)
		}
		secondDelivery := claimNotification(t, ctx, backend, "generation-two")
		if secondDelivery.RecipientName != updated.Name || secondDelivery.RecipientEmail != updated.Email ||
			secondDelivery.SubscriptionGenerationToken == "" ||
			secondDelivery.SubscriptionGenerationToken == firstDelivery.SubscriptionGenerationToken {
			t.Fatalf("resubscription was not protected by a new generation token: first=%+v second=%+v", firstDelivery, secondDelivery)
		}
		if err := backend.MarkNotificationSucceeded(ctx, secondDelivery, outboxClock.Add(2*time.Minute)); err != nil {
			t.Fatalf("MarkNotificationSucceeded current generation: %v", err)
		}
		assertSubscribed(t, ctx, backend, updated.Sub, stick.ID, false)
	})

	t.Run("unit of work rolls back callback error", func(t *testing.T) {
		backend := openBackend(t, factory)
		ctx := context.Background()
		stick, err := domain.NewStick("contract-uow-rollback", "Rolled back")
		if err != nil {
			t.Fatal(err)
		}
		injected := errors.New("injected callback failure")

		err = backend.WithinTransaction(ctx, func(tx application.Transaction) error {
			if err := tx.CreateStick(ctx, stick); err != nil {
				return err
			}
			return injected
		})
		if !errors.Is(err, injected) {
			t.Fatalf("WithinTransaction error = %v, want injected error", err)
		}
		if _, err := backend.GetStick(ctx, stick.ID); !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("rolled-back stick GetStick error = %v, want ErrNotFound", err)
		}
	})

	t.Run("release and outbox work roll back atomically", func(t *testing.T) {
		backend := openBackend(t, factory)
		ctx := context.Background()
		service := application.NewService(backend)
		stick := createStick(t, ctx, backend, "contract-release-rollback", "Atomic release")
		holder := identity("holder", "Holder", "holder@example.com")
		watcher := identity("watcher", "Watcher", "watcher@example.com")
		secondWatcher := identity("second-watcher", "Second watcher", "second-watcher@example.com")

		held, err := service.ClaimStick(ctx, holder, stick.ID, "must roll back", stick.Version)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.Subscribe(ctx, watcher, stick.ID, held.Version); err != nil {
			t.Fatal(err)
		}
		if err := service.Subscribe(ctx, secondWatcher, stick.ID, held.Version); err != nil {
			t.Fatal(err)
		}
		before, err := backend.GetStick(ctx, stick.ID)
		if err != nil {
			t.Fatal(err)
		}
		injected := errors.New("injected after bulk outbox enqueue")
		releasedAt := outboxClock.Add(-24 * time.Hour)

		err = backend.WithinTransaction(ctx, func(tx application.Transaction) error {
			current, err := tx.GetStick(ctx, stick.ID)
			if err != nil {
				return err
			}
			next, err := domain.Release(current, holder.Sub)
			if err != nil {
				return err
			}
			if err := tx.SaveStick(ctx, next, current.Version); err != nil {
				return err
			}
			if err := tx.CloseSession(ctx, stick.ID, holder.Sub, releasedAt); err != nil {
				return err
			}
			if err := tx.EnqueueReleaseNotifications(ctx, current, releasedAt); err != nil {
				return err
			}
			return injected
		})
		if !errors.Is(err, injected) {
			t.Fatalf("release transaction error = %v, want injected error", err)
		}

		after, err := backend.GetStick(ctx, stick.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("release state was not rolled back: before=%+v after=%+v", before, after)
		}
		_, count, err := backend.GetHistory(ctx, stick.ID, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("rolled-back release completed %d sessions", count)
		}
		delivery, err := backend.ClaimNotification(ctx, outboxClock, outboxClock.Add(-time.Hour), "rollback-check")
		if err != nil {
			t.Fatal(err)
		}
		if delivery != nil {
			t.Fatalf("rolled-back outbox entry was claimable: %+v", delivery)
		}
		assertSubscribed(t, ctx, backend, watcher.Sub, stick.ID, true)
	})

	t.Run("outbox claim ownership and stale reclaim", func(t *testing.T) {
		backend := openBackend(t, factory)
		ctx := context.Background()
		service := application.NewService(backend)
		stick := createStick(t, ctx, backend, "contract-outbox-claim", "Outbox claims")
		holder := identity("holder", "Holder", "holder@example.com")
		watcher := identity("watcher", "Watcher", "watcher@example.com")

		held, err := service.ClaimStick(ctx, holder, stick.ID, "claim ownership", stick.Version)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.Subscribe(ctx, watcher, stick.ID, held.Version); err != nil {
			t.Fatal(err)
		}
		if _, err := service.ReleaseStick(ctx, holder, stick.ID, held.Version); err != nil {
			t.Fatal(err)
		}

		first := claimNotification(t, ctx, backend, "worker-one")
		if first.ClaimToken != "worker-one" || first.Attempts != 1 {
			t.Fatalf("first claim metadata = %+v", first)
		}
		unavailable, err := backend.ClaimNotification(ctx, outboxClock, outboxClock.Add(-time.Hour), "worker-two-early")
		if err != nil {
			t.Fatal(err)
		}
		if unavailable != nil {
			t.Fatalf("active claim was concurrently claimable: %+v", unavailable)
		}

		wrongToken := first
		wrongToken.ClaimToken = "not-the-owner"
		if err := backend.MarkNotificationFailed(ctx, wrongToken, outboxClock.Add(time.Hour), "wrong owner"); !errors.Is(err, outbox.ErrClaimLost) {
			t.Fatalf("wrong-token failure result = %v, want ErrClaimLost", err)
		}

		reclaimNow := outboxClock.Add(2 * time.Hour)
		reclaimed, err := backend.ClaimNotification(ctx, reclaimNow, outboxClock.Add(time.Hour), "worker-two")
		if err != nil {
			t.Fatalf("stale ClaimNotification: %v", err)
		}
		if reclaimed == nil {
			t.Fatal("stale ClaimNotification returned no delivery")
		}
		if reclaimed.ID != first.ID || reclaimed.ClaimToken != "worker-two" || reclaimed.Attempts != first.Attempts+1 {
			t.Fatalf("stale reclaim metadata: first=%+v reclaimed=%+v", first, reclaimed)
		}
		if err := backend.MarkNotificationSucceeded(ctx, first, reclaimNow); !errors.Is(err, outbox.ErrClaimLost) {
			t.Fatalf("old owner success result = %v, want ErrClaimLost", err)
		}
		if err := backend.MarkNotificationFailed(ctx, first, reclaimNow.Add(time.Hour), "old owner"); !errors.Is(err, outbox.ErrClaimLost) {
			t.Fatalf("old owner failure result = %v, want ErrClaimLost", err)
		}
		if err := backend.MarkNotificationSucceeded(ctx, *reclaimed, reclaimNow); err != nil {
			t.Fatalf("current owner success result: %v", err)
		}
	})

	t.Run("concurrent outbox claimers have one owner", func(t *testing.T) {
		backend := openBackend(t, factory)
		ctx := context.Background()
		service := application.NewService(backend)
		stick := createStick(t, ctx, backend, "contract-outbox-concurrent", "Concurrent claims")
		holder := identity("holder", "Holder", "holder@example.com")
		watcher := identity("watcher", "Watcher", "watcher@example.com")

		held, err := service.ClaimStick(ctx, holder, stick.ID, "claim once", stick.Version)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.Subscribe(ctx, watcher, stick.ID, held.Version); err != nil {
			t.Fatal(err)
		}
		if _, err := service.ReleaseStick(ctx, holder, stick.ID, held.Version); err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		results := make(chan struct {
			token    string
			delivery *outbox.Delivery
			err      error
		}, 2)
		for _, token := range []string{"concurrent-one", "concurrent-two"} {
			go func(token string) {
				<-start
				delivery, err := backend.ClaimNotification(ctx, outboxClock, outboxClock.Add(-time.Hour), token)
				results <- struct {
					token    string
					delivery *outbox.Delivery
					err      error
				}{token: token,
					delivery: delivery,
					err:      err,
				}
			}(token)
		}
		close(start)

		var owner *outbox.Delivery
		for range 2 {
			result := <-results
			if result.err != nil {
				t.Fatalf("claim %s: %v", result.token, result.err)
			}
			if result.delivery == nil {
				continue
			}
			if owner != nil {
				t.Fatalf("notification was claimed twice: first=%+v second=%+v", owner, result.delivery)
			}
			owner = result.delivery
		}
		if owner == nil {
			t.Fatal("concurrent claimers had no owner")
		}
		if err := backend.MarkNotificationSucceeded(ctx, *owner, outboxClock.Add(time.Hour)); err != nil {
			t.Fatalf("mark concurrent owner succeeded: %v", err)
		}
	})
}

func openBackend(t *testing.T, factory Factory) Backend {
	t.Helper()
	backend := factory(t)
	if backend == nil {
		t.Fatal("backend factory returned nil")
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close backend: %v", err)
		}
	})
	if err := backend.PingContext(context.Background()); err != nil {
		t.Fatalf("ping backend: %v", err)
	}
	return backend
}

func createStick(t *testing.T, ctx context.Context, backend Backend, id, name string) domain.Stick {
	t.Helper()
	stick, err := domain.NewStick(id, name)
	if err != nil {
		t.Fatalf("NewStick: %v", err)
	}
	if err := backend.WithinTransaction(ctx, func(tx application.Transaction) error {
		return tx.CreateStick(ctx, stick)
	}); err != nil {
		t.Fatalf("CreateStick: %v", err)
	}
	return stick
}

func rename(t *testing.T, ctx context.Context, service *application.Service, stick domain.Stick, name string) domain.Stick {
	t.Helper()
	renamed, err := service.RenameStick(ctx, admin(), stick.ID, name, stick.Version)
	if err != nil {
		t.Fatalf("RenameStick: %v", err)
	}
	return renamed
}

func claimNotification(t *testing.T, ctx context.Context, backend Backend, token string) outbox.Delivery {
	t.Helper()
	delivery, err := backend.ClaimNotification(ctx, outboxClock, outboxClock.Add(-time.Hour), token)
	if err != nil {
		t.Fatalf("ClaimNotification: %v", err)
	}
	if delivery == nil {
		t.Fatal("ClaimNotification returned no delivery")
	}
	if delivery.ClaimToken != token {
		t.Fatalf("ClaimNotification token = %q, want %q", delivery.ClaimToken, token)
	}
	return *delivery
}

func assertSticks(t *testing.T, sticks []domain.Stick, want map[string]string) {
	t.Helper()
	if len(sticks) != len(want) {
		t.Fatalf("stick count = %d, want %d: %+v", len(sticks), len(want), sticks)
	}
	for _, stick := range sticks {
		name, ok := want[stick.ID]
		if !ok || stick.Name != name {
			t.Fatalf("unexpected stick in list: %+v; want %v", stick, want)
		}
	}
}

func assertReasons(t *testing.T, sessions []domain.Session, reasons ...string) {
	t.Helper()
	if len(sessions) != len(reasons) {
		t.Fatalf("history length = %d, want %d: %+v", len(sessions), len(reasons), sessions)
	}
	for i, reason := range reasons {
		if sessions[i].Reason != reason {
			t.Fatalf("history reason %d = %q, want %q", i, sessions[i].Reason, reason)
		}
	}
}

func assertSubscribed(t *testing.T, ctx context.Context, backend Backend, subject, stickID string, want bool) {
	t.Helper()
	ids, err := backend.SubscribedStickIDs(ctx, subject)
	if err != nil {
		t.Fatalf("SubscribedStickIDs: %v", err)
	}
	found := false
	for _, id := range ids {
		if id == stickID {
			found = true
		}
	}
	if found != want {
		t.Fatalf("subscription for %q on %q present=%t, want %t (all IDs: %v)", subject, stickID, found, want, ids)
	}
}

func assertVersion(t *testing.T, stick domain.Stick, want int64) {
	t.Helper()
	if stick.Version != want {
		t.Fatalf("stick version = %d, want %d: %+v", stick.Version, want, stick)
	}
}

func assertError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func identity(sub, name, email string) domain.Identity {
	return domain.Identity{Sub: sub, Name: name, Email: email, EmailVerified: true}
}

func admin() domain.Identity {
	return domain.Identity{Sub: "admin", Name: "Admin", Email: "admin@example.com", EmailVerified: true, IsAdmin: true}
}
