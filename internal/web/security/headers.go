package security

import "net/http"

// Headers adds conservative browser security headers to every
// response. UI styles and fonts are served only from this application, and
// scripts remain disabled.
func Headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; script-src 'none'; img-src 'self' data:; style-src 'self'; font-src 'self'; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}
