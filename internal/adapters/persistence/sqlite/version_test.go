package sqlite

import (
	"context"
	"errors"
	"sync"
	"testing"

	app "stick/internal/application"
	domain "stick/internal/core"
)

func TestExpectedVersionCASAllowsOnlyOneConcurrentMutation(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	createTestStick(t, store, "aa001", "original")
	service := app.NewService(store)
	admin := domain.Identity{Sub: "admin", IsAdmin: true}

	start := make(chan struct{})
	results := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, name := range []string{"first", "second"} {
		go func(name string) {
			ready.Done()
			<-start
			_, err := service.RenameStick(ctx, admin, "aa001", name, 1)
			results <- err
		}(name)
	}
	ready.Wait()
	close(start)

	var succeeded, conflicted int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, app.ErrVersionConflict):
			conflicted++
		default:
			t.Fatalf("concurrent rename error = %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent results: succeeded=%d conflicted=%d", succeeded, conflicted)
	}
	stick, err := store.GetStick(ctx, "aa001")
	if err != nil {
		t.Fatal(err)
	}
	if stick.Version != 2 {
		t.Fatalf("version after concurrent CAS = %d, want 2", stick.Version)
	}
}

func TestStickVersionLifecycleExcludesSubscriptions(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	createTestStick(t, store, "aa001", "prod")
	service := app.NewService(store)
	admin := domain.Identity{Sub: "admin", IsAdmin: true}
	user := domain.Identity{Sub: "u1", Name: "Alice", Email: "alice@example.com"}

	stick, _ := store.GetStick(ctx, "aa001")
	if stick.Version != 1 {
		t.Fatalf("created version = %d, want 1", stick.Version)
	}
	stick, err := service.RenameStick(ctx, admin, stick.ID, "renamed", stick.Version)
	if err != nil {
		t.Fatal(err)
	}
	stick, err = service.ClaimStick(ctx, user, stick.ID, "working", stick.Version)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Subscribe(ctx, domain.Identity{Sub: "watcher"}, stick.ID, stick.Version); err != nil {
		t.Fatal(err)
	}
	afterSubscribe, err := store.GetStick(ctx, stick.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterSubscribe.Version != stick.Version {
		t.Fatalf("subscription changed version from %d to %d", stick.Version, afterSubscribe.Version)
	}
	stick, err = service.ReleaseStick(ctx, user, stick.ID, stick.Version)
	if err != nil {
		t.Fatal(err)
	}
	stick, err = service.ArchiveStick(ctx, admin, stick.ID, stick.Version)
	if err != nil {
		t.Fatal(err)
	}
	stick, err = service.UnarchiveStick(ctx, admin, stick.ID, stick.Version)
	if err != nil {
		t.Fatal(err)
	}
	if stick.Version != 6 {
		t.Fatalf("lifecycle version = %d, want 6", stick.Version)
	}
}

func TestSaveStickRejectsStaleVersionAtPersistenceBoundary(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	createTestStick(t, store, "aa001", "original")
	stale, err := store.GetStick(ctx, "aa001")
	if err != nil {
		t.Fatal(err)
	}
	service := app.NewService(store)
	if _, err := service.RenameStick(ctx, domain.Identity{IsAdmin: true}, stale.ID, "current", stale.Version); err != nil {
		t.Fatal(err)
	}
	staleUpdate, err := domain.Rename(stale, "stale")
	if err != nil {
		t.Fatal(err)
	}
	err = store.WithinTransaction(ctx, func(tx app.Transaction) error {
		return tx.SaveStick(ctx, staleUpdate, stale.Version)
	})
	if !errors.Is(err, app.ErrVersionConflict) {
		t.Fatalf("SaveStick stale error = %v, want ErrVersionConflict", err)
	}
	current, err := store.GetStick(ctx, stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Name != "current" || current.Version != 2 {
		t.Fatalf("stale persistence write changed stick: %+v", current)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func createTestStick(t *testing.T, store *Store, id, name string) domain.Stick {
	t.Helper()
	stick, err := domain.NewStick(id, name)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.WithinTransaction(ctx, func(tx app.Transaction) error {
		return tx.CreateStick(ctx, stick)
	}); err != nil {
		t.Fatal(err)
	}
	return stick
}
