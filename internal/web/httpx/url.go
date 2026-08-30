package httpx

import (
	"fmt"
	"net/url"
	"path"
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

// CookiePath returns the narrowest cookie path covering the application.
func CookiePath(publicURL publicurl.URL) string {
	if mountPath := publicURL.MountPath(); mountPath != "" {
		return mountPath
	}
	return "/"
}

// ContainsLocation reports whether location is a local absolute-path redirect
// contained by the application mount. Encoded or literal traversal, network
// paths, authorities, and backslashes are rejected.
func ContainsLocation(publicURL publicurl.URL, location string) bool {
	if location == "" || location[0] != '/' || strings.HasPrefix(location, "//") || strings.Contains(location, `\`) {
		return false
	}
	parsed, err := url.Parse(location)
	if err != nil || parsed.IsAbs() || parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil {
		return false
	}
	if parsed.Path == "" || parsed.Path[0] != '/' || strings.Contains(parsed.Path, `\`) {
		return false
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	clean := path.Clean(parsed.Path)
	mountPath := publicURL.MountPath()
	if mountPath == "" {
		return strings.HasPrefix(clean, "/")
	}
	return clean == mountPath || strings.HasPrefix(clean, mountPath+"/")
}
