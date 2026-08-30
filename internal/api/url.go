package api

import (
	"fmt"
	"strings"

	"stick/internal/publicurl"
)

// Path prefixes a trusted application-relative path, query string, or
// ServeMux pattern with the configured mount path.
func Path(publicURL publicurl.URL, reference string) string {
	if reference == "" || reference[0] != '/' || strings.HasPrefix(reference, "//") || strings.ContainsAny(reference, `\#`) {
		panic(fmt.Sprintf("invalid application-relative reference %q", reference))
	}
	if publicURL.MountPath() == "" {
		return reference
	}
	return publicURL.MountPath() + reference
}

// Absolute returns an absolute URL for a trusted application-relative
// reference.
func Absolute(publicURL publicurl.URL, reference string) string {
	return publicURL.Origin() + Path(publicURL, reference)
}
