package api

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

// healthHandler provides unauthenticated process and dependency probes. Responses
// contain no configuration or database details.
type healthHandler struct {
	readiness ReadinessChecker
}

// NewHealth returns a health handler backed by readiness.
func NewHealth(readiness ReadinessChecker) *healthHandler {
	return &healthHandler{readiness: readiness}
}

// Register adds the liveness and readiness routes to router.
func (h *healthHandler) Register(router *Router) {
	router.HandleFunc(http.MethodGet, "/healthz", h.Liveness)
	router.HandleFunc(http.MethodGet, "/readyz", h.Readiness)
}

// Liveness serves the process liveness probe.
func (h *healthHandler) Liveness(w http.ResponseWriter, _ *http.Request) {
	h.writeStatus(w, http.StatusOK, "ok\n")
}

// Readiness serves the database readiness probe.
func (h *healthHandler) Readiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
	defer cancel()
	// Readiness deliberately reflects only the required database dependency.
	// Identity-provider discovery is lazy, so an identity-provider outage does
	// not remove otherwise healthy instances from service.
	if h.readiness == nil || h.readiness.PingContext(ctx) != nil {
		h.writeStatus(w, http.StatusServiceUnavailable, "not ready\n")
		return
	}
	h.writeStatus(w, http.StatusOK, "ready\n")
}

func (h *healthHandler) writeStatus(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
