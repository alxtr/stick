package sticks

import (
	"net/http"

	"stick/internal/auth"
)

func (h *Handler) listSticks(w http.ResponseWriter, r *http.Request) {
	sticks, err := h.service.ListSticks(r.Context())
	if err != nil {
		internalError(w, r, "list sticks", err)
		return
	}
	writeCollection(w, r, sticksToJSON(sticks))
}

func (h *Handler) listArchivedSticks(w http.ResponseWriter, r *http.Request) {
	identity := auth.IdentityFromContext(r.Context())
	if !identity.IsAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	sticks, err := h.service.ListArchivedSticks(r.Context(), identity)
	if err != nil {
		handleError(w, r, "list archived sticks", err)
		return
	}
	writeCollection(w, r, sticksToJSON(sticks))
}

func (h *Handler) getStick(w http.ResponseWriter, r *http.Request) {
	stick, err := h.service.GetStick(r.Context(), auth.IdentityFromContext(r.Context()), r.PathValue("id"))
	if err != nil {
		handleError(w, r, "get stick", err)
		return
	}
	setETag(w, stick.Version)
	if ifNoneMatch(headerValue(r, "If-None-Match"), etag(stick.Version)) {
		notModified(w)
		return
	}
	writeJSON(w, http.StatusOK, stickToJSON(stick))
}

func (h *Handler) createStick(w http.ResponseWriter, r *http.Request) {
	if !auth.IdentityFromContext(r.Context()).IsAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	var request struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	stick, err := h.service.CreateStick(r.Context(), auth.IdentityFromContext(r.Context()), request.Name)
	if err != nil {
		handleError(w, r, "create stick", err)
		return
	}
	setETag(w, stick.Version)
	w.Header().Set("Location", h.publicURL+apiPrefix+"/sticks/"+stick.ID)
	writeJSON(w, http.StatusCreated, stickToJSON(stick))
}

func (h *Handler) renameStick(w http.ResponseWriter, r *http.Request) {
	if !auth.IdentityFromContext(r.Context()).IsAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	var request struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	version, ok := h.expectedVersion(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	stick, err := h.service.RenameStick(r.Context(), auth.IdentityFromContext(r.Context()), r.PathValue("id"), request.Name, version)
	if err != nil {
		handleError(w, r, "rename stick", err)
		return
	}
	writeStick(w, stick)
}

func (h *Handler) archiveStick(w http.ResponseWriter, r *http.Request) {
	if !auth.IdentityFromContext(r.Context()).IsAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	version, ok := h.expectedVersion(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	stick, err := h.service.ArchiveStick(r.Context(), auth.IdentityFromContext(r.Context()), r.PathValue("id"), version)
	if err != nil {
		handleError(w, r, "archive stick", err)
		return
	}
	writeStick(w, stick)
}

func (h *Handler) unarchiveStick(w http.ResponseWriter, r *http.Request) {
	if !auth.IdentityFromContext(r.Context()).IsAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	version, ok := h.expectedVersion(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	stick, err := h.service.UnarchiveStick(r.Context(), auth.IdentityFromContext(r.Context()), r.PathValue("id"), version)
	if err != nil {
		handleError(w, r, "unarchive stick", err)
		return
	}
	writeStick(w, stick)
}

func (h *Handler) claimStick(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	version, ok := h.expectedVersion(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	stick, err := h.service.ClaimStick(r.Context(), auth.IdentityFromContext(r.Context()), r.PathValue("id"), request.Reason, version)
	if err != nil {
		handleError(w, r, "claim stick", err)
		return
	}
	writeStick(w, stick)
}

func (h *Handler) releaseStick(w http.ResponseWriter, r *http.Request) {
	version, ok := h.expectedVersion(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	stick, err := h.service.ReleaseStick(r.Context(), auth.IdentityFromContext(r.Context()), r.PathValue("id"), version)
	if err != nil {
		handleError(w, r, "release stick", err)
		return
	}
	writeStick(w, stick)
}

func (h *Handler) history(w http.ResponseWriter, r *http.Request) {
	identity := auth.IdentityFromContext(r.Context())
	id := r.PathValue("id")
	stick, err := h.service.GetStick(r.Context(), identity, id)
	if err != nil {
		handleError(w, r, "get stick history", err)
		return
	}
	setETag(w, stick.Version)
	limit, offset, ok := historyPagination(w, r)
	if !ok {
		return
	}
	sessions, total, err := h.service.GetHistory(r.Context(), identity, id, limit, offset)
	if err != nil {
		handleError(w, r, "get stick history", err)
		return
	}
	response := struct {
		Sessions []sessionJSON `json:"sessions"`
		Total    int           `json:"total"`
		Limit    int           `json:"limit"`
		Offset   int           `json:"offset"`
	}{
		Sessions: sessionsToJSON(sessions),
		Total:    total,
		Limit:    limit,
		Offset:   offset,
	}
	writeConditionalJSON(w, r, response)
}
