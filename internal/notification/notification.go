// Package notification defines transport-neutral notification delivery contracts.
package notification

import (
	"context"
	"errors"
	"fmt"
)

// Notification holds the transport-neutral data available to notification backends.
type Notification struct {
	StickID        string
	StickName      string
	HolderName     string
	HolderEmail    string
	Duration       string
	ReleasedAt     string
	BaseURL        string
	RecipientName  string
	RecipientEmail string
}

// Notifier sends a notification. Implementations must honor context
// cancellation and deadlines and return promptly when the context is done.
type Notifier interface {
	Notify(ctx context.Context, notification Notification) error
}

// NotifierFunc is a function that implements Notifier.
type NotifierFunc func(ctx context.Context, notification Notification) error

// Notify invokes f with the supplied notification.
func (f NotifierFunc) Notify(ctx context.Context, notification Notification) error {
	return f(ctx, notification)
}

type noopNotifier struct{}

func (noopNotifier) Notify(context.Context, Notification) error { return nil }

// Noop returns a Notifier that silently discards all notifications.
func Noop() Notifier { return noopNotifier{} }

type namedNotifier struct {
	name     string
	notifier Notifier
}

func (n namedNotifier) Notify(ctx context.Context, notification Notification) error {
	if err := n.notifier.Notify(ctx, notification); err != nil {
		return fmt.Errorf("%s: %w", n.name, err)
	}
	return nil
}

// Named wraps a notifier so its runtime errors identify the backend.
func Named(name string, notifier Notifier) Notifier {
	return namedNotifier{name: name, notifier: notifier}
}

type fanoutNotifier struct {
	notifiers []Notifier
}

// Multi returns a notifier that sends each notification to every supplied
// backend. All backends are attempted even when one fails; the returned error
// contains every failure so one unavailable backend does not prevent the
// others from receiving the notification.
func Multi(notifiers ...Notifier) Notifier {
	filtered := make([]Notifier, 0, len(notifiers))
	for _, notifier := range notifiers {
		if notifier != nil {
			filtered = append(filtered, notifier)
		}
	}
	switch len(filtered) {
	case 0:
		return nil
	case 1:
		return filtered[0]
	default:
		return fanoutNotifier{notifiers: filtered}
	}
}

// Fanout is an alias for Multi.
func Fanout(notifiers ...Notifier) Notifier { return Multi(notifiers...) }

func (n fanoutNotifier) Notify(ctx context.Context, notification Notification) error {
	var failures []error
	for _, notifier := range n.notifiers {
		if err := notifier.Notify(ctx, notification); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
