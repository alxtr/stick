package application_test

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	app "stick/internal/application"
	domain "stick/internal/core"
)

var (
	testContext = context.Background()
	admin       = domain.Identity{Sub: "admin", Name: "Admin", Email: "admin@example.com", IsAdmin: true}
	user        = domain.Identity{Sub: "user", Name: "User", Email: "user@example.com"}
)

func TestServiceOwnsVersionedLifecycleAndTransactions(t *testing.T) {
	store := newMemoryStore(domain.Stick{ID: "aa001", Name: "Old", Version: 1})
	service := app.NewService(store)

	renamed, err := service.RenameStick(testContext, admin, "aa001", "New", 1)
	if err != nil || renamed.Version != 2 || renamed.Name != "New" {
		t.Fatalf("RenameStick = %+v, %v", renamed, err)
	}
	claimed, err := service.ClaimStick(testContext, user, "aa001", "deploying", 2)
	if err != nil || claimed.Version != 3 || claimed.Holder == nil || len(store.sessions) != 1 {
		t.Fatalf("ClaimStick = %+v, %v; sessions=%+v", claimed, err, store.sessions)
	}
	if store.sessions[0].ClaimedAt != claimed.Holder.ClaimedAt {
		t.Fatal("claim and session did not use the same timestamp")
	}
	store.subscribers = []domain.Identity{{Sub: "watcher", Name: "Watcher", Email: "w@example.com"}}
	released, err := service.ReleaseStick(testContext, user, "aa001", 3)
	if err != nil || released.Version != 4 || !released.Available() || released.Holder != nil {
		t.Fatalf("ReleaseStick = %+v, %v", released, err)
	}
	if len(store.notifications) != 1 || store.notifications[0].before.Holder == nil || store.sessions[0].ReleasedAt == nil {
		t.Fatalf("release transaction snapshots = %+v, sessions=%+v", store.notifications, store.sessions)
	}
	if store.transactionCalls != 3 {
		t.Fatalf("transaction calls = %d, want 3", store.transactionCalls)
	}
}

func TestCreateStickUsesUUIDv7WithoutCollisionPreflight(t *testing.T) {
	store := newMemoryStore(domain.Stick{})
	stick, err := app.NewService(store).CreateStick(testContext, admin, "Production")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := uuid.Parse(stick.ID)
	if err != nil || parsed.String() != stick.ID || parsed[6]>>4 != 7 {
		t.Fatalf("CreateStick ID = %q, want canonical UUIDv7: %v", stick.ID, err)
	}
	if store.getCalls != 0 {
		t.Fatalf("CreateStick performed %d collision preflight reads", store.getCalls)
	}
}

func TestGetHistoryChecksVisibilityOnce(t *testing.T) {
	store := newMemoryStore(domain.Stick{ID: "aa001", Version: 1})
	store.sessions = []domain.Session{{StickID: "aa001"}}
	sessions, total, err := app.NewService(store).GetHistory(testContext, user, "aa001", 10, 0)
	if err != nil || len(sessions) != 1 || total != 1 {
		t.Fatalf("GetHistory = %+v, %d, %v", sessions, total, err)
	}
	if store.getCalls != 1 || store.historyCalls != 1 {
		t.Fatalf("GetHistory calls: visibility=%d history=%d, want one each", store.getCalls, store.historyCalls)
	}

	archivedAt := time.Now().UTC()
	archived := newMemoryStore(domain.Stick{ID: "aa001", Version: 1, ArchivedAt: &archivedAt})
	if _, _, err := app.NewService(archived).GetHistory(testContext, user, "aa001", 10, 0); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("archived GetHistory error = %v, want ErrNotFound", err)
	}
	if archived.getCalls != 1 || archived.historyCalls != 0 {
		t.Fatalf("hidden GetHistory calls: visibility=%d history=%d", archived.getCalls, archived.historyCalls)
	}
}

func TestServiceRejectsStaleVersionBeforeDomainDecisionOrWrite(t *testing.T) {
	store := newMemoryStore(domain.Stick{ID: "aa001", Name: "Held", Version: 7, Holder: &domain.Holder{Sub: "other"}})
	_, err := app.NewService(store).ArchiveStick(testContext, admin, "aa001", 6)
	if !errors.Is(err, app.ErrVersionConflict) {
		t.Fatalf("ArchiveStick error = %v, want version conflict", err)
	}
	if store.saveCalls != 0 || store.stick.Version != 7 {
		t.Fatalf("stale operation wrote state: calls=%d stick=%+v", store.saveCalls, store.stick)
	}
}

