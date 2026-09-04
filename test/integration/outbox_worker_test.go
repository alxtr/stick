package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"stick/internal/adapters/notification/teams"
	"stick/internal/adapters/notification/webhook"
	"stick/internal/adapters/persistence/sqlite"
	"stick/internal/application"
	core "stick/internal/core"
	"stick/internal/notification"
	"stick/internal/outbox"
)

func TestWorkerSuccessfulDeliveryRecordsOutcomeAndCleansSubscription(t *testing.T) {
	store, conn := preparedDelivery(t)
	delivered := make(chan notification.Notification, 1)
	worker := testWorker(store, notification.NotifierFunc(func(_ context.Context, notification notification.Notification) error {
		delivered <- notification
		return nil
	}))
	cancel, done := startWorker(worker)
	t.Cleanup(func() { stopWorker(t, cancel, done) })

	waitForOutbox(t, conn, "succeeded", 1)
	select {
	case notification := <-delivered:
		if notification.StickName != "prod-deploy" || notification.HolderName != "Alice" ||
			notification.RecipientEmail != "bob@example.com" || notification.BaseURL != "https://example.com/stick" {
			t.Fatalf("notification snapshot = %+v", notification)
		}
	default:
		t.Fatal("successful row recorded without notifier call")
	}
	subscribers, err := application.NewService(store).SubscribedStickIDs(context.Background(), core.Identity{Sub: "watcher-sub"})
	if err != nil {
		t.Fatal(err)
	}
	if len(subscribers) != 0 {
		t.Fatalf("successful delivery retained subscriptions: %+v", subscribers)
	}
	var deliveredAtValid int
	if err := conn.QueryRow(`SELECT delivered_at IS NOT NULL FROM notification_outbox`).Scan(&deliveredAtValid); err != nil {
		t.Fatal(err)
	}
	if deliveredAtValid != 1 {
		t.Fatal("successful delivery outcome was not retained")
	}
}

