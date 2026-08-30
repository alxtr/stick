package health

import (
	"net/http"

	"stick/internal/web/httpx"
)

// Register registers the liveness and readiness routes.
func Register(router *httpx.Router, handler *Handler) {
	router.HandleFunc(http.MethodGet, "/healthz", handler.Liveness)
	router.HandleFunc(http.MethodGet, "/readyz", handler.Readiness)
}
