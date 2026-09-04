package notification_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	notification "stick/internal/notification"
)

func TestNoopNotifier(t *testing.T) {
	notifier := notification.Noop()
	if err := notifier.Notify(context.Background(), notification.Notification{}); err != nil {
		t.Errorf("Noop should never error, got %v", err)
	}
}

func TestNamedNotifierAttributesErrorAndPreservesCause(t *testing.T) {
	failure := errors.New("connection refused")
	notifier := notification.Named("smtp", notification.NotifierFunc(func(context.Context, notification.Notification) error {
		return failure
	}))

	err := notifier.Notify(context.Background(), notification.Notification{})
	if !errors.Is(err, failure) {
		t.Fatalf("Named should preserve the cause, got %v", err)
	}
	if !strings.Contains(err.Error(), "smtp: connection refused") {
		t.Fatalf("Named error = %q, want backend attribution", err)
	}
}

func TestFanoutNotifierAttemptsEveryBackendAndJoinsErrors(t *testing.T) {
	firstFailure := errors.New("first failure")
	secondFailure := errors.New("second failure")
	var calls []string
	notifier := notification.Fanout(
		notification.Named("first", notification.NotifierFunc(func(context.Context, notification.Notification) error {
			calls = append(calls, "first")
			return firstFailure
		})),
		notification.Named("second", notification.NotifierFunc(func(context.Context, notification.Notification) error {
			calls = append(calls, "second")
			return secondFailure
		})),
	)

	err := notifier.Notify(context.Background(), notification.Notification{})
	if !errors.Is(err, firstFailure) || !errors.Is(err, secondFailure) {
		t.Fatalf("Fanout error = %v, want both causes", err)
	}
	if strings.Join(calls, ",") != "first,second" {
		t.Fatalf("backend calls = %v, want both backends in order", calls)
	}
}
