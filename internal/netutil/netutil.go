// Package netutil contains small network-related safety helpers shared by
// configuration and outbound HTTP integrations.
package netutil

import (
	"net"
	"net/url"
	"strings"
)

// IsLoopbackHost reports whether hostname names the local machine. Hostnames
// that are not loopback must use TLS when they carry credentials or tokens.
func IsLoopbackHost(hostname string) bool {
	hostname = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(hostname)), ".")
	if hostname == "localhost" {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

// IsHTTPSOrLoopbackHTTP reports whether an HTTP URL is encrypted or limited
// to the local machine. It is suitable for endpoints that carry credentials.
func IsHTTPSOrLoopbackHTTP(endpoint *url.URL) bool {
	return endpoint.Scheme == "https" ||
		(endpoint.Scheme == "http" && IsLoopbackHost(endpoint.Hostname()))
}

// SafeEndpoint returns only the scheme and authority from an endpoint URL.
// Paths, queries, and fragments are intentionally omitted because webhook
// tokens are commonly carried in those components.
func SafeEndpoint(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "<configured endpoint>"
	}
	return parsed.Scheme + "://" + parsed.Host
}
