package dashboard

import (
	"net/http"

	"stick/internal/web/httpx"
)

// Register registers the dashboard route under /.
func Register(router *httpx.Router, handler *Handler) {
	router.HandleFunc(http.MethodGet, "/{$}", handler.Dashboard)
}
