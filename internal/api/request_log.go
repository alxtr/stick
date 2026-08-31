package api

import (
	"context"
	"net/http"

	"stick/internal/api/requestlog"
)

// RequestLogger assigns or propagates a safe request ID, exposes it in the
// response, and logs only method/path/status/duration/request ID. Query
// strings and headers are intentionally excluded because they may contain
// credentials or other user data.
func RequestLogger(next http.Handler) http.Handler {
	return requestlog.RequestLogger(next)
}

// WithRequestID returns a context containing the request ID.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return requestlog.WithRequestID(ctx, requestID)
}

// RequestID returns the request ID assigned by the request logger, if any.
func RequestID(ctx context.Context) string {
	return requestlog.RequestID(ctx)
}

// LogError logs a request failure with its correlated request ID.
func LogError(ctx context.Context, message, operation string, err error) {
	requestlog.LogError(ctx, message, operation, err)
}
