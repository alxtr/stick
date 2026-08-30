package security_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"stick/internal/web/security"
)

func TestCSRFProtection(t *testing.T) {
	secret := []byte("secret-32-bytes-minimum-length!!")
	sessionCookie := "session-cookie-value"

	var token string
	getToken := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token = security.CSRFToken(r)
		w.WriteHeader(http.StatusOK)
	})
	protected := security.CSRFProtection(secret, nil)(getToken)
	get := httptest.NewRequest(http.MethodGet, "/", nil)
	get.AddCookie(&http.Cookie{Name: security.SessionCookieName(), Value: sessionCookie})
	protected.ServeHTTP(httptest.NewRecorder(), get)
	if token == "" {
		t.Fatal("expected CSRF token for session")
	}

	tests := []struct {
		name        string
		path        string
		body        string
		headerToken string
		status      int
		wantField   string
	}{
		{name: "valid form token remains available downstream", body: "csrf_token=" + token + "&name=Deploy+Key", status: http.StatusOK, wantField: "Deploy Key"},
		{name: "missing", body: "", status: http.StatusForbidden},
		{name: "query token rejected", path: "/sticks/aa001/claim?csrf_token=" + token, body: "name=Deploy+Key", status: http.StatusForbidden},
		{name: "invalid", body: "csrf_token=wrong", status: http.StatusForbidden},
		{name: "malformed form", body: "csrf_token=%zz", status: http.StatusBadRequest},
		{name: "oversized form", body: "csrf_token=" + token + "&padding=" + strings.Repeat("x", 9<<10), status: http.StatusBadRequest},
		{name: "header token does not pre-parse body", body: "name=%zz", headerToken: token, status: http.StatusOK, wantField: "name=%zz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var downstreamField string
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.headerToken != "" {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Errorf("downstream body read: %v", err)
					}
					downstreamField = string(body)
				} else {
					if err := r.ParseForm(); err != nil {
						t.Errorf("downstream ParseForm: %v", err)
					}
					downstreamField = r.Form.Get("name")
				}
				w.WriteHeader(http.StatusOK)
			})
			protected := security.CSRFProtection(secret, nil)(next)
			path := tt.path
			if path == "" {
				path = "/sticks/aa001/claim"
			}
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tt.headerToken != "" {
				req.Header.Set("X-CSRF-Token", tt.headerToken)
			}
			req.AddCookie(&http.Cookie{Name: security.SessionCookieName(), Value: sessionCookie})
			rr := httptest.NewRecorder()
			protected.ServeHTTP(rr, req)
			if rr.Code != tt.status {
				t.Errorf("status = %d, want %d", rr.Code, tt.status)
			}
			if downstreamField != tt.wantField {
				t.Errorf("downstream field = %q, want %q", downstreamField, tt.wantField)
			}
		})
	}
}
