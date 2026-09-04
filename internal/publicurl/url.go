// Package publicurl validates and normalizes the application's public URL.
package publicurl

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Parse validates and normalizes the complete public application URL. A lone
// trailing slash is accepted. Mount segments are deliberately limited to a
// conservative set of ASCII URL characters so they are safe in URLs, ServeMux
// patterns, and cookie paths without context-specific escaping.
//
// The normalized URL is returned as a string. Consumers that need the mount
// path should derive it at the routing boundary; other consumers can keep the
// complete public URL unchanged.
func Parse(publicURL string) (string, error) {
	if publicURL == "" {
		return "", invalidPublicURL(publicURL, "must not be empty")
	}
	for _, r := range publicURL {
		if r > 0x7f || r <= 0x20 || r == 0x7f {
			return "", invalidPublicURL(publicURL, "must contain only visible ASCII characters")
		}
	}
	if strings.ContainsAny(publicURL, `\%`) {
		return "", invalidPublicURL(publicURL, "backslashes and percent escapes are not supported")
	}

	parsed, err := url.Parse(publicURL)
	if err != nil {
		return "", fmt.Errorf("invalid PUBLIC_URL %q: %w", publicURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", invalidPublicURL(publicURL, "scheme must be http or https")
	}
	if !parsed.IsAbs() || parsed.Host == "" || parsed.Hostname() == "" || parsed.Opaque != "" {
		return "", invalidPublicURL(publicURL, "must be an absolute URL with a hostname")
	}
	if parsed.User != nil {
		return "", invalidPublicURL(publicURL, "user information is not supported")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(publicURL, "#") {
		return "", invalidPublicURL(publicURL, "must not include a query or fragment")
	}
	if err := validateAuthority(parsed); err != nil {
		return "", invalidPublicURL(publicURL, err.Error())
	}

	mountPath, err := normalizeMountPath(parsed.Path)
	if err != nil {
		return "", invalidPublicURL(publicURL, err.Error())
	}

	origin := parsed.Scheme + "://" + parsed.Host
	return origin + mountPath, nil
}

func normalizeMountPath(path string) (string, error) {
	if path == "" || path == "/" {
		return "", nil
	}
	if strings.Contains(path, "//") {
		return "", errors.New("mount path must not contain duplicate slashes")
	}
	path = strings.TrimSuffix(path, "/")
	for _, segment := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if segment == "." || segment == ".." {
			return "", errors.New("mount path must not contain dot segments")
		}
		for _, c := range []byte(segment) {
			if !isMountCharacter(c) {
				return "", errors.New("mount path may contain only ASCII letters, digits, hyphens, periods, underscores, and tildes")
			}
		}
	}
	return path, nil
}

func validateAuthority(parsed *url.URL) error {
	hostname := parsed.Hostname()
	if net.ParseIP(hostname) == nil {
		name := strings.TrimSuffix(hostname, ".")
		if name == "" || len(name) > 253 {
			return fmt.Errorf("hostname is invalid")
		}
		for _, label := range strings.Split(name, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return fmt.Errorf("hostname is invalid")
			}
			for _, c := range []byte(label) {
				if !isAlphaNumeric(c) && c != '-' {
					return fmt.Errorf("hostname is invalid")
				}
			}
		}
	}

	port := parsed.Port()
	if hasPortSeparator(parsed.Host) && port == "" {
		return fmt.Errorf("port is invalid")
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("port is invalid")
		}
	}
	return nil
}

func hasPortSeparator(host string) bool {
	if strings.HasPrefix(host, "[") {
		end := strings.LastIndexByte(host, ']')
		return end >= 0 && len(host) > end+1
	}
	return strings.Contains(host, ":")
}

func isMountCharacter(c byte) bool {
	return isAlphaNumeric(c) || c == '-' || c == '.' || c == '_' || c == '~'
}

func isAlphaNumeric(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func invalidPublicURL(raw, reason string) error {
	return fmt.Errorf("invalid PUBLIC_URL %q: %s", raw, reason)
}
