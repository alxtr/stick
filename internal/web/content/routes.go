package content

import (
	"net/http"

	"stick/internal/web/httpx"
)

// RegisterAssets registers the UI's embedded stylesheet under /assets.
func RegisterAssets(router *httpx.Router) {
	router.Handle(http.MethodGet, "/assets/styles.css", stylesheet())
}

func stylesheet() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, no-cache")
		http.ServeFileFS(w, r, Assets(), "assets/styles.css")
	})
}
