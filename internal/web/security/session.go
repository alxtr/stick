// Package security contains the browser authentication and request integrity
// primitives shared by the Web UI route packages.
package security

const sessionCookieName = "stick_session"

// SessionCookieName returns the name of the browser session cookie.
func SessionCookieName() string { return sessionCookieName }
