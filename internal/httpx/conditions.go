package httpx

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// ParseIfMatch parses one numeric HTTP entity tag from an If-Match value.
func ParseIfMatch(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("missing If-Match")
	}
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' || strings.HasPrefix(value, "W/") {
		return 0, errors.New("invalid entity tag")
	}
	version, err := strconv.ParseInt(value[1:len(value)-1], 10, 64)
	if err != nil || version < 1 || ETag(version) != value {
		return 0, errors.New("invalid entity tag")
	}
	return version, nil
}

// IfMatchVersion validates the required If-Match header and returns its
// numeric version.
func IfMatchVersion(w http.ResponseWriter, r *http.Request) (int64, bool) {
	header := HeaderValue(r, "If-Match")
	if strings.TrimSpace(header) == "" {
		WriteError(w, http.StatusPreconditionRequired, "If-Match header is required")
		return 0, false
	}
	version, err := ParseIfMatch(header)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid If-Match header")
		return 0, false
	}
	return version, true
}
