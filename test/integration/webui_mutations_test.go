package integration_test

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"stick/internal/adapters/persistence/sqlite"
	"stick/internal/application"
	"stick/internal/auth"
	domain "stick/internal/core"
	"stick/internal/web/sticks"
)

type uiMutationScenario struct {
	name     string
	setup    func(*testing.T, *sqlite.Store)
	identity domain.Identity
	path     string
	form     url.Values
	serve    func(*sticks.Handler, http.ResponseWriter, *http.Request)
}

func TestUIMutationsRejectMissingAndStaleFormVersions(t *testing.T) {
	scenarios := []uiMutationScenario{
		{
			name:     "rename",
			setup:    seedAvailableStick,
			identity: domain.Identity{Sub: "admin", IsAdmin: true},
			path:     "/sticks/aa001/rename",
			form:     url.Values{"name": {"renamed"}},
			serve: func(handler *sticks.Handler, writer http.ResponseWriter, request *http.Request) {
				handler.Rename(writer, request)
			},
		},
		{
			name:     "archive",
			setup:    seedAvailableStick,
			identity: domain.Identity{Sub: "admin", IsAdmin: true},
			path:     "/sticks/aa001/archive",
			serve: func(handler *sticks.Handler, writer http.ResponseWriter, request *http.Request) {
				handler.Archive(writer, request)
			},
		},
		{
			name: "unarchive",
			setup: func(t *testing.T, store *sqlite.Store) {
				seedAvailableStick(t, store)
				archiveWebUIStick(t, store, "aa001")
			},
			identity: domain.Identity{Sub: "admin", IsAdmin: true},
			path:     "/sticks/aa001/unarchive",
			serve: func(handler *sticks.Handler, writer http.ResponseWriter, request *http.Request) {
				handler.Unarchive(writer, request)
			},
		},
		{
			name:     "claim",
			setup:    seedAvailableStick,
			identity: domain.Identity{Sub: "u1"},
			path:     "/sticks/aa001/claim",
			form:     url.Values{"reason": {"working"}},
			serve: func(handler *sticks.Handler, writer http.ResponseWriter, request *http.Request) {
				handler.Claim(writer, request)
			},
		},
		{
			name: "release",
			setup: func(t *testing.T, store *sqlite.Store) {
				seedAvailableStick(t, store)
				claimWebUIStick(t, store, "aa001", domain.Identity{Sub: "u1"}, "working")
			},
			identity: domain.Identity{Sub: "u1"},
			path:     "/sticks/aa001/release",
			serve: func(handler *sticks.Handler, writer http.ResponseWriter, request *http.Request) {
				handler.Release(writer, request)
			},
		},
		{
			name:     "subscribe",
			setup:    seedAvailableStick,
			identity: domain.Identity{Sub: "u1"},
			path:     "/sticks/aa001/notify",
			serve: func(handler *sticks.Handler, writer http.ResponseWriter, request *http.Request) {
				handler.Subscribe(writer, request)
			},
		},
		{
			name: "unsubscribe",
			setup: func(t *testing.T, store *sqlite.Store) {
				seedAvailableStick(t, store)
				subscribeWebUIStick(t, store, "aa001", "u1", "Watcher", "watcher@example.com")
			},
			identity: domain.Identity{Sub: "u1"},
			path:     "/sticks/aa001/notify/cancel",
			serve: func(handler *sticks.Handler, writer http.ResponseWriter, request *http.Request) {
				handler.Unsubscribe(writer, request)
			},
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			for _, precondition := range []struct {
				name       string
				version    string
				wantStatus int
			}{
				{name: "missing", wantStatus: http.StatusBadRequest},
				{name: "stale", version: "999", wantStatus: http.StatusConflict},
			} {
				t.Run(precondition.name, func(t *testing.T) {
					store := newWebUITestDB(t)
					scenario.setup(t, store)
					before := currentWebUIStick(t, store, "aa001")
					handler, _, err := newSticksHandler(application.NewService(store), time.UTC, webuiPublicURL(t, "/base"), true)
					if err != nil {
						t.Fatal(err)
					}
					form := cloneValues(scenario.form)
					if precondition.version != "" {
						form.Set("version", precondition.version)
					}
					request := newUIFormRequest(scenario.path, form, scenario.identity)
					recorder := httptest.NewRecorder()
					scenario.serve(handler, recorder, request)
					if recorder.Code != precondition.wantStatus {
						t.Fatalf("status = %d, want %d: %s", recorder.Code, precondition.wantStatus, recorder.Body.String())
					}
					if got := recorder.Header().Get("Location"); got != "" {
						t.Fatalf("Location = %q, want no redirect", got)
					}
					body := recorder.Body.String()
					if precondition.wantStatus == http.StatusConflict {
						for _, want := range []string{
							"This stick changed since the page was loaded. Review its current state and try again.",
							before.Name,
							strconv.FormatInt(before.Version, 10),
						} {
							if !strings.Contains(body, want) {
								t.Errorf("conflict page missing %q", want)
							}
						}
					}
					if strings.Contains(body, "?error=") {
						t.Error("conflict page contains query-string error transport")
					}
					after := currentWebUIStick(t, store, "aa001")
					if after.Version != before.Version || after.Name != before.Name {
						t.Fatalf("conflicting request changed stick: before=%+v after=%+v", before, after)
					}
				})
			}
		})
	}
}

