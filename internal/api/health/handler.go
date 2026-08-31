// Package health provides the process and dependency health endpoints.
package health

import (
	"context"
	"net/http"
	"time"
)

const (
	// ReadinessTimeout is the maximum time spent checking the readiness
	// dependency.
	ReadinessTimeout = 2 * time.Second
)

// RouteRegistrar is the part of the API router needed to register routes.
// Keeping the interface here avoids coupling route groups to the server's
// router implementation.
type RouteRegistrar interface {
	HandleFunc(method, reference string, handler http.HandlerFunc, middlewares ...func(http.Handler) http.Handler)
}

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

// Register adds the liveness and readiness routes to router.
func (h *Handler) Register(router RouteRegistrar) {
	router.HandleFunc(http.MethodGet, "/healthz", h.Liveness)
	router.HandleFunc(http.MethodGet, "/readyz", h.Readiness)
}

// Liveness serves the process liveness probe.
func (h *Handler) Liveness(w http.ResponseWriter, _ *http.Request) {
	h.writeStatus(w, http.StatusOK, "ok\n")
}

// Readiness serves the database readiness probe.
func (h *Handler) Readiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), ReadinessTimeout)
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

func (h *Handler) writeStatus(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
