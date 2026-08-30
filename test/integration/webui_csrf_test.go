package integration_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"stick/internal/auth"
	domain "stick/internal/core"
	"stick/internal/publicurl"
	"stick/internal/web/render"
	"stick/internal/web/security"
)

func TestCSRFProtectionUsesLocalHTMLErrorsForBrowserRequests(t *testing.T) {
	secret := []byte("secret-32-bytes-minimum-length!!")
	sessionCookie := "session-cookie-value"
	renderer := newTestRenderer(t)
	var token string
	protected := security.CSRFProtection(secret, renderer.ErrorPage)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token = security.CSRFToken(r)
		w.WriteHeader(http.StatusNoContent)
	}))
	get := httptest.NewRequest(http.MethodGet, "/base/", nil)
	get.AddCookie(&http.Cookie{Name: security.SessionCookieName(), Value: sessionCookie})
	protected.ServeHTTP(httptest.NewRecorder(), get)
	if token == "" {
		t.Fatal("CSRF token is empty")
	}

	for _, test := range []struct {
		name   string
		body   string
		status int
		text   string
	}{
		{name: "malformed form", body: "csrf_token=" + token + "&name=%zz", status: http.StatusBadRequest, text: "invalid or too large"},
		{name: "invalid token", body: "csrf_token=wrong", status: http.StatusForbidden, text: "Not allowed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/base/sticks/new", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.AddCookie(&http.Cookie{Name: security.SessionCookieName(), Value: sessionCookie})
			request = request.WithContext(auth.WithIdentity(request.Context(), domain.Identity{Sub: "admin", IsAdmin: true}))
			recorder := httptest.NewRecorder()

			protected.ServeHTTP(recorder, request)

			if recorder.Code != test.status || recorder.Header().Get("Content-Type") != "text/html; charset=utf-8" {
				t.Fatalf("response = %d Content-Type %q", recorder.Code, recorder.Header().Get("Content-Type"))
			}
			body := recorder.Body.String()
			if !strings.Contains(body, test.text) || !strings.Contains(body, `href="/base/"`) || !strings.Contains(body, `href="/base/assets/styles.css"`) {
				t.Errorf("mounted error page missing expected content: %s", body)
			}
			if strings.Contains(body, "%zz") {
				t.Error("error page leaked malformed form details")
			}
		})
	}
}

func newTestRenderer(t *testing.T) render.Renderer {
	t.Helper()
	publicURL, err := publicurl.Parse("http://example.test/base")
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := newWebUIRenderer(time.UTC, publicURL, true)
	if err != nil {
		t.Fatal(err)
	}
	return renderer
}
