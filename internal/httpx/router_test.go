package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"stick/internal/httpx"
)

func testMountPath(t *testing.T, mountPath string) string {
	t.Helper()
	return mountPath
}

func TestRouterWithScopesMiddleware(t *testing.T) {
	routes := httpx.NewRouter(testMountPath(t, ""))
	reject := func(http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusUnauthorized)
		})
	}
	protected := routes.With(httpx.Chain(reject))
	routes.Handle(http.MethodGet, "/public", http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	protected.Handle(http.MethodGet, "/private", http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))

	publicRecorder := httptest.NewRecorder()
	routes.ServeHTTP(publicRecorder, httptest.NewRequest(http.MethodGet, "/public", nil))
	if publicRecorder.Code != http.StatusNoContent {
		t.Errorf("public route status = %d, want 204", publicRecorder.Code)
	}

	privateRecorder := httptest.NewRecorder()
	routes.ServeHTTP(privateRecorder, httptest.NewRequest(http.MethodGet, "/private", nil))
	if privateRecorder.Code != http.StatusUnauthorized {
		t.Errorf("private route status = %d, want 401", privateRecorder.Code)
	}
}

func TestRouterPreservesNestedMountAndPathValues(t *testing.T) {
	routes := httpx.NewRouter(testMountPath(t, "/ops/stick"))
	routes.HandleFunc(http.MethodGet, "/sticks/{id}", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/ops/stick/sticks/aa001" {
			t.Errorf("handler path = %q", got)
		}
		_, _ = w.Write([]byte(r.PathValue("id")))
	})

	recorder := httptest.NewRecorder()
	routes.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/ops/stick/sticks/aa001", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "aa001" {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	for _, path := range []string{"/sticks/aa001", "/ops/stick-other/sticks/aa001"} {
		recorder := httptest.NewRecorder()
		routes.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("path %q status=%d, want 404", path, recorder.Code)
		}
	}
}

func TestRouterUsesCustomNotFoundOnlyInsideMount(t *testing.T) {
	routes := httpx.NewRouter(testMountPath(t, "/stick"))
	routes.SetNotFound(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("local not found"))
	}))

	inside := httptest.NewRecorder()
	routes.ServeHTTP(inside, httptest.NewRequest(http.MethodGet, "/stick/missing", nil))
	if inside.Code != http.StatusNotFound || inside.Body.String() != "local not found" || inside.Header().Get("Content-Type") != "text/html" {
		t.Fatalf("mounted response = %d %q %q", inside.Code, inside.Header().Get("Content-Type"), inside.Body.String())
	}

	outside := httptest.NewRecorder()
	routes.ServeHTTP(outside, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if outside.Code != http.StatusNotFound || outside.Body.String() == "local not found" {
		t.Fatalf("unmounted response = %d %q", outside.Code, outside.Body.String())
	}
}

func TestRouterCustomNotFoundAndRedirectSemantics(t *testing.T) {
	routes := httpx.NewRouter(testMountPath(t, "/stick"))
	routes.SetNotFound(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("local not found"))
	}))
	routes.HandleFunc(http.MethodGet, "/known", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	routes.HandleFunc(http.MethodPost, "/submit", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	routes.HandleFunc(http.MethodGet, "/tree/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	for _, test := range []struct {
		name      string
		method    string
		path      string
		status    int
		allow     string
		location  string
		custom404 bool
	}{
		{name: "POST to GET route", method: http.MethodPost, path: "/stick/known", status: http.StatusNotFound, custom404: true},
		{name: "GET to POST route", method: http.MethodGet, path: "/stick/submit", status: http.StatusNotFound, custom404: true},
		{name: "canonical subtree redirect", method: http.MethodGet, path: "/stick/tree", status: http.StatusTemporaryRedirect, location: "/stick/tree/"},
		{name: "unknown mounted route", method: http.MethodGet, path: "/stick/unknown", status: http.StatusNotFound, custom404: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			routes.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d; body=%q", recorder.Code, test.status, recorder.Body.String())
			}
			if got := recorder.Header().Get("Allow"); got != test.allow {
				t.Errorf("Allow = %q, want %q", got, test.allow)
			}
			if got := recorder.Header().Get("Location"); got != test.location {
				t.Errorf("Location = %q, want %q", got, test.location)
			}
			if got := recorder.Body.String() == "local not found"; got != test.custom404 {
				t.Errorf("custom 404 body present = %t, want %t; body=%q", got, test.custom404, recorder.Body.String())
			}
		})
	}
}

func TestRouterRejectsUntrustedReferences(t *testing.T) {
	routes := httpx.NewRouter("/stick")
	for _, reference := range []string{"", "api/v1/sticks", "//evil.example", `/stick\\one`, "/stick#fragment"} {
		t.Run(reference, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("Handle(%q) did not panic", reference)
				}
			}()
			routes.Handle(http.MethodGet, reference, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		})
	}
}
