package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"stick/internal/adapters/persistence/sqlite"
	"stick/internal/application"
)

func TestNewHandlerRegistersRouteGroups(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "stick.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	handler, err := newHandler(application.NewService(store), store, Options{
		PublicURL: "http://example.test/ops/stick",
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
	}{
		{name: "health route", path: "/ops/stick/healthz", wantStatus: http.StatusOK, wantBody: "ok\n"},
		{name: "protected sticks route", path: "/ops/stick/api/v1/sticks", wantStatus: http.StatusUnauthorized, wantBody: `{"error":"unauthorized"}`},
		{name: "api not found", path: "/ops/stick/missing", wantStatus: http.StatusNotFound, wantBody: `{"error":"not found"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
			if recorder.Code != test.wantStatus || recorder.Body.String() != test.wantBody {
				t.Fatalf("response = %d %q, want %d %q", recorder.Code, recorder.Body.String(), test.wantStatus, test.wantBody)
			}
			if recorder.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Error("security response header missing")
			}
			if recorder.Header().Get("X-Request-ID") == "" {
				t.Error("request ID response header missing")
			}
		})
	}
}

func TestNewRunnerValidatesPublicURL(t *testing.T) {
	if _, err := NewRunner(nil, nil, Options{PublicURL: "not a URL"}); err == nil {
		t.Fatal("NewRunner accepted an invalid public URL")
	}
}

func TestRunnerReportsListenFailure(t *testing.T) {
	runner, err := NewRunner(nil, nil, Options{
		PublicURL:  "http://example.test",
		ListenAddr: "invalid-listen-address",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("Run error = %v, want listen error", err)
	}
}
