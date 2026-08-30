package sticks

import (
	"net/http"
	"strconv"

	"stick/internal/web/security"
)

// parseBoundedForm parses a browser form request body with a conservative
// limit. The limit is a UI transport policy rather than a generic HTTP rule.
func parseBoundedForm(response http.ResponseWriter, request *http.Request) error {
	return security.ParseBoundedForm(response, request)
}

func parseVersion(raw string) (int64, error) { return strconv.ParseInt(raw, 10, 64) }
