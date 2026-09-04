package httpx

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const maxJSONBodyBytes = 1 << 20

// WriteCollection writes a JSON collection with a content-derived ETag and
// supports conditional requests.
func WriteCollection(w http.ResponseWriter, r *http.Request, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	sum := sha256.Sum256(data)
	collectionETag := `"` + hex.EncodeToString(sum[:]) + `"`
	w.Header().Set("ETag", collectionETag)
	if IfNoneMatch(HeaderValue(r, "If-None-Match"), collectionETag) {
		NotModified(w)
		return
	}
	WriteData(w, http.StatusOK, data)
}

// WriteConditionalJSON writes a JSON response with a content-derived ETag
// and supports conditional requests.
func WriteConditionalJSON(w http.ResponseWriter, r *http.Request, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	sum := sha256.Sum256(data)
	representationETag := `"` + hex.EncodeToString(sum[:]) + `"`
	w.Header().Set("ETag", representationETag)
	if IfNoneMatch(HeaderValue(r, "If-None-Match"), representationETag) {
		NotModified(w)
		return
	}
	WriteData(w, http.StatusOK, data)
}

// HeaderValue joins repeated request header values using the standard
// comma-separated representation.
func HeaderValue(r *http.Request, name string) string {
	return strings.Join(r.Header.Values(name), ",")
}

// WriteJSON marshals value and writes it as a JSON response.
func WriteJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	WriteData(w, status, data)
}

// WriteData writes already-marshaled JSON HTTP response data.
func WriteData(w http.ResponseWriter, status int, data []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

// NotModified writes a response for a satisfied conditional request.
func NotModified(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotModified)
}

// WriteError writes the standard JSON error response.
func WriteError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, struct {
		Error string `json:"error"`
	}{Error: message})
}

// SetETag sets an entity tag for a numeric resource version.
func SetETag(w http.ResponseWriter, version int64) { w.Header().Set("ETag", ETag(version)) }

// ETag returns the entity tag for a numeric resource version.
func ETag(version int64) string { return `"` + strconv.FormatInt(version, 10) + `"` }

// IfNoneMatch reports whether one of the supplied entity tags matches the
// current representation.
func IfNoneMatch(header, current string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "*" {
			return true
		}
		if strings.HasPrefix(part, "W/") {
			part = strings.TrimSpace(strings.TrimPrefix(part, "W/"))
		}
		if part == current {
			return true
		}
	}
	return false
}

// DecodeJSON decodes one strict JSON request body into destination.
func DecodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

// Unauthorized writes the standard bearer authentication error response.
func Unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer`)
	WriteError(w, http.StatusUnauthorized, "unauthorized")
}

// InternalError logs a request failure and writes a generic server error.
func InternalError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	LogError(r.Context(), "request failed", operation, err)
	WriteError(w, http.StatusInternalServerError, "internal server error")
}
