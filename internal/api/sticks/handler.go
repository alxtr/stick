// Package sticks provides the authenticated Stick REST API route group.
package sticks

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

// RouteRegistrar is the part of the API router needed to register routes.
// Keeping the interface here avoids coupling route groups to the server's
// router implementation.
type RouteRegistrar interface {
	HandleFunc(method, reference string, handler http.HandlerFunc, middlewares ...func(http.Handler) http.Handler)
}

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
func (h *Handler) Register(router RouteRegistrar) {
	h.register(router, http.MethodGet, apiPrefix+"/sticks", h.listSticks)
	h.register(router, http.MethodPost, apiPrefix+"/sticks", h.createStick)
	h.register(router, http.MethodGet, apiPrefix+"/sticks/archived", h.listArchivedSticks)
	h.register(router, http.MethodGet, apiPrefix+"/sticks/{id}", h.getStick)
	h.register(router, http.MethodPatch, apiPrefix+"/sticks/{id}", h.renameStick)
	h.register(router, http.MethodGet, apiPrefix+"/sticks/{id}/history", h.history)
	h.register(router, http.MethodPost, apiPrefix+"/sticks/{id}/claim", h.claimStick)
	h.register(router, http.MethodPost, apiPrefix+"/sticks/{id}/release", h.releaseStick)
	h.register(router, http.MethodPost, apiPrefix+"/sticks/{id}/archive", h.archiveStick)
	h.register(router, http.MethodPost, apiPrefix+"/sticks/{id}/unarchive", h.unarchiveStick)
	if h.notificationsEnabled {
		h.register(router, http.MethodPut, apiPrefix+"/sticks/{id}/subscription", h.subscribe)
		h.register(router, http.MethodDelete, apiPrefix+"/sticks/{id}/subscription", h.unsubscribe)
		h.register(router, http.MethodGet, apiPrefix+"/subscriptions", h.subscriptions)
	}
}

func (h *Handler) register(router RouteRegistrar, method, reference string, handler http.HandlerFunc) {
	router.HandleFunc(method, reference, handler, h.authenticate)
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