func TestUIMutationRedirectsAndErrors(t *testing.T) {
	tests := []struct {
		name         string
		setup        func(*testing.T, *sqlite.Store)
		identity     domain.Identity
		path         string
		form         url.Values
		serve        func(*sticks.Handler, http.ResponseWriter, *http.Request)
		wantLocation string
		wantStatus   int
		wantMessage  string
	}{
		{
			name:         "rename redirects to the detail page",
			setup:        seedAvailableStick,
			identity:     domain.Identity{Sub: "admin", IsAdmin: true},
			path:         "/sticks/aa001/rename",
			form:         url.Values{"name": {"renamed"}},
			wantLocation: "/base/sticks/aa001",
			wantStatus:   http.StatusSeeOther,
			serve: func(handler *sticks.Handler, writer http.ResponseWriter, request *http.Request) {
				handler.Rename(writer, request)
			},
		},
		{
			name:         "archive redirects to the dashboard",
			setup:        seedAvailableStick,
			identity:     domain.Identity{Sub: "admin", IsAdmin: true},
			path:         "/sticks/aa001/archive",
			wantLocation: "/base/",
			wantStatus:   http.StatusSeeOther,
			serve: func(handler *sticks.Handler, writer http.ResponseWriter, request *http.Request) {
				handler.Archive(writer, request)
			},
		},
		{
			name: "archive already archived stick explains the conflict",
			setup: func(t *testing.T, store *sqlite.Store) {
				seedAvailableStick(t, store)
				archiveWebUIStick(t, store, "aa001")
			},
			identity:    domain.Identity{Sub: "admin", IsAdmin: true},
			path:        "/sticks/aa001/archive",
			wantStatus:  http.StatusConflict,
			wantMessage: "This stick is already archived.",
			serve: func(handler *sticks.Handler, writer http.ResponseWriter, request *http.Request) {
				handler.Archive(writer, request)
			},
		},
		{
			name: "unarchive redirects to the dashboard",
			setup: func(t *testing.T, store *sqlite.Store) {
				seedAvailableStick(t, store)
				archiveWebUIStick(t, store, "aa001")
			},
			identity:     domain.Identity{Sub: "admin", IsAdmin: true},
			path:         "/sticks/aa001/unarchive",
			wantLocation: "/base/",
			wantStatus:   http.StatusSeeOther,
			serve: func(handler *sticks.Handler, writer http.ResponseWriter, request *http.Request) {
				handler.Unarchive(writer, request)
			},
		},
		{
			name:        "unarchive active stick explains the conflict",
			setup:       seedAvailableStick,
			identity:    domain.Identity{Sub: "admin", IsAdmin: true},
			path:        "/sticks/aa001/unarchive",
			wantStatus:  http.StatusConflict,
			wantMessage: "This stick is not archived.",
			serve: func(handler *sticks.Handler, writer http.ResponseWriter, request *http.Request) {
				handler.Unarchive(writer, request)
			},
		},
		{
			name:         "release redirects back to the stick",
			setup:        seedHeldBy("u1"),
			identity:     domain.Identity{Sub: "u1"},
			path:         "/sticks/aa001/release",
			wantLocation: "/base/sticks/aa001",
			wantStatus:   http.StatusSeeOther,
			serve: func(handler *sticks.Handler, writer http.ResponseWriter, request *http.Request) {
				handler.Release(writer, request)
			},
		},
		{
			name:        "archive held stick explains the conflict",
			setup:       seedHeldBy("holder"),
			identity:    domain.Identity{Sub: "admin", IsAdmin: true},
			path:        "/sticks/aa001/archive",
			wantStatus:  http.StatusConflict,
			wantMessage: "This stick is currently held.",
			serve: func(handler *sticks.Handler, writer http.ResponseWriter, request *http.Request) {
				handler.Archive(writer, request)
			},
		},
		{
			name:        "claim held stick explains the conflict",
			setup:       seedHeldBy("holder"),
			identity:    domain.Identity{Sub: "u1"},
			path:        "/sticks/aa001/claim",
			form:        url.Values{"reason": {"again"}},
			wantStatus:  http.StatusConflict,
			wantMessage: "This stick is already held.",
			serve: func(handler *sticks.Handler, writer http.ResponseWriter, request *http.Request) {
				handler.Claim(writer, request)
			},
		},
		{
			name: "claim archived stick explains the conflict",
			setup: func(t *testing.T, store *sqlite.Store) {
				seedAvailableStick(t, store)
				archiveWebUIStick(t, store, "aa001")
			},
			identity:    domain.Identity{Sub: "admin", IsAdmin: true},
			path:        "/sticks/aa001/claim",
			form:        url.Values{"reason": {"working"}},
			wantStatus:  http.StatusConflict,
			wantMessage: "This stick is archived and cannot be claimed.",
			serve: func(handler *sticks.Handler, writer http.ResponseWriter, request *http.Request) {
				handler.Claim(writer, request)
			},
		},
		{
			name:        "release by non-holder explains the ownership error",
			setup:       seedHeldBy("holder"),
			identity:    domain.Identity{Sub: "u1"},
			path:        "/sticks/aa001/release",
			wantStatus:  http.StatusForbidden,
			wantMessage: "You can only put down a stick that you currently hold.",
			serve: func(handler *sticks.Handler, writer http.ResponseWriter, request *http.Request) {
				handler.Release(writer, request)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newWebUITestDB(t)
			test.setup(t, store)
			handler, _, err := newSticksHandler(application.NewService(store), time.UTC, webuiPublicURL(t, "/base"), true)
			if err != nil {
				t.Fatal(err)
			}
			stick, err := store.GetStick(context.Background(), "aa001")
			if err != nil {
				t.Fatal(err)
			}
			form := cloneValues(test.form)
			form.Set("version", strconv.FormatInt(stick.Version, 10))
			request := newUIFormRequest(test.path, form, test.identity)
			recorder := httptest.NewRecorder()
			test.serve(handler, recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if got := recorder.Header().Get("Location"); got != test.wantLocation {
				t.Fatalf("Location = %q, want %q", got, test.wantLocation)
			}
			if test.wantMessage != "" {
				if !strings.Contains(recorder.Body.String(), test.wantMessage) {
					t.Errorf("response missing %q", test.wantMessage)
				}
				if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
					t.Errorf("Content-Type = %q, want HTML", got)
				}
				if strings.Contains(recorder.Body.String(), "?error=") {
					t.Error("response contains query-string error transport")
				}
			}
		})
	}
}

