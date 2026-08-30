package sticks

import (
	"net/http"

	"stick/internal/web/httpx"
)

// Register registers every route below /sticks.
func Register(router *httpx.Router, handler *Handler) {
	router.HandleFunc(http.MethodGet, "/sticks/new", handler.NewStick)
	router.HandleFunc(http.MethodPost, "/sticks/new", handler.CreateStick)
	router.HandleFunc(http.MethodGet, "/sticks/{id}", handler.Detail)
	router.HandleFunc(http.MethodPost, "/sticks/{id}/rename", handler.Rename)
	router.HandleFunc(http.MethodPost, "/sticks/{id}/archive", handler.Archive)
	router.HandleFunc(http.MethodPost, "/sticks/{id}/unarchive", handler.Unarchive)
	router.HandleFunc(http.MethodPost, "/sticks/{id}/claim", handler.Claim)
	router.HandleFunc(http.MethodPost, "/sticks/{id}/release", handler.Release)
	if handler.notificationsEnabled {
		router.HandleFunc(http.MethodPost, "/sticks/{id}/notify", handler.Subscribe)
		router.HandleFunc(http.MethodPost, "/sticks/{id}/notify/cancel", handler.Unsubscribe)
	}
}
