package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	app "stick/internal/application"
)

func TestRunCancelsSiblingAndReturnsComponentError(t *testing.T) {
	wantErr := errors.New("web stopped")
	siblingStopped := make(chan struct{})
	webComponent := testComponent{run: func(context.Context) error { return wantErr }}
	worker := testComponent{run: func(ctx context.Context) error {
		<-ctx.Done()
		close(siblingStopped)
		return nil
	}}

	if err := app.Run(context.Background(), webComponent, worker); !errors.Is(err, wantErr) {
		t.Fatalf("Run error = %v, want %v", err, wantErr)
	}
	select {
	case <-siblingStopped:
	case <-time.After(time.Second):
		t.Fatal("sibling component was not canceled")
	}
}

func TestRunWithNoComponentsReturnsImmediately(t *testing.T) {
	if err := app.Run(context.Background()); err != nil {
		t.Fatalf("Run error = %v, want nil", err)
	}
}

func TestRunStopsAllComponentsOnParentCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	stopped := make(chan struct{}, 3)
	component := func() testComponent {
		return testComponent{run: func(ctx context.Context) error {
			<-ctx.Done()
			stopped <- struct{}{}
			return nil
		}}
	}
	done := make(chan error, 1)
	go func() { done <- app.Run(parent, component(), component(), component()) }()
	cancelParent()
	if err := <-done; err != nil {
		t.Fatalf("Run error = %v, want nil", err)
	}
	for range 3 {
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Fatal("component was not stopped")
		}
	}
}

type testComponent struct {
	run func(context.Context) error
}

func (c testComponent) Run(ctx context.Context) error { return c.run(ctx) }
