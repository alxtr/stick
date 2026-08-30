// Package sticks owns the browser routes below /sticks, including pages and
// their form mutations.
package sticks

import (
	"errors"
	"html/template"
	"net/http"

	"stick/internal/application"
	"stick/internal/auth"
	domain "stick/internal/core"
	"stick/internal/publicurl"
	"stick/internal/web/render"
	"stick/internal/web/views"
)

const (
	invalidStickNameMessage   = "Name must be non-empty, at most 100 characters, and contain only letters, digits, hyphens, and spaces."
	invalidClaimReasonMessage = "Reason must be non-empty and at most 500 characters."
	staleStickError           = "This stick changed since the page was loaded. Review its current state and try again."
)

// DetailFormState is the detail form state used at the route boundary.
type DetailFormState = views.DetailFormState

// NewStickFormState is the new-stick form state used at the route boundary.
type NewStickFormState = views.NewStickFormState

// Handler serves the /sticks pages and mutations.
type Handler struct {
	service              *application.Service
	publicURL            publicurl.URL
	notificationsEnabled bool
	renderer             render.Renderer
	detailTemplate       *template.Template
	newStickTemplate     *template.Template
}

// New returns a handler for the /sticks route family.
func New(
	service *application.Service,
	publicURL publicurl.URL,
	renderer render.Renderer,
	detailTemplate, newStickTemplate *template.Template,
	notificationsEnabled bool,
) *Handler {
	return &Handler{
		service:              service,
		publicURL:            publicURL,
		notificationsEnabled: notificationsEnabled,
		renderer:             renderer,
		detailTemplate:       detailTemplate,
		newStickTemplate:     newStickTemplate,
	}
}

// NewStick renders the new-stick form.
func (h *Handler) NewStick(w http.ResponseWriter, r *http.Request) {
	identity := auth.IdentityFromContext(r.Context())
	if !identity.IsAdmin {
		h.renderer.ErrorPage(w, r, http.StatusForbidden)
		return
	}
	h.renderNewStick(w, r, http.StatusOK, NewStickFormState{})
}

// Detail renders the stick detail page and session history.
func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	identity := auth.IdentityFromContext(r.Context())
	currentStick, err := h.service.GetStick(r.Context(), identity, r.PathValue("id"))
	if err != nil {
		h.handlePageError(w, r, "get stick", err)
		return
	}
	h.renderStickDetail(w, r, currentStick, http.StatusOK, DetailFormState{})
}

func (h *Handler) renderNewStick(w http.ResponseWriter, r *http.Request, status int, form NewStickFormState) {
	h.renderer.RenderStatus(w, r, "render new stick", h.newStickTemplate,
		h.renderer.Mapper.NewStick(r, auth.IdentityFromContext(r.Context()), form), status)
}

func (h *Handler) renderCurrentStickDetail(
	w http.ResponseWriter,
	r *http.Request,
	id string,
	status int,
	form DetailFormState,
) {
	identity := auth.IdentityFromContext(r.Context())
	stick, err := h.service.GetStick(r.Context(), identity, id)
	if err != nil {
		h.handlePageError(w, r, "get current stick", err)
		return
	}
	h.renderStickDetail(w, r, stick, status, form)
}

func (h *Handler) renderStickDetail(
	w http.ResponseWriter,
	r *http.Request,
	currentStick domain.Stick,
	status int,
	form DetailFormState,
) {
	identity := auth.IdentityFromContext(r.Context())
	id := currentStick.ID
	page := parseHistoryPage(r.URL.Query().Get("page"))
	offset := (page - 1) * pageSize

	sessions, totalSessions, err := h.service.GetHistory(r.Context(), identity, id, pageSize, offset)
	if err != nil {
		h.handlePageError(w, r, "get stick history", err)
		return
	}
	totalPages := (totalSessions + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}

	var subscribedStickIDs []string
	if h.notificationsEnabled && needsStickSubscriptionViewState(currentStick, identity) {
		subscribedStickIDs, err = h.service.SubscribedStickIDs(r.Context(), identity)
		if err != nil {
			h.handlePageError(w, r, "list subscriptions", err)
			return
		}
	}

	h.renderer.RenderStatus(w, r, "render stick detail", h.detailTemplate, h.renderer.Mapper.Detail(
		r,
		identity,
		currentStick,
		sessions,
		page,
		totalPages,
		totalSessions,
		historyPageLinks(page, totalPages),
		subscribedStickIDs,
		form,
	), status)
}

func (h *Handler) handlePageError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	switch {
	case errors.Is(err, application.ErrNotFound):
		h.renderer.ErrorPage(w, r, http.StatusNotFound)
	case errors.Is(err, application.ErrForbidden):
		h.renderer.ErrorPage(w, r, http.StatusForbidden)
	default:
		h.renderer.InternalError(w, r, operation, err)
	}
}

func needsStickSubscriptionViewState(stick domain.Stick, identity domain.Identity) bool {
	return !stick.Archived() && !stick.Available() && stick.Holder.Sub != identity.Sub
}

func (h *Handler) requireAdminMutation(w http.ResponseWriter, r *http.Request) bool {
	if auth.IdentityFromContext(r.Context()).IsAdmin {
		return true
	}
	h.renderer.ErrorPage(w, r, http.StatusForbidden)
	return false
}

func (h *Handler) parseForm(w http.ResponseWriter, r *http.Request) bool {
	if err := parseBoundedForm(w, r); err == nil {
		return true
	}
	h.renderer.ErrorPage(w, r, http.StatusBadRequest)
	return false
}

func (h *Handler) handleCommonMutationError(w http.ResponseWriter, r *http.Request, id string, err error) bool {
	switch {
	case errors.Is(err, application.ErrNotFound):
		h.renderer.ErrorPage(w, r, http.StatusNotFound)
	case errors.Is(err, application.ErrForbidden):
		h.renderer.ErrorPage(w, r, http.StatusForbidden)
	case errors.Is(err, application.ErrVersionConflict):
		h.renderCurrentStickDetail(w, r, id, http.StatusConflict, DetailFormState{Alert: staleStickError})
	case errors.Is(err, domain.ErrVersionExhausted):
		h.renderCurrentStickDetail(w, r, id, http.StatusConflict, DetailFormState{
			Alert: "This stick can no longer be changed. Review its current state and contact an administrator.",
		})
	default:
		return false
	}
	return true
}

func (h *Handler) expectedFormVersion(w http.ResponseWriter, r *http.Request) (int64, bool) {
	version, err := parseVersion(r.Form.Get("version"))
	if err != nil || version < 1 {
		h.renderer.ErrorPage(w, r, http.StatusBadRequest)
		return 0, false
	}
	return version, true
}