func TestWorkerDeliversTeamsNotificationAndRecordsOutcome(t *testing.T) {
	store, conn := preparedDelivery(t)
	received := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read request body", http.StatusInternalServerError)
			return
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		received <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	notifier, err := teams.New(teams.Config{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	worker := testWorker(store, notifier)
	cancel, done := startWorker(worker)
	t.Cleanup(func() { stopWorker(t, cancel, done) })

	waitForOutbox(t, conn, "succeeded", 1)
	select {
	case payload := <-received:
		if payload["@type"] != "MessageCard" || payload["title"] != "prod-deploy is available" {
			t.Fatalf("Teams payload = %v", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("Teams notification was not delivered")
	}
	subscribers, err := application.NewService(store).SubscribedStickIDs(context.Background(), core.Identity{Sub: "watcher-sub"})
	if err != nil {
		t.Fatal(err)
	}
	if len(subscribers) != 0 {
		t.Fatalf("successful Teams delivery retained subscription: %v", subscribers)
	}
}

func TestWorkerDeliversToMultipleInstancesOfOneBackend(t *testing.T) {
	store, conn := preparedDelivery(t)
	received := make(chan string, 2)
	newWebhook := func(name string) notification.Notifier {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			received <- name
			w.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(server.Close)
		notifier, err := webhook.New(webhook.Config{URL: server.URL})
		if err != nil {
			t.Fatal(err)
		}
		return notifier
	}

	worker := testWorker(store, notification.Multi(
		newWebhook("first webhook"),
		newWebhook("second webhook"),
	))
	cancel, done := startWorker(worker)
	t.Cleanup(func() { stopWorker(t, cancel, done) })

	waitForOutbox(t, conn, "succeeded", 1)
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case name := <-received:
			got[name] = true
		case <-time.After(time.Second):
			t.Fatalf("webhook instance %d was not called", i+1)
		}
	}
	if len(got) != 2 {
		t.Fatalf("webhook deliveries = %v, want both instances", got)
	}
}

func TestWorkerRetriesFailureThenSucceeds(t *testing.T) {
	store, conn := preparedDelivery(t)
	var calls atomic.Int32
	worker := testWorker(store, notification.NotifierFunc(func(context.Context, notification.Notification) error {
		if calls.Add(1) == 1 {
			return errors.New("temporary SMTP failure")
		}
		return nil
	}))
	cancel, done := startWorker(worker)
	t.Cleanup(func() { stopWorker(t, cancel, done) })

	waitForOutbox(t, conn, "succeeded", 2)
	if calls.Load() != 2 {
		t.Fatalf("notifier calls = %d, want 2", calls.Load())
	}
	var lastError string
	if err := conn.QueryRow(`SELECT last_error FROM notification_outbox`).Scan(&lastError); err != nil {
		t.Fatal(err)
	}
	if lastError != "temporary SMTP failure" {
		t.Fatalf("last failure = %q", lastError)
	}
}

func TestWorkerRecoversStaleClaimAfterRestart(t *testing.T) {
	store, conn := preparedDelivery(t)
	now := time.Now().UTC()
	abandoned, err := store.ClaimNotification(context.Background(), now, now.Add(-time.Minute), "abandoned-worker")
	if err != nil {
		t.Fatal(err)
	}
	if abandoned == nil || abandoned.Attempts != 1 {
		t.Fatalf("abandoned claim = %+v", abandoned)
	}
	if _, err := conn.Exec(`UPDATE notification_outbox SET claimed_at=? WHERE id=?`, now.Add(-time.Hour), abandoned.ID); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	worker := testWorker(store, notification.NotifierFunc(func(context.Context, notification.Notification) error {
		calls.Add(1)
		return nil
	}))
	cancel, done := startWorker(worker)
	t.Cleanup(func() { stopWorker(t, cancel, done) })
	waitForOutbox(t, conn, "succeeded", 2)
	if calls.Load() != 1 {
		t.Fatalf("recovery notifier calls = %d, want 1", calls.Load())
	}
}

func TestWorkerRunCancellationStopsCooperativeInFlightNotifier(t *testing.T) {
	store, conn := preparedDelivery(t)
	started := make(chan struct{})
	worker := testWorker(store, notification.NotifierFunc(func(ctx context.Context, _ notification.Notification) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}))
	cancel, done := startWorker(worker)
	waitForSignal(t, started, "notification did not start")

	stopWorker(t, cancel, done)
	waitForOutbox(t, conn, "failed", 1)
	subscribers, err := application.NewService(store).SubscribedStickIDs(context.Background(), core.Identity{Sub: "watcher-sub"})
	if err != nil {
		t.Fatal(err)
	}
	if len(subscribers) != 1 {
		t.Fatalf("canceled delivery removed subscription: %+v", subscribers)
	}
}

func TestWorkerBoundsEachDeliveryAttempt(t *testing.T) {
	store, conn := preparedDelivery(t)
	timedOut := make(chan struct{})
	worker := outbox.NewWorker(store, notification.NotifierFunc(func(ctx context.Context, _ notification.Notification) error {
		<-ctx.Done()
		close(timedOut)
		return ctx.Err()
	}), outbox.WorkerOptions{
		PollInterval:   time.Millisecond,
		AttemptTimeout: 20 * time.Millisecond,
		InitialBackoff: time.Second,
		MaxBackoff:     time.Second,
		ClaimTTL:       time.Second,
	})
	cancel, done := startWorker(worker)
	waitForSignal(t, timedOut, "notification attempt did not time out")
	stopWorker(t, cancel, done)
	waitForOutbox(t, conn, "failed", 1)

	var failure string
	if err := conn.QueryRow(`SELECT last_error FROM notification_outbox`).Scan(&failure); err != nil {
		t.Fatal(err)
	}
	if failure != context.DeadlineExceeded.Error() {
		t.Fatalf("attempt failure = %q, want deadline exceeded", failure)
	}
}

func TestWorkerDoesNotDeleteNewerResubscription(t *testing.T) {
	store, conn := preparedDelivery(t)
	var capturedGeneration string
	if err := conn.QueryRow(`SELECT subscription_generation_token FROM notification_outbox`).Scan(&capturedGeneration); err != nil {
		t.Fatal(err)
	}
	stick := currentStick(t, store, "aa001")
	if err := application.NewService(store).Subscribe(context.Background(), core.Identity{
		Sub:   "watcher-sub",
		Name:  "Bob Updated",
		Email: "new@example.com",
	}, "aa001", stick.Version); err != nil {
		t.Fatal(err)
	}
	var currentGeneration string
	if err := conn.QueryRow(`
		SELECT generation_token FROM subscriptions
		WHERE stick_id='aa001' AND user_sub='watcher-sub'`).Scan(&currentGeneration); err != nil {
		t.Fatal(err)
	}
	if currentGeneration == capturedGeneration {
		t.Fatal("resubscription reused the captured generation token")
	}

	worker := testWorker(store, notification.Noop())
	cancel, done := startWorker(worker)
	t.Cleanup(func() { stopWorker(t, cancel, done) })
	waitForOutbox(t, conn, "succeeded", 1)
	subscribers, err := application.NewService(store).SubscribedStickIDs(context.Background(), core.Identity{Sub: "watcher-sub"})
	if err != nil {
		t.Fatal(err)
	}
	var name, email string
	if err := conn.QueryRow(`
		SELECT user_name, user_email FROM subscriptions
		WHERE stick_id='aa001' AND user_sub='watcher-sub'`).Scan(&name, &email); err != nil {
		t.Fatal(err)
	}
	if len(subscribers) != 1 || name != "Bob Updated" || email != "new@example.com" {
		t.Fatalf("new subscription was deleted or changed: ids=%v name=%q email=%q", subscribers, name, email)
	}
}

func TestWorkerUsesInjectedClockForDeliveryOutcome(t *testing.T) {
	store, conn := preparedDelivery(t)
	fixed := time.Date(2040, time.January, 2, 3, 4, 5, 0, time.UTC)
	worker := outbox.NewWorker(store, notification.Noop(), outbox.WorkerOptions{
		PollInterval:   time.Millisecond,
		AttemptTimeout: 100 * time.Millisecond,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
		ClaimTTL:       time.Second,
		Now:            func() time.Time { return fixed },
	})
	cancel, done := startWorker(worker)
	t.Cleanup(func() { stopWorker(t, cancel, done) })
	waitForOutbox(t, conn, "succeeded", 1)

	var deliveredAt time.Time
	if err := conn.QueryRow(`SELECT delivered_at FROM notification_outbox`).Scan(&deliveredAt); err != nil {
		t.Fatal(err)
	}
	if !deliveredAt.Equal(fixed) {
		t.Fatalf("delivered_at = %v, want %v", deliveredAt, fixed)
	}
	subscribers, err := application.NewService(store).SubscribedStickIDs(context.Background(), core.Identity{Sub: "watcher-sub"})
	if err != nil {
		t.Fatal(err)
	}
	if len(subscribers) != 0 {
		t.Fatalf("no-op delivery retained captured subscription: %v", subscribers)
	}
}

func preparedDelivery(t *testing.T) (*sqlite.Store, *sql.DB) {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "outbox.db")
	store, err := sqlite.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	conn, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	if _, err := conn.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	ctx := context.Background()
	holder := core.Identity{Sub: "holder-sub", Name: "Alice", Email: "alice@example.com"}
	createStick(t, ctx, store, "aa001", "prod-deploy")
	claimStick(t, store, "aa001", holder, "deploying")
	subscribeStick(t, store, "aa001", core.Identity{Sub: "watcher-sub", Name: "Bob", Email: "bob@example.com"})
	releaseStick(t, store, "aa001", holder)
	return store, conn
}