func TestUIRenameValidationRendersCurrentFormWithSubmittedValue(t *testing.T) {
	store := newWebUITestDB(t)
	seedAvailableStick(t, store)
	handler, _, err := newSticksHandler(application.NewService(store), time.UTC, webuiPublicURL(t, "/base"), true)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"name": {"bad/name"}, "version": {webUIStickVersion(t, store, "aa001")}}
	request := newUIFormRequest("/sticks/aa001/rename", form, domain.Identity{Sub: "admin", IsAdmin: true})
	recorder := httptest.NewRecorder()

	handler.Rename(recorder, request)

	if recorder.Code != http.StatusUnprocessableEntity || recorder.Header().Get("Location") != "" {
		t.Fatalf("response = %d Location %q", recorder.Code, recorder.Header().Get("Location"))
	}
	for _, want := range []string{
		`value="bad/name"`,
		`aria-invalid="true"`,
		`aria-describedby="rename-name-error"`,
		`id="rename-name-error" role="alert"`,
		invalidNameText,
	} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Errorf("validation response missing %q", want)
		}
	}
	if strings.Contains(recorder.Body.String(), "?error=") {
		t.Error("rename validation used query-string error transport")
	}
	if stick := currentWebUIStick(t, store, "aa001"); stick.Name != "prod" || stick.Version != 1 {
		t.Fatalf("invalid rename changed stick: %+v", stick)
	}
}

