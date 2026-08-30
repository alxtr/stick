package security

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
)

import "stick/internal/web/httpx"

type csrfContextKey struct{}

// CSRFToken returns the token bound to the current browser session cookie. It
// is safe to call for unauthenticated requests; it returns an empty string
// when there is no session cookie or CSRF middleware context.
func CSRFToken(r *http.Request) string {
	cookie, err := r.Cookie(SessionCookieName())
	if err != nil || cookie.Value == "" {
		return ""
	}
	return csrfToken(cookie.Value, csrfSecretFromContext(r.Context()))
}

// CSRFProtection protects browser form POSTs using an HMAC token derived from
// the authenticated JWT cookie. This is a synchronizer-style token without a
// server-side session table: a token is valid only for the exact current
// session cookie and configured signing secret. errorPage renders browser-safe
// 400 and 403 responses; it may be nil in isolated middleware use.
func CSRFProtection(jwtSecret []byte, errorPage func(http.ResponseWriter, *http.Request, int)) httpx.Middleware {
	secret := append([]byte(nil), jwtSecret...)
	reject := func(w http.ResponseWriter, r *http.Request, status int) {
		r = withCSRFSecret(r, secret)
		if errorPage != nil {
			errorPage(w, r, status)
			return
		}
		http.Error(w, http.StatusText(status), status)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				next.ServeHTTP(w, withCSRFSecret(r, secret))
				return
			}

			cookie, err := r.Cookie(SessionCookieName())
			if err != nil || cookie.Value == "" {
				reject(w, r, http.StatusForbidden)
				return
			}

			token := r.Header.Get("X-CSRF-Token")
			if token == "" {
				// ParseForm caches the bounded form on the request, so downstream
				// handlers can access the same values without rereading the body.
				if err := ParseBoundedForm(w, r); err != nil {
					reject(w, r, http.StatusBadRequest)
					return
				}
				token = r.PostForm.Get("csrf_token")
			}

			if !validCSRFToken(token, cookie.Value, secret) {
				reject(w, r, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, withCSRFSecret(r, secret))
		})
	}
}

const maxFormBodyBytes int64 = 8 << 10

// ParseBoundedForm parses a browser form body with the UI's conservative size
// limit. Route packages use the same helper so CSRF validation and handlers
// enforce one request-body policy.
func ParseBoundedForm(response http.ResponseWriter, request *http.Request) error {
	request.Body = http.MaxBytesReader(response, request.Body, maxFormBodyBytes)
	return request.ParseForm()
}

func csrfSecretFromContext(ctx context.Context) []byte {
	secret, _ := ctx.Value(csrfContextKey{}).([]byte)
	return secret
}

func withCSRFSecret(r *http.Request, secret []byte) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), csrfContextKey{}, secret))
}

func csrfToken(sessionCookie string, secret []byte) string {
	if len(secret) == 0 || sessionCookie == "" {
		return ""
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("stick csrf\x00"))
	_, _ = mac.Write([]byte(sessionCookie))
	return hex.EncodeToString(mac.Sum(nil))
}

func validCSRFToken(token, sessionCookie string, secret []byte) bool {
	want := csrfToken(sessionCookie, secret)
	got, err := hex.DecodeString(token)
	if err != nil || len(got) != sha256.Size || want == "" {
		return false
	}
	wantBytes, _ := hex.DecodeString(want)
	return hmac.Equal(got, wantBytes)
}
