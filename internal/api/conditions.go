package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"stick/internal/auth"
)

func (h *Handler) expectedVersion(w http.ResponseWriter, r *http.Request, id string) (int64, bool) {
	header := headerValue(r, "If-Match")
	if strings.TrimSpace(header) == "" {
		writeError(w, http.StatusPreconditionRequired, "If-Match header is required")
		return 0, false
	}
	tags, wildcard, err := parseIfMatch(header)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid If-Match header")
		return 0, false
	}
	if len(tags) == 1 && !wildcard {
		return tags[0], true
	}

	// A wildcard or a list of tags must be compared to the current
	// representation before choosing the version passed to the service.
	stick, err := h.service.GetStick(r.Context(), auth.IdentityFromContext(r.Context()), id)
	if err != nil {
		handleError(w, r, "get stick", err)
		return 0, false
	}
	if !wildcard {
		matched := false
		for _, tag := range tags {
			if tag == stick.Version {
				matched = true
				break
			}
		}
		if !matched {
			writeError(w, http.StatusPreconditionFailed, "precondition failed")
			return 0, false
		}
	}
	return stick.Version, true
}

func historyPagination(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	query := r.URL.Query()
	limit := defaultHistoryLimit
	offset := 0
	var err error
	if raw := query.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maxHistoryLimit {
			writeError(w, http.StatusBadRequest, "invalid history limit")
			return 0, 0, false
		}
	}
	if raw := query.Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > maxHistoryOffset {
			writeError(w, http.StatusBadRequest, "invalid history offset")
			return 0, 0, false
		}
	}
	return limit, offset, true
}

func parseIfMatch(value string) ([]int64, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false, errors.New("missing If-Match")
	}
	if value == "*" {
		return nil, true, nil
	}
	parts := strings.Split(value, ",")
	tags := make([]int64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len(part) < 3 || part[0] != '"' || part[len(part)-1] != '"' || strings.HasPrefix(part, "W/") {
			return nil, false, errors.New("invalid entity tag")
		}
		version, err := strconv.ParseInt(part[1:len(part)-1], 10, 64)
		if err != nil || version < 1 || etag(version) != part {
			return nil, false, errors.New("invalid entity tag")
		}
		tags = append(tags, version)
	}
	if len(tags) == 0 {
		return nil, false, errors.New("missing entity tag")
	}
	return tags, false, nil
}

func ifNoneMatch(header, current string) bool {
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
