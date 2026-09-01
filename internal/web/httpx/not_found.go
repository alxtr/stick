package httpx

import "net/http"

// NotFound writes the standard JSON response used for unknown routes.
func NotFound(w http.ResponseWriter, _ *http.Request) {
	WriteError(w, http.StatusNotFound, "not found")
}
