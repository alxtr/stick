// Package health provides unauthenticated operational HTTP probes.
package health

import (
	"context"
	"net/http"
	"time"
)

const readinessTimeout = 2 * time.Second

// ReadinessChecker is the dependency used by the readiness probe.
type ReadinessChecker interface {
	PingContext(context.Context) error
}

// Handler provides unauthenticated process and dependency probes. Responses
// contain no configuration or database details.
type Handler struct {
	readiness ReadinessChecker
}

// New returns a health handler backed by readiness.
func New(readiness ReadinessChecker) *Handler {
	return &Handler{readiness: readiness}
}

// Liveness serves the process liveness probe.
func (h *Handler) Liveness(w http.ResponseWriter, _ *http.Request) {
	h.writeStatus(w, http.StatusOK, "ok\n")
}

// Readiness serves the database readiness probe.
func (h *Handler) Readiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
	defer cancel()
	// Readiness deliberately reflects only the required database dependency.
	// OIDC discovery is lazy, so an identity-provider outage does not remove
	// otherwise healthy instances from service.
	if h.readiness == nil || h.readiness.PingContext(ctx) != nil {
		h.writeStatus(w, http.StatusServiceUnavailable, "not ready\n")
		return
	}
	h.writeStatus(w, http.StatusOK, "ready\n")
}

func (h *Handler) writeStatus(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
