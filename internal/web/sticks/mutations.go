package sticks

import (
	"errors"
	"net/http"

	"stick/internal/application"
	"stick/internal/auth"
	domain "stick/internal/core"
	"stick/internal/web/httpx"
)

// CreateStick handles the new-stick form submission.
func (h *Handler) CreateStick(w http.ResponseWriter, r *http.Request) {
	if !auth.IdentityFromContext(r.Context()).IsAdmin {
		h.renderer.ErrorPage(w, r, http.StatusForbidden)
		return
	}
	if !h.parseForm(w, r) {
		return
	}
	name := r.Form.Get("name")
	if _, err := h.service.CreateStick(r.Context(), auth.IdentityFromContext(r.Context()), name); err != nil {
		if errors.Is(err, domain.ErrInvalidStickName) {
			h.renderNewStick(w, r, http.StatusUnprocessableEntity, NewStickFormState{
				Name:      name,
				NameError: invalidStickNameMessage,
			})
			return
		}
		if errors.Is(err, application.ErrAlreadyExists) {
			h.renderNewStick(w, r, http.StatusConflict, NewStickFormState{
				Name:  name,
				Alert: "The stick could not be created because it conflicts with current data. Review it and try again.",
			})
			return
		}
		if errors.Is(err, application.ErrForbidden) {
			h.renderer.ErrorPage(w, r, http.StatusForbidden)
			return
		}
		h.renderer.InternalError(w, r, "create stick", err)
		return
	}
	http.Redirect(w, r, httpx.Path(h.publicURL, "/"), http.StatusSeeOther)
}

// Rename handles the stick rename form submission.
func (h *Handler) Rename(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminMutation(w, r) {
		return
	}
	id := r.PathValue("id")
	if !h.parseForm(w, r) {
		return
	}
	name := r.Form.Get("name")
	expectedVersion, ok := h.expectedFormVersion(w, r)
	if !ok {
		return
	}
	if _, err := h.service.RenameStick(r.Context(), auth.IdentityFromContext(r.Context()), id, name, expectedVersion); err != nil {
		if errors.Is(err, domain.ErrInvalidStickName) {
			h.renderCurrentStickDetail(w, r, id, http.StatusUnprocessableEntity, DetailFormState{
				RenameValue: &name,
				RenameError: invalidStickNameMessage,
			})
			return
		}
		if h.handleCommonMutationError(w, r, id, err) {
			return
		}
		h.renderer.InternalError(w, r, "rename stick", err)
		return
	}
	http.Redirect(w, r, h.stickPath(id, ""), http.StatusSeeOther)
}

// Archive handles the stick archive form submission.
func (h *Handler) Archive(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminMutation(w, r) {
		return
	}
	id := r.PathValue("id")
	if !h.parseForm(w, r) {
		return
	}
	expectedVersion, ok := h.expectedFormVersion(w, r)
	if !ok {
		return
	}
	if _, err := h.service.ArchiveStick(r.Context(), auth.IdentityFromContext(r.Context()), id, expectedVersion); err != nil {
		if errors.Is(err, domain.ErrHeld) {
			h.renderCurrentStickDetail(w, r, id, http.StatusConflict, DetailFormState{
				Alert: "This stick is currently held. It must be put down before it can be archived.",
			})
			return
		}
		if errors.Is(err, domain.ErrAlreadyArchived) {
			h.renderCurrentStickDetail(w, r, id, http.StatusConflict, DetailFormState{
				Alert: "This stick is already archived. Review its current state before trying another action.",
			})
			return
		}
		if h.handleCommonMutationError(w, r, id, err) {
			return
		}
		h.renderer.InternalError(w, r, "archive stick", err)
		return
	}
	http.Redirect(w, r, httpx.Path(h.publicURL, "/"), http.StatusSeeOther)
}