func TestServiceRollsBackClaimAndReleaseSideEffects(t *testing.T) {
	t.Run("claim session failure", func(t *testing.T) {
		initial := domain.Stick{ID: "aa001", Name: "Prod", Version: 1}
		store := newMemoryStore(initial)
		store.failCreateSession = errors.New("session unavailable")
		_, err := app.NewService(store).ClaimStick(testContext, user, initial.ID, "deploying", 1)
		if !errors.Is(err, store.failCreateSession) {
			t.Fatalf("ClaimStick error = %v", err)
		}
		if store.stick != initial || len(store.sessions) != 0 {
			t.Fatalf("failed claim was not rolled back: stick=%+v sessions=%+v", store.stick, store.sessions)
		}
	})

	t.Run("release outbox failure", func(t *testing.T) {
		initial := domain.Stick{
			ID:      "aa001",
			Name:    "Prod",
			Version: 2,
			Holder: &domain.Holder{
				Sub:       user.Sub,
				Name:      user.Name,
				Email:     user.Email,
				ClaimedAt: time.Now().UTC(),
				Reason:    "deploying",
			},
		}
		store := newMemoryStore(initial)
		store.sessions = []domain.Session{{StickID: initial.ID, HolderSub: user.Sub, ClaimedAt: initial.Holder.ClaimedAt}}
		store.subscribers = []domain.Identity{{Sub: "watcher"}}
		store.failNotification = errors.New("outbox unavailable")
		_, err := app.NewService(store).ReleaseStick(testContext, user, initial.ID, 2)
		if !errors.Is(err, store.failNotification) {
			t.Fatalf("ReleaseStick error = %v", err)
		}
		if store.stick.Version != 2 || store.stick.Holder == nil || store.sessions[0].ReleasedAt != nil || len(store.notifications) != 0 {
			t.Fatalf("failed release was not rolled back: stick=%+v sessions=%+v notifications=%+v",
				store.stick, store.sessions, store.notifications)
		}
	})
}

func TestServiceSubscriptionsDoNotChangeStickVersion(t *testing.T) {
	store := newMemoryStore(domain.Stick{ID: "aa001", Version: 9, Holder: &domain.Holder{Sub: "other"}})
	service := app.NewService(store)
	if err := service.Subscribe(testContext, user, "aa001", 9); err != nil {
		t.Fatal(err)
	}
	if err := service.Unsubscribe(testContext, user, "aa001", 9); err != nil {
		t.Fatal(err)
	}
	if store.stick.Version != 9 || store.saveCalls != 0 {
		t.Fatalf("subscription changed stick version: %+v", store.stick)
	}
}

func TestServiceAuthorizationAndValidationPrecedeTransactions(t *testing.T) {
	store := newMemoryStore(domain.Stick{ID: "aa001", Version: 1})
	service := app.NewService(store)
	if _, err := service.RenameStick(testContext, user, "aa001", "Valid", 1); !errors.Is(err, app.ErrForbidden) {
		t.Fatalf("non-admin rename error = %v", err)
	}
	if _, err := service.RenameStick(testContext, admin, "aa001", "bad!", 1); !errors.Is(err, domain.ErrInvalidStickName) {
		t.Fatalf("invalid rename error = %v", err)
	}
	if _, err := service.ClaimStick(testContext, user, "aa001", " ", 1); !errors.Is(err, domain.ErrInvalidClaimReason) {
		t.Fatalf("invalid claim error = %v", err)
	}
	if store.transactionCalls != 0 {
		t.Fatalf("invalid calls opened %d transactions", store.transactionCalls)
	}
}

func TestServiceArchivedVisibility(t *testing.T) {
	archivedAt := time.Now().UTC()
	store := newMemoryStore(domain.Stick{ID: "aa001", Version: 2, ArchivedAt: &archivedAt})
	service := app.NewService(store)
	if _, err := service.GetStick(testContext, user, "aa001"); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("non-admin GetStick error = %v", err)
	}
	if stick, err := service.GetStick(testContext, admin, "aa001"); err != nil || !stick.Archived() {
		t.Fatalf("admin GetStick = %+v, %v", stick, err)
	}
}

type notificationSnapshot struct {
	before     domain.Stick
	releasedAt time.Time
	subscriber domain.Identity
}

type memoryStore struct {
	stick             domain.Stick
	sessions          []domain.Session
	subscribers       []domain.Identity
	notifications     []notificationSnapshot
	subscriptions     map[string]domain.Identity
	transactionCalls  int
	saveCalls         int
	failCreateSession error
	failNotification  error
	getCalls          int
	historyCalls      int
}

