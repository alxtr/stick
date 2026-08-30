// Package publicurl validates and normalizes the application's public URL.
package publicurl

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"stick/internal/netutil"
)

// URL is an immutable, validated public application URL. It contains an
// origin and an optional application mount path. Its zero value is
// intentionally invalid; values must be constructed with Parse.
type URL struct {
	origin    string
	mountPath string
}

// Parse validates and normalizes the complete public application URL. A lone
// trailing slash is accepted. Mount segments are deliberately limited to a
// conservative set of ASCII URL characters so they are safe in URLs, ServeMux
// patterns, and cookie paths without context-specific escaping.
func Parse(publicURL string) (URL, error) {
	if publicURL == "" {
		return URL{}, invalidPublicURL(publicURL, "must not be empty")
	}
	for _, r := range publicURL {
		if r > 0x7f || r <= 0x20 || r == 0x7f {
			return URL{}, invalidPublicURL(publicURL, "must contain only visible ASCII characters")
		}
	}
	if strings.ContainsAny(publicURL, `\%`) {
		return URL{}, invalidPublicURL(publicURL, "backslashes and percent escapes are not supported")
	}

	parsed, err := url.Parse(publicURL)
	if err != nil {
		return URL{}, fmt.Errorf("invalid PUBLIC_URL %q: %w", publicURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return URL{}, invalidPublicURL(publicURL, "scheme must be http or https")
	}
	if !parsed.IsAbs() || parsed.Host == "" || parsed.Hostname() == "" || parsed.Opaque != "" {
		return URL{}, invalidPublicURL(publicURL, "must be an absolute URL with a hostname")
	}
	if parsed.User != nil {
		return URL{}, invalidPublicURL(publicURL, "user information is not supported")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(publicURL, "#") {
		return URL{}, invalidPublicURL(publicURL, "must not include a query or fragment")
	}
	if err := validateAuthority(parsed); err != nil {
		return URL{}, invalidPublicURL(publicURL, err.Error())
	}

	mountPath, err := normalizeMountPath(parsed.Path)
	if err != nil {
		return URL{}, invalidPublicURL(publicURL, err.Error())
	}

	origin := parsed.Scheme + "://" + parsed.Host
	return URL{origin: origin, mountPath: mountPath}, nil
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

// Validate reports whether u was constructed by Parse. Because URL's fields
// are private, a non-zero value is always valid outside this package.
func (u URL) Validate() error {
	if u.origin == "" {
		return errors.New("publicurl.URL is invalid: construct it with publicurl.Parse")
	}
	return nil
}

func (u URL) assertValid() {
	if err := u.Validate(); err != nil {
		panic(err.Error())
	}
}

func (u URL) String() string {
	u.assertValid()
	return u.origin + u.mountPath
}

// Origin returns the normalized scheme and authority without the application
// mount path.
func (u URL) Origin() string {
	u.assertValid()
	return u.origin
}

// MountPath returns the normalized mount path, or an empty string for a root
// deployment.
func (u URL) MountPath() string {
	u.assertValid()
	return u.mountPath
}

// IsHTTPS reports whether the public origin uses HTTPS.
func (u URL) IsHTTPS() bool {
	u.assertValid()
	return strings.HasPrefix(u.origin, "https://")
}

// IsLoopback reports whether the public URL points at the local machine.
func (u URL) IsLoopback() bool {
	u.assertValid()
	parsed, err := url.Parse(u.origin)
	return err == nil && netutil.IsLoopbackHost(parsed.Hostname())
}