const invalidNameText = "Name must be non-empty, at most 100 characters, and contain only letters, digits, hyphens, and spaces."

func TestUITransactionVersionConflictRendersLatestStateAndFreshVersion(t *testing.T) {
	store := newWebUITestDB(t)
	seedAvailableStick(t, store)
	conflictingStore := &versionConflictStore{
		Store:      store,
		inject:     true,
		latestName: "newer transaction state",
	}
	handler, _, err := newSticksHandler(application.NewService(conflictingStore), time.UTC, webuiPublicURL(t, "/base"), true)
	if err != nil {
		t.Fatal(err)
	}
	staleVersion := webUIStickVersion(t, store, "aa001")
	request := newUIFormRequest("/sticks/aa001/rename", url.Values{
		"name": {"attempted overwrite"}, "version": {staleVersion},
	}, domain.Identity{Sub: "admin", IsAdmin: true})
	recorder := httptest.NewRecorder()

	handler.Rename(recorder, request)

	if recorder.Code != http.StatusConflict || recorder.Header().Get("Location") != "" {
		t.Fatalf("response = %d Location %q", recorder.Code, recorder.Header().Get("Location"))
	}
	latest := currentWebUIStick(t, store, "aa001")
	if latest.Name != conflictingStore.latestName || latest.Version != 2 {
		t.Fatalf("latest stick = %+v", latest)
	}
	body := recorder.Body.String()
	for _, want := range []string{
		conflictingStore.latestName,
		"Review its current state and try again.",
		html.EscapeString(strconv.FormatInt(latest.Version, 10)),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("conflict response missing %q", want)
		}
	}
	if strings.Contains(body, "attempted overwrite") || strings.Contains(body, "?error=") {
		t.Error("conflict response rendered stale input or query-string error transport")
	}
}

type versionConflictStore struct {
	application.Store
	inject     bool
	latestName string
}

func (s *versionConflictStore) WithinTransaction(ctx context.Context, work func(application.Transaction) error) error {
	if !s.inject {
		return s.Store.WithinTransaction(ctx, work)
	}
	s.inject = false
	current, err := s.GetStick(ctx, "aa001")
	if err != nil {
		return err
	}
	next, err := domain.Rename(current, s.latestName)
	if err != nil {
		return err
	}
	if err := s.Store.WithinTransaction(ctx, func(tx application.Transaction) error {
		return tx.SaveStick(ctx, next, current.Version)
	}); err != nil {
		return err
	}
	return application.ErrVersionConflict
}

func seedAvailableStick(t *testing.T, store *sqlite.Store) {
	createWebUIStick(t, store, "aa001", "prod")
}

func seedHeldBy(subject string) func(*testing.T, *sqlite.Store) {
	return func(t *testing.T, store *sqlite.Store) {
		seedAvailableStick(t, store)
		claimWebUIStick(t, store, "aa001", domain.Identity{Sub: subject}, "working")
	}
}

func newUIFormRequest(path string, form url.Values, identity domain.Identity) *http.Request {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetPathValue("id", "aa001")
	return request.WithContext(auth.WithIdentity(request.Context(), identity))
}

func cloneValues(values url.Values) url.Values {
	clone := make(url.Values, len(values))
	for key, values := range values {
		clone[key] = append([]string(nil), values...)
	}
	return clone
}
