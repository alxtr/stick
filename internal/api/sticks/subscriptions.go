package sticks

import (
	"net/http"

	"stick/internal/auth"
	"stick/internal/web/httpx"
)

func (h *Handler) subscribe(w http.ResponseWriter, r *http.Request) {
	version, ok := h.expectedVersion(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if err := h.service.Subscribe(r.Context(), auth.IdentityFromContext(r.Context()), r.PathValue("id"), version); err != nil {
		handleError(w, r, "subscribe to stick", err)
		return
	}
	httpx.SetETag(w, version)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) unsubscribe(w http.ResponseWriter, r *http.Request) {
	version, ok := h.expectedVersion(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if err := h.service.Unsubscribe(r.Context(), auth.IdentityFromContext(r.Context()), r.PathValue("id"), version); err != nil {
		handleError(w, r, "unsubscribe from stick", err)
		return
	}
	httpx.SetETag(w, version)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) subscriptions(w http.ResponseWriter, r *http.Request) {
	ids, err := h.service.SubscribedStickIDs(r.Context(), auth.IdentityFromContext(r.Context()))
	if err != nil {
		httpx.InternalError(w, r, "list subscriptions", err)
		return
	}
	if ids == nil {
		ids = []string{}
	}
	httpx.WriteCollection(w, r, ids)
}
