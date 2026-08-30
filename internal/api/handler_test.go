package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"stick/internal/adapters/persistence/sqlite"
	"stick/internal/api"
	"stick/internal/application"
	domain "stick/internal/core"
	"stick/internal/publicurl"
)

type tokenValidator map[string]domain.Identity

func (v tokenValidator) Validate(_ context.Context, token string) (domain.Identity, error) {
	identity, ok := v[token]
	if !ok {
		return domain.Identity{}, errors.New("invalid token")
	}
	return identity, nil
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "stick.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	publicURL, err := publicurl.Parse("http://localhost:8080")
	if err != nil {
		t.Fatal(err)
	}
	admin := domain.Identity{Sub: "admin", Name: "Admin", Email: "admin@example.com", EmailVerified: true}
	handler := api.New(application.NewService(store), tokenValidator{
		"admin-token": admin,
		"user-token":  {Sub: "user", Name: "User", Email: "user@example.com", EmailVerified: true},
	}, []string{"admin@example.com"}, publicURL, true)
	router := api.NewRouter(publicURL)
	api.Register(router, handler)
	router.SetNotFound(http.HandlerFunc(api.NotFound))
	return router
}

func request(handler http.Handler, method, path, token, body string, headers map[string]string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, stringsReader(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	handler.ServeHTTP(recorder, request)
	return recorder
}

func stringsReader(value string) *strings.Reader { return strings.NewReader(value) }

func TestAuthorizationIsBearerOnly(t *testing.T) {
	handler := newTestHandler(t)
	response := request(handler, http.MethodGet, "/api/v1/sticks", "", "", nil)
	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("status=%d authenticate=%q body=%s", response.Code, response.Header().Get("WWW-Authenticate"), response.Body.String())
	}
	response = request(handler, http.MethodGet, "/api/v1/sticks", "unknown", "", nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token status=%d", response.Code)
	}
}

func TestStickETagAndOptimisticConcurrency(t *testing.T) {
	handler := newTestHandler(t)
	created := request(handler, http.MethodPost, "/api/v1/sticks", "admin-token", `{"name":"Deploy Key"}`, map[string]string{"Content-Type": "application/json"})
	if created.Code != http.StatusCreated || created.Header().Get("ETag") != `"1"` {
		t.Fatalf("create status=%d etag=%q body=%s", created.Code, created.Header().Get("ETag"), created.Body.String())
	}
	var stick struct {
		ID      string `json:"id"`
		Version int64  `json:"version"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &stick); err != nil {
		t.Fatal(err)
	}
	if stick.ID == "" || stick.Version != 1 {
		t.Fatalf("created stick = %+v", stick)
	}

	path := "/api/v1/sticks/" + stick.ID
	current := request(handler, http.MethodGet, path, "user-token", "", nil)
	if current.Code != http.StatusOK || current.Header().Get("ETag") != `"1"` {
		t.Fatalf("get status=%d etag=%q", current.Code, current.Header().Get("ETag"))
	}
	notModified := request(handler, http.MethodGet, path, "user-token", "", map[string]string{"If-None-Match": `"1"`})
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Fatalf("not modified status=%d body=%q", notModified.Code, notModified.Body.String())
	}

	missing := request(handler, http.MethodPatch, path, "admin-token", `{"name":"Renamed"}`, map[string]string{"Content-Type": "application/json"})
	if missing.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match status=%d", missing.Code)
	}
	stale := request(handler, http.MethodPatch, path, "admin-token", `{"name":"Stale"}`, map[string]string{
		"Content-Type": "application/json", "If-Match": `"9"`,
	})
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale status=%d body=%s", stale.Code, stale.Body.String())
	}
	updated := request(handler, http.MethodPatch, path, "admin-token", `{"name":"Renamed"}`, map[string]string{
		"Content-Type": "application/json", "If-Match": `"1"`,
	})
	if updated.Code != http.StatusOK || updated.Header().Get("ETag") != `"2"` {
		t.Fatalf("update status=%d etag=%q body=%s", updated.Code, updated.Header().Get("ETag"), updated.Body.String())
	}
}

func TestAPIMapsAuthorizationAndValidationErrors(t *testing.T) {
	handler := newTestHandler(t)
	for _, test := range []struct {
		name   string
		method string
		path   string
		token  string
		body   string
		status int
	}{
		{name: "non-admin create", method: http.MethodPost, path: "/api/v1/sticks", token: "user-token", body: `{"name":"x"}`, status: http.StatusForbidden},
		{name: "invalid JSON", method: http.MethodPost, path: "/api/v1/sticks", token: "admin-token", body: `{`, status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := request(handler, test.method, test.path, test.token, test.body, map[string]string{"Content-Type": "application/json"})
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body.String(), test.status)
			}
		})
	}

	unknown := request(handler, http.MethodGet, "/api/v1/missing", "user-token", "", nil)
	if unknown.Code != http.StatusNotFound || unknown.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unknown route status=%d content-type=%q", unknown.Code, unknown.Header().Get("Content-Type"))
	}
}

var _ api.TokenValidator = tokenValidator{}
