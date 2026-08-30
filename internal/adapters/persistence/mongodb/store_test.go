package mongodb

import (
	"context"
	"errors"
	"testing"

	"stick/internal/application"
	domain "stick/internal/core"
	"stick/internal/outbox"
)

func TestContextError(t *testing.T) {
	var nilContext context.Context
	if err := contextError(nilContext); err == nil {
		t.Fatal("nil context unexpectedly accepted")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(contextError(canceled), context.Canceled) {
		t.Fatalf("canceled context error = %v, want context.Canceled", contextError(canceled))
	}
	if err := contextError(context.Background()); err != nil {
		t.Fatalf("background context error = %v, want nil", err)
	}
}

func TestWrapStoreErrorPreservesSentinels(t *testing.T) {
	for _, sentinel := range []error{
		application.ErrNotFound,
		application.ErrAlreadyExists,
		application.ErrVersionConflict,
		domain.ErrAlreadyHeld,
		domain.ErrNotHolder,
		domain.ErrAlreadyArchived,
		domain.ErrNotArchived,
		domain.ErrHeld,
		domain.ErrVersionExhausted,
		outbox.ErrClaimLost,
	} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			if got := wrapStoreError("operation", sentinel); !errors.Is(got, sentinel) {
				t.Fatalf("wrapped error = %v, does not preserve %v", got, sentinel)
			}
		})
	}
	if wrapStoreError("operation", nil) != nil {
		t.Fatal("nil error was wrapped")
	}
	wrapped := wrapStoreError("operation", errors.New("database failed"))
	if wrapped == nil || wrapped.Error() != "operation: database failed" {
		t.Fatalf("unexpected wrapped error: %v", wrapped)
	}
}
