package httpx_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"stick/internal/web/httpx"
)

func TestChainAppliesMiddlewareInOrder(t *testing.T) {
	var calls []string
	record := func(name string) httpx.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				calls = append(calls, name+" before")
				next.ServeHTTP(response, request)
				calls = append(calls, name+" after")
			})
		}
	}
	final := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls = append(calls, "handler")
	})

	stack := httpx.Chain(record("first"), record("second"))
	stack(final).ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/", nil),
	)

	want := []string{"first before", "second before", "handler", "second after", "first after"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %q, want %q", calls, want)
	}
}

func TestRequestLoggerAddsRequestIDAndLogs(t *testing.T) {
	var logs strings.Builder
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	root := httpx.RequestLogger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/sticks", nil)
	request.Header.Set("X-Request-ID", "known-request-id")
	root.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d, want 204", recorder.Code)
	}
	if recorder.Header().Get("X-Request-ID") != "known-request-id" {
		t.Fatal("request ID middleware did not run")
	}
	if got := logs.String(); !strings.Contains(got, "status=204") || !strings.Contains(got, "request_id=known-request-id") {
		t.Errorf("log = %q", got)
	}
}
