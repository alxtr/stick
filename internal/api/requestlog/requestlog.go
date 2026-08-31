// Package requestlog provides request logging and request-ID context helpers.
package requestlog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type contextKey string

const requestIDKey contextKey = "request-id"

// RequestLogger assigns or propagates a safe request ID, exposes it in the
// response, and logs only method/path/status/duration/request ID. Query
// strings and headers are intentionally excluded because they may contain
// credentials or other user data.
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := validRequestID(r.Header.Get("X-Request-ID"))
		if requestID == "" {
			requestID = newRequestID()
		}
		w.Header().Set("X-Request-ID", requestID)
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		r = r.WithContext(WithRequestID(r.Context(), requestID))
		next.ServeHTTP(sw, r)
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		duration := time.Since(start)
		slog.InfoContext(r.Context(), "http request", "method", r.Method, "path", r.URL.Path, "status", status,
			"duration_ms", duration.Seconds()*1000, "request_id", requestID)
	})
}

// WithRequestID returns a context containing the request ID.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}

// RequestID returns the request ID assigned by the request logger, if any.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// LogError logs a request failure with its correlated request ID.
func LogError(ctx context.Context, message, operation string, err error) {
	slog.ErrorContext(ctx, message, "request_id", RequestID(ctx), "operation", operation, "error", err)
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func validRequestID(value string) string {
	if len(value) == 0 || len(value) > 128 {
		return ""
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("._-", r) {
			return ""
		}
	}
	return value
}

func newRequestID() string {
	var b [16]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	// crypto/rand failures should be exceptionally rare; a timestamp keeps
	// request logging useful without exposing request data.
	return fmt.Sprintf("fallback-%x", time.Now().UnixNano())
}