func currentStick(t *testing.T, store *sqlite.Store, id string) core.Stick {
	t.Helper()
	stick, err := store.GetStick(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return stick
}

func claimStick(t *testing.T, store *sqlite.Store, id string, identity core.Identity, reason string) {
	t.Helper()
	stick := currentStick(t, store, id)
	if _, err := application.NewService(store).ClaimStick(context.Background(), identity, id, reason, stick.Version); err != nil {
		t.Fatal(err)
	}
}

func subscribeStick(t *testing.T, store *sqlite.Store, id string, identity core.Identity) {
	t.Helper()
	stick := currentStick(t, store, id)
	if err := application.NewService(store).Subscribe(context.Background(), identity, id, stick.Version); err != nil {
		t.Fatal(err)
	}
}

func releaseStick(t *testing.T, store *sqlite.Store, id string, identity core.Identity) {
	t.Helper()
	stick := currentStick(t, store, id)
	if _, err := application.NewService(store).ReleaseStick(context.Background(), identity, id, stick.Version); err != nil {
		t.Fatal(err)
	}
}

func testWorker(store *sqlite.Store, notifier notification.Notifier) *outbox.Worker {
	return outbox.NewWorker(store, notifier, outbox.WorkerOptions{
		BaseURL:        "https://example.com/stick",
		Location:       time.UTC,
		PollInterval:   time.Millisecond,
		AttemptTimeout: 100 * time.Millisecond,
		InitialBackoff: 2 * time.Millisecond,
		MaxBackoff:     5 * time.Millisecond,
		ClaimTTL:       10 * time.Millisecond,
	})
}

func waitForOutbox(t *testing.T, conn *sql.DB, status string, attempts int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var gotStatus string
		var gotAttempts int
		err := conn.QueryRow(`SELECT status, attempts FROM notification_outbox LIMIT 1`).Scan(&gotStatus, &gotAttempts)
		if err == nil && gotStatus == status && gotAttempts == attempts {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("outbox status/attempts = %q/%d (err %v), want %q/%d", gotStatus, gotAttempts, err, status, attempts)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func startWorker(worker *outbox.Worker) (context.CancelFunc, <-chan error) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	return cancel, done
}

func stopWorker(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run worker: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("worker did not stop")
	}
}