func newMemoryStore(stick domain.Stick) *memoryStore {
	return &memoryStore{stick: stick, subscriptions: make(map[string]domain.Identity)}
}

func (s *memoryStore) GetStick(_ context.Context, id string) (domain.Stick, error) {
	s.getCalls++
	if s.stick.ID != id {
		return domain.Stick{}, app.ErrNotFound
	}
	return cloneStick(s.stick), nil
}

func (s *memoryStore) ListSticks(context.Context) ([]domain.Stick, error) {
	return []domain.Stick{cloneStick(s.stick)}, nil
}
func (s *memoryStore) ListArchivedSticks(context.Context) ([]domain.Stick, error) {
	return []domain.Stick{cloneStick(s.stick)}, nil
}
func (s *memoryStore) GetHistory(context.Context, string, int, int) ([]domain.Session, int, error) {
	s.historyCalls++
	return append([]domain.Session(nil), s.sessions...), len(s.sessions), nil
}
func (s *memoryStore) SubscribedStickIDs(context.Context, string) ([]string, error) { return nil, nil }

func (s *memoryStore) WithinTransaction(ctx context.Context, fn func(app.Transaction) error) error {
	s.transactionCalls++
	tx := &memoryTransaction{
		stick:         cloneStick(s.stick),
		sessions:      append([]domain.Session(nil), s.sessions...),
		subscribers:   append([]domain.Identity(nil), s.subscribers...),
		notifications: append([]notificationSnapshot(nil), s.notifications...),
		subscriptions: cloneSubscriptions(s.subscriptions),
		owner:         s,
	}
	if err := fn(tx); err != nil {
		return err
	}
	s.stick, s.sessions, s.notifications, s.subscriptions = tx.stick, tx.sessions, tx.notifications, tx.subscriptions
	return nil
}

type memoryTransaction struct {
	stick         domain.Stick
	sessions      []domain.Session
	subscribers   []domain.Identity
	notifications []notificationSnapshot
	subscriptions map[string]domain.Identity
	owner         *memoryStore
}

func (t *memoryTransaction) GetStick(_ context.Context, id string) (domain.Stick, error) {
	if t.stick.ID != id {
		return domain.Stick{}, app.ErrNotFound
	}
	return cloneStick(t.stick), nil
}
func (t *memoryTransaction) CreateStick(_ context.Context, stick domain.Stick) error {
	t.stick = cloneStick(stick)
	return nil
}
func (t *memoryTransaction) SaveStick(_ context.Context, stick domain.Stick, expected int64) error {
	t.owner.saveCalls++
	if t.stick.Version != expected {
		return app.ErrVersionConflict
	}
	t.stick = cloneStick(stick)
	return nil
}
func (t *memoryTransaction) CreateSession(_ context.Context, session domain.Session) error {
	if t.owner.failCreateSession != nil {
		return t.owner.failCreateSession
	}
	t.sessions = append(t.sessions, session)
	return nil
}
func (t *memoryTransaction) CloseSession(_ context.Context, stickID, sub string, at time.Time) error {
	for i := range t.sessions {
		if t.sessions[i].StickID == stickID && t.sessions[i].HolderSub == sub && t.sessions[i].ReleasedAt == nil {
			t.sessions[i].ReleasedAt = &at
			return nil
		}
	}
	return errors.New("active session not found")
}
func (t *memoryTransaction) EnqueueReleaseNotifications(_ context.Context, before domain.Stick, at time.Time) error {
	if t.owner.failNotification != nil {
		return t.owner.failNotification
	}
	for _, subscriber := range t.subscribers {
		t.notifications = append(t.notifications, notificationSnapshot{
			before:     cloneStick(before),
			releasedAt: at,
			subscriber: subscriber,
		})
	}
	return nil
}
func (t *memoryTransaction) Subscribe(_ context.Context, id string, identity domain.Identity, _ string) error {
	t.subscriptions[id+"/"+identity.Sub] = identity
	return nil
}
func (t *memoryTransaction) Unsubscribe(_ context.Context, id, sub string) error {
	delete(t.subscriptions, id+"/"+sub)
	return nil
}

func cloneStick(stick domain.Stick) domain.Stick {
	if stick.Holder != nil {
		holder := *stick.Holder
		stick.Holder = &holder
	}
	if stick.ArchivedAt != nil {
		at := *stick.ArchivedAt
		stick.ArchivedAt = &at
	}
	return stick
}

func cloneSubscriptions(source map[string]domain.Identity) map[string]domain.Identity {
	result := make(map[string]domain.Identity, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
