package auth

import (
	"net/http"

	"stick/internal/web/httpx"
)

// Register registers the /auth routes.
func Register(router *httpx.Router, handler *Handler) {
	router.HandleFunc(http.MethodGet, "/auth/login", handler.Login)
	router.HandleFunc(http.MethodGet, "/auth/callback", handler.Callback)
	router.HandleFunc(http.MethodPost, "/auth/logout", handler.Logout)
}
