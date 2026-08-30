package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"stick/internal/api"
)

func TestRequestIDContext(t *testing.T) {
	ctx := api.WithRequestID(context.Background(), "request-42")
	if got := api.RequestID(ctx); got != "request-42" {
		t.Errorf("request ID = %q, want request-42", got)
	}
}

func TestRequestLoggerAssignsAndPropagatesID(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	wrapped := api.RequestLogger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if api.RequestID(r.Context()) == "" {
			t.Error("request ID missing from context")
		}
		w.WriteHeader(http.StatusTeapot)
	}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/private?secret=do-not-log", nil)
	wrapped.ServeHTTP(recorder, request)
	requestID := recorder.Header().Get("X-Request-ID")
	if requestID == "" {
		t.Fatal("response request ID missing")
	}
	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("log is not JSON: %v", err)
	}
	if entry["request_id"] != requestID || entry["path"] != "/private" || entry["status"] != float64(http.StatusTeapot) {
		t.Errorf("unexpected log entry: %v", entry)
	}
	if strings.Contains(logs.String(), "secret=do-not-log") {
		t.Error("request query string was logged")
	}
}

func TestRequestLoggerPropagatesValidIncomingID(t *testing.T) {
	wrapped := api.RequestLogger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Request-ID", "upstream-request-42")
	recorder := httptest.NewRecorder()
	wrapped.ServeHTTP(recorder, request)
	if got := recorder.Header().Get("X-Request-ID"); got != "upstream-request-42" {
		t.Errorf("request ID = %q, want upstream-request-42", got)
	}
}

func TestLogErrorLogsCorrelatedFailure(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	ctx := api.WithRequestID(t.Context(), "request-42")

	api.LogError(ctx, "request failed", "perform operation", errors.New("failed"))

	for _, want := range []string{`"msg":"request failed"`, `"request_id":"request-42"`, `"operation":"perform operation"`, `"error":"failed"`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log missing %q: %s", want, logs.String())
		}
	}
}
