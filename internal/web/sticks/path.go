package sticks

import (
	"net/http"
	"net/url"

	"stick/internal/publicurl"
	"stick/internal/web/httpx"
)

func (h *Handler) stickPath(id, suffix string) string {
	return httpx.Path(h.publicURL, stickReference(id, suffix))
}

func stickReference(id, suffix string) string {
	return "/sticks/" + url.PathEscape(id) + suffix
}

// safeRedirect returns redirect_to when it is safely contained by the
// application mount, otherwise the mounted fallback.
func safeRedirect(r *http.Request, publicURL publicurl.URL, fallback string) string {
	fallback = httpx.Path(publicURL, fallback)
	to := r.Form.Get("redirect_to")
	if !httpx.ContainsLocation(publicURL, to) {
		return fallback
	}
	return to
}
