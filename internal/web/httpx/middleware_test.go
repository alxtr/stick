package httpx_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"stick/internal/web/httpx"
	"stick/internal/web/security"
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

func TestMiddlewareAppliesLoggingAndCSRFInOrder(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	innerCalled := false
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { innerCalled = true })
	root := httpx.Chain(
		httpx.RequestLogger,
		security.CSRFProtection([]byte("secret-32-bytes-minimum-length!!"), nil),
	)(inner)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/sticks/aa001/release", nil)
	request.Header.Set("X-Request-ID", "known-request-id")
	root.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden || innerCalled {
		t.Fatalf("status=%d innerCalled=%v, want 403/false", recorder.Code, innerCalled)
	}
	if recorder.Header().Get("X-Request-ID") != "known-request-id" {
		t.Fatal("request ID middleware did not run")
	}
	if got := logs.String(); !strings.Contains(got, "status=403") || !strings.Contains(got, "request_id=known-request-id") {
		t.Errorf("log = %q", got)
	}
}
