// Package outbox delivers persisted notifications with retries.
package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"stick/internal/application"
	"stick/internal/format"
)

const (
	defaultPollInterval   = time.Second
	defaultAttemptTimeout = 30 * time.Second
	defaultInitialBackoff = time.Second
	defaultMaxBackoff     = 5 * time.Minute
	defaultClaimTTL       = time.Minute
	resultTimeout         = 5 * time.Second
	maxFailureLength      = 4096
)

// WorkerOptions controls durable delivery. Zero values use production-safe
// defaults; the timing fields are configurable primarily for deterministic tests.
type WorkerOptions struct {
	BaseURL        string
	Location       *time.Location
	Now            func() time.Time
	PollInterval   time.Duration
	AttemptTimeout time.Duration
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	ClaimTTL       time.Duration
}

// Worker claims and delivers persisted notification records.
type Worker struct {
	store    Store
	notifier application.Notifier
	options  WorkerOptions
}

// NewWorker returns a notification worker with normalized options.
func NewWorker(store Store, notifier application.Notifier, options WorkerOptions) *Worker {
	if notifier == nil {
		notifier = application.Noop()
	}
	if options.Location == nil {
		options.Location = time.UTC
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.PollInterval <= 0 {
		options.PollInterval = defaultPollInterval
	}
	if options.AttemptTimeout <= 0 {
		options.AttemptTimeout = defaultAttemptTimeout
	}
	if options.InitialBackoff <= 0 {
		options.InitialBackoff = defaultInitialBackoff
	}
	if options.MaxBackoff <= 0 {
		options.MaxBackoff = defaultMaxBackoff
	}
	if options.MaxBackoff < options.InitialBackoff {
		options.MaxBackoff = options.InitialBackoff
	}
	if options.ClaimTTL <= 0 {
		options.ClaimTTL = defaultClaimTTL
	}
	return &Worker{store: store, notifier: notifier, options: options}
}

// Run processes notifications until ctx is canceled. The caller owns the
// goroutine and lifecycle.
func (w *Worker) Run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		worked, err := w.processOne(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.ErrorContext(ctx, "notification worker failed", "operation", "deliver notification", "error", err)
		}
		if ctx.Err() != nil {
			return nil
		}
		if worked {
			continue
		}
		timer := time.NewTimer(w.options.PollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

func (w *Worker) processOne(ctx context.Context) (bool, error) {
	now := w.options.Now().UTC()
	delivery, err := w.store.ClaimNotification(ctx, now, now.Add(-w.options.ClaimTTL), uuid.NewV7().String())
	if err != nil {
		return false, err
	}
	if delivery == nil {
		return false, nil
	}

	attemptCtx, cancel := context.WithTimeout(ctx, w.options.AttemptTimeout)
	err = w.notifier.Notify(attemptCtx, w.notification(*delivery))
	cancel()

	resultCtx, resultCancel := context.WithTimeout(context.Background(), resultTimeout)
	defer resultCancel()
	if err == nil {
		if markErr := w.store.MarkNotificationSucceeded(resultCtx, *delivery, w.options.Now().UTC()); markErr != nil {
			return true, fmt.Errorf("record notification %d success: %w", delivery.ID, markErr)
		}
		return true, nil
	}

	nextAttempt := w.options.Now().UTC().Add(w.backoff(delivery.Attempts))
	failure := err.Error()
	if len(failure) > maxFailureLength {
		failure = failure[:maxFailureLength]
	}
	if markErr := w.store.MarkNotificationFailed(resultCtx, *delivery, nextAttempt, failure); markErr != nil {
		return true, fmt.Errorf("record notification %d failure: %w", delivery.ID, markErr)
	}
	return true, fmt.Errorf("deliver notification %d attempt %d: %w", delivery.ID, delivery.Attempts, err)
}

func (w *Worker) notification(delivery Delivery) application.Notification {
	duration := delivery.ReleasedAt.Sub(delivery.HeldSince).Round(time.Minute)
	return application.Notification{
		StickID:        delivery.StickID,
		StickName:      delivery.StickName,
		HolderName:     delivery.HolderName,
		HolderEmail:    delivery.HolderEmail,
		Duration:       format.Duration(duration),
		ReleasedAt:     delivery.ReleasedAt.In(w.options.Location).Format("Jan 2 · 15:04"),
		BaseURL:        w.options.BaseURL,
		RecipientName:  delivery.RecipientName,
		RecipientEmail: delivery.RecipientEmail,
	}
}

func (w *Worker) backoff(attempt int) time.Duration {
	delay := w.options.InitialBackoff
	for i := 1; i < attempt && delay < w.options.MaxBackoff; i++ {
		if delay > w.options.MaxBackoff/2 {
			return w.options.MaxBackoff
		}
		delay *= 2
	}
	if delay > w.options.MaxBackoff {
		return w.options.MaxBackoff
	}
	return delay
}
