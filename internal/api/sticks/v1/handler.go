// Package v1 provides the authenticated Stick REST API route group.
package v1

import (
	"context"
	"net/http"
	"strings"

	"stick/internal/application"
	"stick/internal/auth"
	domain "stick/internal/core"
	"stick/internal/httpx"
)

const (
	defaultHistoryLimit = 20
	maxHistoryLimit     = 100
	maxHistoryOffset    = 100_000
)

// Handler serves the authenticated Stick REST API.
type Handler struct {
	service              *application.Service
	validator            TokenValidator
	admins               map[string]struct{}
	publicURL            string
	notificationsEnabled bool
}

// TokenValidator authenticates one external bearer token.
type TokenValidator interface {
	Validate(context.Context, string) (domain.Identity, error)
}

// New returns a Stick API handler.
func New(
	service *application.Service,
	validator TokenValidator,
	adminEmails []string,
	publicURL string,
	notificationsEnabled bool,
) *Handler {
	return &Handler{
		service:              service,
		validator:            validator,
		admins:               auth.AdminSet(adminEmails),
		publicURL:            publicURL,
		notificationsEnabled: notificationsEnabled,
	}
}

// Register adds all Stick API routes to router. Authentication is applied to
// every route in this group.
func (h *Handler) Register(router *httpx.Router) {
	protected := router.With(h.authenticate)
	protected.HandleFunc(http.MethodGet, "/api/v1/sticks", h.listSticks)
	protected.HandleFunc(http.MethodPost, "/api/v1/sticks", h.createStick)
	protected.HandleFunc(http.MethodGet, "/api/v1/sticks/archived", h.listArchivedSticks)
	protected.HandleFunc(http.MethodGet, "/api/v1/sticks/{id}", h.getStick)
	protected.HandleFunc(http.MethodPatch, "/api/v1/sticks/{id}", h.renameStick)
	protected.HandleFunc(http.MethodGet, "/api/v1/sticks/{id}/history", h.history)
	protected.HandleFunc(http.MethodPost, "/api/v1/sticks/{id}/claim", h.claimStick)
	protected.HandleFunc(http.MethodPost, "/api/v1/sticks/{id}/release", h.releaseStick)
	protected.HandleFunc(http.MethodPost, "/api/v1/sticks/{id}/archive", h.archiveStick)
	protected.HandleFunc(http.MethodPost, "/api/v1/sticks/{id}/unarchive", h.unarchiveStick)
	if h.notificationsEnabled {
		protected.HandleFunc(http.MethodPut, "/api/v1/sticks/{id}/subscription", h.subscribe)
		protected.HandleFunc(http.MethodDelete, "/api/v1/sticks/{id}/subscription", h.unsubscribe)
		protected.HandleFunc(http.MethodGet, "/api/v1/subscriptions", h.subscriptions)
	}
}

func (h *Handler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			httpx.Unauthorized(w)
			return
		}
		if h.validator == nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		identity, err := h.validator.Validate(r.Context(), parts[1])
		if err != nil {
			httpx.Unauthorized(w)
			return
		}
		identity = auth.WithAdminStatus(identity, h.admins)
		next.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), identity)))
	})
}
