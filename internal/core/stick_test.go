package core_test

import (
	"errors"
	"math"
	"testing"
	"time"

	domain "stick/internal/core"
)

func TestStickTransitionsIncrementVersionExactlyOnce(t *testing.T) {
	now := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	identity := domain.Identity{Sub: "u1", Name: "Alice", Email: "alice@example.com"}
	stick, err := domain.NewStick("aa001", "Prod")
	if err != nil || stick.Version != 1 {
		t.Fatalf("NewStick = %+v, %v", stick, err)
	}

	stick, err = domain.Rename(stick, "Production")
	assertVersion(t, stick, err, 2)
	stick, err = domain.Claim(stick, identity, "deploying", now)
	assertVersion(t, stick, err, 3)
	stick, err = domain.Release(stick, identity.Sub)
	assertVersion(t, stick, err, 4)
	stick, err = domain.Archive(stick, now)
	assertVersion(t, stick, err, 5)
	stick, err = domain.Unarchive(stick)
	assertVersion(t, stick, err, 6)
}

func TestStickTransitionsRejectInvalidStateWithoutChangingInput(t *testing.T) {
	held := domain.Stick{ID: "aa001", Version: 4, Holder: &domain.Holder{Sub: "u1"}}
	if _, err := domain.Archive(held, time.Now()); !errors.Is(err, domain.ErrHeld) {
		t.Fatalf("Archive held error = %v", err)
	}
	if held.Version != 4 || held.Holder == nil {
		t.Fatalf("failed transition mutated input: %+v", held)
	}
	if _, err := domain.Release(held, "other"); !errors.Is(err, domain.ErrNotHolder) {
		t.Fatalf("Release non-holder error = %v", err)
	}
}

func TestStickTransitionRejectsVersionOverflow(t *testing.T) {
	stick := domain.Stick{ID: "aa001", Name: "Prod", Version: math.MaxInt64}
	if _, err := domain.Rename(stick, "New"); !errors.Is(err, domain.ErrVersionExhausted) {
		t.Fatalf("Rename overflow error = %v", err)
	}
}

func TestStickStateIsDerivedFromHolderAndArchivedAt(t *testing.T) {
	stick := domain.Stick{}
	if !stick.Available() || stick.Archived() {
		t.Fatalf("zero-value lifecycle state = available %t, archived %t", stick.Available(), stick.Archived())
	}
	stick.Holder = &domain.Holder{Sub: "u1"}
	archivedAt := time.Now().UTC()
	stick.ArchivedAt = &archivedAt
	if stick.Available() || !stick.Archived() {
		t.Fatalf("derived lifecycle state = available %t, archived %t", stick.Available(), stick.Archived())
	}
}

func assertVersion(t *testing.T, stick domain.Stick, err error, want int64) {
	t.Helper()
	if err != nil || stick.Version != want {
		t.Fatalf("transition = %+v, %v; want version %d", stick, err, want)
	}
}
