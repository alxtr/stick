package sticks

import (
	"net/http"

	"stick/internal/auth"
	"stick/internal/httpx"
)

func (h *Handler) listSticks(w http.ResponseWriter, r *http.Request) {
	sticks, err := h.service.ListSticks(r.Context())
	if err != nil {
		httpx.InternalError(w, r, "list sticks", err)
		return
	}
	httpx.WriteCollection(w, r, sticksToJSON(sticks))
}

func (h *Handler) listArchivedSticks(w http.ResponseWriter, r *http.Request) {
	identity := auth.IdentityFromContext(r.Context())
	if !identity.IsAdmin {
		httpx.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	sticks, err := h.service.ListArchivedSticks(r.Context(), identity)
	if err != nil {
		handleError(w, r, "list archived sticks", err)
		return
	}
	httpx.WriteCollection(w, r, sticksToJSON(sticks))
}

func (h *Handler) getStick(w http.ResponseWriter, r *http.Request) {
	stick, err := h.service.GetStick(r.Context(), auth.IdentityFromContext(r.Context()), r.PathValue("id"))
	if err != nil {
		handleError(w, r, "get stick", err)
		return
	}
	httpx.SetETag(w, stick.Version)
	if httpx.IfNoneMatch(httpx.HeaderValue(r, "If-None-Match"), httpx.ETag(stick.Version)) {
		httpx.NotModified(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, stickToJSON(stick))
}

func (h *Handler) createStick(w http.ResponseWriter, r *http.Request) {
	if !auth.IdentityFromContext(r.Context()).IsAdmin {
		httpx.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	var request struct {
		Name string `json:"name"`
	}
	if !httpx.DecodeJSON(w, r, &request) {
		return
	}
	stick, err := h.service.CreateStick(r.Context(), auth.IdentityFromContext(r.Context()), request.Name)
	if err != nil {
		handleError(w, r, "create stick", err)
		return
	}
	httpx.SetETag(w, stick.Version)
	w.Header().Set("Location", h.publicURL+apiPrefix+"/sticks/"+stick.ID)
	httpx.WriteJSON(w, http.StatusCreated, stickToJSON(stick))
}

func (h *Handler) renameStick(w http.ResponseWriter, r *http.Request) {
	if !auth.IdentityFromContext(r.Context()).IsAdmin {
		httpx.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	var request struct {
		Name string `json:"name"`
	}
	if !httpx.DecodeJSON(w, r, &request) {
		return
	}
	version, ok := httpx.IfMatchVersion(w, r)
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
		httpx.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	version, ok := httpx.IfMatchVersion(w, r)
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
		httpx.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	version, ok := httpx.IfMatchVersion(w, r)
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
	if !httpx.DecodeJSON(w, r, &request) {
		return
	}
	version, ok := httpx.IfMatchVersion(w, r)
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
	version, ok := httpx.IfMatchVersion(w, r)
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
	httpx.SetETag(w, stick.Version)
	limit, offset, err := httpx.ParsePagination(r, httpx.PaginationOptions{
		DefaultLimit: defaultHistoryLimit,
		MaxLimit:     maxHistoryLimit,
		MaxOffset:    maxHistoryOffset,
	})
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid history pagination")
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
	httpx.WriteConditionalJSON(w, r, response)
}
