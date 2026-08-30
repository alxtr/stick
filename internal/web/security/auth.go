package security

import (
	"net/http"

	"stick/internal/auth"
	"stick/internal/publicurl"
	"stick/internal/web/httpx"
)

// RequireAuth returns middleware that validates the browser's JWT session
// cookie and enriches the identity with admin status from the provided email
// list. Unauthenticated or invalid requests are redirected to the mounted
// /auth/login path.
func RequireAuth(jwtSecret []byte, adminEmails []string, publicURL publicurl.URL) httpx.Middleware {
	admins := auth.AdminSet(adminEmails)
	loginPath := httpx.Path(publicURL, "/auth/login")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName())
			if err != nil || cookie.Value == "" {
				http.Redirect(w, r, loginPath, http.StatusFound)
				return
			}
			identity, err := auth.Verify(cookie.Value, jwtSecret)
			if err != nil {
				http.Redirect(w, r, loginPath, http.StatusFound)
				return
			}
			identity = auth.WithAdminStatus(identity, admins)
			ctx := auth.WithIdentity(r.Context(), identity)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
