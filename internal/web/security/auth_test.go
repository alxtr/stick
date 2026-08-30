package security_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"stick/internal/auth"
	domain "stick/internal/core"
	"stick/internal/publicurl"
	"stick/internal/web/security"
)

func TestRequireAuth_NoCookie(t *testing.T) {
	secret := []byte("secret-32-bytes-minimum-length!!")
	mw := security.RequireAuth(secret, nil, testPublicURL(t, ""))
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("expected redirect (302), got %d", rr.Code)
	}
}

func TestRequireAuth_ValidToken(t *testing.T) {
	secret := []byte("secret-32-bytes-minimum-length!!")
	identity := domain.Identity{Sub: "u1", Name: "Alice", Email: "alice@example.com"}
	token, _ := auth.Issue(identity, secret)

	mw := security.RequireAuth(secret, nil, testPublicURL(t, ""))
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := auth.IdentityFromContext(r.Context())
		if got.Sub != identity.Sub {
			t.Errorf("got sub %q, want %q", got.Sub, identity.Sub)
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: security.SessionCookieName(), Value: token})
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func testPublicURL(t *testing.T, mountPath string) publicurl.URL {
	t.Helper()
	publicURL, err := publicurl.Parse("http://example.test" + mountPath)
	if err != nil {
		t.Fatal(err)
	}
	return publicURL
}

func TestRequireAuth_InvalidToken(t *testing.T) {
	secret := []byte("secret-32-bytes-minimum-length!!")
	mw := security.RequireAuth(secret, nil, testPublicURL(t, ""))
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: security.SessionCookieName(), Value: "not.a.valid.jwt"})
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("expected redirect (302) for invalid token, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/auth/login" {
		t.Errorf("expected redirect to /auth/login, got %q", loc)
	}
}

func TestRequireAuth_InternalPathRedirectsToPublicPrefix(t *testing.T) {
	secret := []byte("secret-32-bytes-minimum-length!!")
	mw := security.RequireAuth(secret, nil, testPublicURL(t, "/basepath"))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: security.SessionCookieName(), Value: "not.a.valid.jwt"})
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("expected redirect (302), got %d", rr.Code)
	}
	if got := rr.Header().Get("Location"); got != "/basepath/auth/login" {
		t.Errorf("Location = %q, want /basepath/auth/login", got)
	}
}

func TestRequireAuth_VerifiedAdminUser(t *testing.T) {
	secret := []byte("secret-32-bytes-minimum-length!!")
	identity := domain.Identity{Sub: "u1", Name: "Alice", Email: "alice@example.com", EmailVerified: true}
	token, _ := auth.Issue(identity, secret)

	mw := security.RequireAuth(secret, []string{"alice@example.com", "bob@example.com"}, testPublicURL(t, ""))
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := auth.IdentityFromContext(r.Context())
		if !got.IsAdmin {
			t.Error("expected IsAdmin=true for email in admin list")
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: security.SessionCookieName(), Value: token})
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestRequireAuth_UnverifiedAdminUser(t *testing.T) {
	secret := []byte("secret-32-bytes-minimum-length!!")
	identity := domain.Identity{Sub: "u1", Name: "Alice", Email: "alice@example.com", EmailVerified: false}
	token, _ := auth.Issue(identity, secret)

	mw := security.RequireAuth(secret, []string{"alice@example.com"}, testPublicURL(t, ""))
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := auth.IdentityFromContext(r.Context())
		if got.IsAdmin {
			t.Error("expected IsAdmin=false for unverified admin email")
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: security.SessionCookieName(), Value: token})
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestRequireAuth_NonAdminUser(t *testing.T) {
	secret := []byte("secret-32-bytes-minimum-length!!")
	identity := domain.Identity{Sub: "u2", Name: "Bob", Email: "bob@example.com"}
	token, _ := auth.Issue(identity, secret)

	mw := security.RequireAuth(secret, []string{"alice@example.com"}, testPublicURL(t, ""))
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := auth.IdentityFromContext(r.Context())
		if got.IsAdmin {
			t.Error("expected IsAdmin=false for email not in admin list")
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: security.SessionCookieName(), Value: token})
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}
