package api

import (
	"net/http"
)

// RegisterHealth registers the liveness and readiness routes.
func RegisterHealth(router *Router, handler *healthHandler) {
	router.HandleFunc(http.MethodGet, "/healthz", handler.Liveness)
	router.HandleFunc(http.MethodGet, "/readyz", handler.Readiness)
}
