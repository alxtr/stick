// Package dashboard owns the browser dashboard at the application root.
package dashboard

import (
	"errors"
	"html/template"
	"net/http"

	"stick/internal/application"
	"stick/internal/auth"
	domain "stick/internal/core"
	"stick/internal/web/render"
)

// Handler serves the dashboard route.
type Handler struct {
	service              *application.Service
	renderer             render.Renderer
	notificationsEnabled bool
	dashboardTemplate    *template.Template
}

// New returns a dashboard handler. The template is supplied separately by the
// root composition package because content parsing is a composition concern.
func New(service *application.Service, renderer render.Renderer, dashboardTemplate *template.Template, notificationsEnabled bool) *Handler {
	return &Handler{
		service:              service,
		renderer:             renderer,
		notificationsEnabled: notificationsEnabled,
		dashboardTemplate:    dashboardTemplate,
	}
}

// Dashboard renders the active-stick dashboard.
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	identity := auth.IdentityFromContext(r.Context())
	sticks, err := h.service.ListSticks(r.Context())
	if err != nil {
		h.renderer.InternalError(w, r, "list sticks", err)
		return
	}
	archivedSticks := []domain.Stick(nil)
	if identity.IsAdmin {
		archivedSticks, err = h.service.ListArchivedSticks(r.Context(), identity)
		if err != nil {
			h.handlePageError(w, r, err)
			return
		}
	}
	var subscribedStickIDs []string
	if h.notificationsEnabled && needsSubscriptionViewState(sticks, identity) {
		subscribedStickIDs, err = h.service.SubscribedStickIDs(r.Context(), identity)
		if err != nil {
			h.renderer.InternalError(w, r, "list subscriptions", err)
			return
		}
	}

	h.renderer.Render(w, r, "render dashboard", h.dashboardTemplate, h.renderer.Mapper.Dashboard(
		r, identity, sticks, subscribedStickIDs, archivedSticks,
	))
}

func (h *Handler) handlePageError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, application.ErrNotFound):
		h.renderer.ErrorPage(w, r, http.StatusNotFound)
	case errors.Is(err, application.ErrForbidden):
		h.renderer.ErrorPage(w, r, http.StatusForbidden)
	default:
		h.renderer.InternalError(w, r, "list archived sticks", err)
	}
}

func needsSubscriptionViewState(sticks []domain.Stick, identity domain.Identity) bool {
	for _, stick := range sticks {
		if !stick.Archived() && !stick.Available() && stick.Holder.Sub != identity.Sub {
			return true
		}
	}
	return false
}
