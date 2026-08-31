// Package api provides the HTTP server and routing infrastructure for Stick.
package api

import (
	"encoding/json"
	"net/http"

	"stick/internal/api/sticks"
	"stick/internal/application"
)

// Handler is kept as an alias for compatibility with callers that used the
// original root API package. New code should use api/sticks directly.
type Handler = sticks.Handler

// TokenValidator is the validator used by the Stick route group.
type TokenValidator = sticks.TokenValidator

// New returns a Stick API handler. New code should use sticks.New directly.
func New(
	service *application.Service,
	validator TokenValidator,
	adminEmails []string,
	publicURL string,
	notificationsEnabled bool,
) *Handler {
	return sticks.New(service, validator, adminEmails, publicURL, notificationsEnabled)
}

// NotFound writes the JSON response used for unknown API routes.
func NotFound(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	data, _ := json.Marshal(struct {
		Error string `json:"error"`
	}{Error: "not found"})
	_, _ = w.Write(data)
}
