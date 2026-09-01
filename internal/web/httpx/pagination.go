package httpx

import (
	"fmt"
	"net/http"
	"strconv"
)

// PaginationOptions controls the accepted pagination range.
type PaginationOptions struct {
	DefaultLimit int
	MaxLimit     int
	MaxOffset    int
}

// PaginationError identifies the invalid pagination parameter.
type PaginationError struct {
	Parameter string
}

func (e *PaginationError) Error() string {
	return fmt.Sprintf("invalid pagination parameter %q", e.Parameter)
}

// ParsePagination parses the conventional limit and offset query parameters.
func ParsePagination(r *http.Request, options PaginationOptions) (int, int, error) {
	query := r.URL.Query()
	limit := options.DefaultLimit
	offset := 0
	if raw := query.Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > options.MaxLimit {
			return 0, 0, &PaginationError{Parameter: "limit"}
		}
		limit = value
	}
	if raw := query.Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > options.MaxOffset {
			return 0, 0, &PaginationError{Parameter: "offset"}
		}
		offset = value
	}
	return limit, offset, nil
}