// Unarchive handles the stick restore form submission.
func (h *Handler) Unarchive(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminMutation(w, r) {
		return
	}
	id := r.PathValue("id")
	if !h.parseForm(w, r) {
		return
	}
	expectedVersion, ok := h.expectedFormVersion(w, r)
	if !ok {
		return
	}
	if _, err := h.service.UnarchiveStick(r.Context(), auth.IdentityFromContext(r.Context()), id, expectedVersion); err != nil {
		if errors.Is(err, domain.ErrNotArchived) {
			h.renderCurrentStickDetail(w, r, id, http.StatusConflict, DetailFormState{
				Alert: "This stick is not archived. Review its current state before trying another action.",
			})
			return
		}
		if h.handleCommonMutationError(w, r, id, err) {
			return
		}
		h.renderer.InternalError(w, r, "unarchive stick", err)
		return
	}
	http.Redirect(w, r, httpx.Path(h.publicURL, "/"), http.StatusSeeOther)
}

// Claim handles the stick claim form submission.
func (h *Handler) Claim(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	identity := auth.IdentityFromContext(r.Context())
	if !h.parseForm(w, r) {
		return
	}
	reason := r.Form.Get("reason")
	expectedVersion, ok := h.expectedFormVersion(w, r)
	if !ok {
		return
	}
	if _, err := h.service.ClaimStick(r.Context(), identity, id, reason, expectedVersion); err != nil {
		if errors.Is(err, domain.ErrInvalidClaimReason) {
			h.renderCurrentStickDetail(w, r, id, http.StatusUnprocessableEntity, DetailFormState{
				ClaimReasonValue: reason,
				ClaimReasonError: invalidClaimReasonMessage,
			})
			return
		}
		if errors.Is(err, domain.ErrAlreadyHeld) {
			h.renderCurrentStickDetail(w, r, id, http.StatusConflict, DetailFormState{
				Alert: "This stick is already held. Review its current state and try again when it is available.",
			})
			return
		}
		if errors.Is(err, domain.ErrAlreadyArchived) {
			h.renderCurrentStickDetail(w, r, id, http.StatusConflict, DetailFormState{
				Alert: "This stick is archived and cannot be claimed. Review its current state before trying another action.",
			})
			return
		}
		if h.handleCommonMutationError(w, r, id, err) {
			return
		}
		h.renderer.InternalError(w, r, "claim stick", err)
		return
	}
	http.Redirect(w, r, h.stickPath(id, ""), http.StatusSeeOther)
}

// Release handles the stick release form submission.
func (h *Handler) Release(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	identity := auth.IdentityFromContext(r.Context())
	if !h.parseForm(w, r) {
		return
	}
	expectedVersion, ok := h.expectedFormVersion(w, r)
	if !ok {
		return
	}

	if _, err := h.service.ReleaseStick(r.Context(), identity, id, expectedVersion); err != nil {
		if errors.Is(err, domain.ErrNotHolder) {
			h.renderCurrentStickDetail(w, r, id, http.StatusForbidden, DetailFormState{
				Alert: "You can only put down a stick that you currently hold.",
			})
			return
		}
		if h.handleCommonMutationError(w, r, id, err) {
			return
		}
		h.renderer.InternalError(w, r, "release stick", err)
		return
	}

	http.Redirect(w, r, safeRedirect(r, h.publicURL, stickReference(id, "")), http.StatusSeeOther)
}

// Subscribe handles the notification subscription form submission.
func (h *Handler) Subscribe(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	identity := auth.IdentityFromContext(r.Context())
	if !h.parseForm(w, r) {
		return
	}
	expectedVersion, ok := h.expectedFormVersion(w, r)
	if !ok {
		return
	}
	if err := h.service.Subscribe(r.Context(), identity, id, expectedVersion); err != nil {
		if h.handleCommonMutationError(w, r, id, err) {
			return
		}
		h.renderer.InternalError(w, r, "subscribe to stick", err)
		return
	}
	http.Redirect(w, r, safeRedirect(r, h.publicURL, stickReference(id, "")), http.StatusSeeOther)
}

// Unsubscribe handles the notification unsubscription form submission.
func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	identity := auth.IdentityFromContext(r.Context())
	if !h.parseForm(w, r) {
		return
	}
	expectedVersion, ok := h.expectedFormVersion(w, r)
	if !ok {
		return
	}
	if err := h.service.Unsubscribe(r.Context(), identity, id, expectedVersion); err != nil {
		if h.handleCommonMutationError(w, r, id, err) {
			return
		}
		h.renderer.InternalError(w, r, "unsubscribe from stick", err)
		return
	}
	http.Redirect(w, r, safeRedirect(r, h.publicURL, stickReference(id, "")), http.StatusSeeOther)
}
