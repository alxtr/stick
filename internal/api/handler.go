// Package api provides the JSON REST API for Stick.
package api

import (
	"context"
	"net/http"
	"strings"

	"stick/internal/application"
	"stick/internal/auth"
	domain "stick/internal/core"
)

const (
	apiPrefix           = "/api/v1"
	maxJSONBodyBytes    = 1 << 20
	defaultHistoryLimit = 20
	maxHistoryLimit     = 100
	maxHistoryOffset    = 100_000
)

// Handler serves the Stick REST API.
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

// New returns an API handler.
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

// Register adds all API routes to router. Authentication is applied to the
// complete API route family; health probes remain unauthenticated.
func (h *Handler) Register(router *Router) {
	protected := router.With(h.authenticate)
	protected.HandleFunc(http.MethodGet, apiPrefix+"/sticks", h.listSticks)
	protected.HandleFunc(http.MethodPost, apiPrefix+"/sticks", h.createStick)
	protected.HandleFunc(http.MethodGet, apiPrefix+"/sticks/archived", h.listArchivedSticks)
	protected.HandleFunc(http.MethodGet, apiPrefix+"/sticks/{id}", h.getStick)
	protected.HandleFunc(http.MethodPatch, apiPrefix+"/sticks/{id}", h.renameStick)
	protected.HandleFunc(http.MethodGet, apiPrefix+"/sticks/{id}/history", h.history)
	protected.HandleFunc(http.MethodPost, apiPrefix+"/sticks/{id}/claim", h.claimStick)
	protected.HandleFunc(http.MethodPost, apiPrefix+"/sticks/{id}/release", h.releaseStick)
	protected.HandleFunc(http.MethodPost, apiPrefix+"/sticks/{id}/archive", h.archiveStick)
	protected.HandleFunc(http.MethodPost, apiPrefix+"/sticks/{id}/unarchive", h.unarchiveStick)
	if h.notificationsEnabled {
		protected.HandleFunc(http.MethodPut, apiPrefix+"/sticks/{id}/subscription", h.subscribe)
		protected.HandleFunc(http.MethodDelete, apiPrefix+"/sticks/{id}/subscription", h.unsubscribe)
		protected.HandleFunc(http.MethodGet, apiPrefix+"/subscriptions", h.subscriptions)
	}
}

// NotFound writes the JSON response used for unknown API routes.
func NotFound(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, "not found")
}

func (h *Handler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			unauthorized(w)
			return
		}
		if h.validator == nil {
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		identity, err := h.validator.Validate(r.Context(), parts[1])
		if err != nil {
			unauthorized(w)
			return
		}
		identity = auth.WithAdminStatus(identity, h.admins)
		next.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), identity)))
	})
}
