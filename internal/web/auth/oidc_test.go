package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"stick/internal/auth"
	"stick/internal/publicurl"
	"stick/internal/web/httpx"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
)

func TestHandler_LoginRedirects(t *testing.T) {
	h := NewOIDCHandler(auth.OIDCConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}, authPublicURL(t, "http://localhost:8080", ""), []byte("secret-32-bytes-minimum-length!!"))
	h.provider = &oidc.Provider{}
	h.oauth2 = oauth2.Config{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURL:  "http://localhost:8080/auth/callback",
		Endpoint:     oauth2.Endpoint{AuthURL: "https://accounts.example.com/authorize"},
	}
	h.randomReader = bytes.NewReader(make([]byte, 48))

	req := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	rr := httptest.NewRecorder()
	h.Login(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d: %s", rr.Code, http.StatusFound, rr.Body.String())
	}
	location, err := url.Parse(rr.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	if got := location.Scheme + "://" + location.Host + location.Path; got != "https://accounts.example.com/authorize" {
		t.Errorf("redirect endpoint = %q", got)
	}
	if got := location.Query().Get("redirect_uri"); got != "http://localhost:8080/auth/callback" {
		t.Errorf("redirect_uri = %q", got)
	}
	if got := location.Query().Get("code_challenge_method"); got != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", got)
	}
	if got := location.Query().Get("code_challenge"); got == "" {
		t.Error("code_challenge is empty")
	}
}

func TestIdentityFromOIDCClaims_RequiresSubject(t *testing.T) {
	_, err := identityFromOIDCClaims(oidcClaims{
		Name:          "Alice",
		Email:         "alice@example.com",
		EmailVerified: true,
	})
	if err == nil {
		t.Fatal("expected missing OIDC subject to be rejected")
	}
}

func TestIdentityFromOIDCClaims_PreservesVerifiedEmail(t *testing.T) {
	identity, err := identityFromOIDCClaims(oidcClaims{
		Sub:           "user123",
		Email:         "alice@example.com",
		EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("identityFromOIDCClaims: %v", err)
	}
	if !identity.EmailVerified {
		t.Fatal("expected email_verified to be preserved")
	}
}

func TestProviderDiscoveryRetriesAfterFailure(t *testing.T) {
	h := NewOIDCHandler(auth.OIDCConfig{Issuer: "https://accounts.example.com"}, authPublicURL(t, "http://example.test", ""), nil)
	h.initialBackoff = time.Nanosecond
	var calls int
	h.discover = func(context.Context, string) (*oidc.Provider, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("temporary discovery failure")
		}
		return &oidc.Provider{}, nil
	}

	if _, _, err := h.providerConfig(context.Background()); err == nil {
		t.Fatal("first discovery unexpectedly succeeded")
	}
	time.Sleep(time.Microsecond)
	provider, _, err := h.providerConfig(context.Background())
	if err != nil {
		t.Fatalf("second discovery failed: %v", err)
	}
	if provider == nil {
		t.Fatal("second discovery returned a nil provider")
	}
	if _, _, err := h.providerConfig(context.Background()); err != nil {
		t.Fatalf("cached provider lookup failed: %v", err)
	}
	if calls != 2 {
		t.Fatalf("discovery called %d times, want 2", calls)
	}
}

func TestProviderDiscoverySingleFlightDoesNotHoldMutexDuringNetworkIO(t *testing.T) {
	h := NewOIDCHandler(auth.OIDCConfig{Issuer: "https://accounts.example.com"}, authPublicURL(t, "http://example.test", ""), nil)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	h.discover = func(context.Context, string) (*oidc.Provider, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return &oidc.Provider{}, nil
	}

	const callers = 20
	var wg sync.WaitGroup
	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			if _, _, err := h.providerConfig(context.Background()); err != nil {
				t.Errorf("providerConfig: %v", err)
			}
		}()
	}
	<-started
	// The state mutex remains independently acquirable while discovery blocks.
	locked := make(chan bool, 1)
	go func() {
		h.providerMu.Lock()
		providerWasNil := h.provider == nil
		h.providerMu.Unlock()
		locked <- providerWasNil
	}()
	select {
	case providerWasNil := <-locked:
		if !providerWasNil {
			t.Fatal("provider was populated before discovery completed")
		}
	case <-time.After(time.Second):
		t.Fatal("provider mutex was held during discovery network I/O")
	}
	close(release)
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("discovery calls = %d, want 1", got)
	}
}

func TestProviderDiscoveryFailureBackoffSuppressesRepeatedNetworkCalls(t *testing.T) {
	h := NewOIDCHandler(auth.OIDCConfig{Issuer: "https://accounts.example.com"}, authPublicURL(t, "http://example.test", ""), nil)
	h.initialBackoff = time.Hour
	var calls int
	h.discover = func(context.Context, string) (*oidc.Provider, error) {
		calls++
		return nil, errors.New("provider unavailable")
	}
	for range 5 {
		if _, _, err := h.providerConfig(context.Background()); err == nil {
			t.Fatal("discovery unexpectedly succeeded")
		}
	}
	if calls != 1 {
		t.Fatalf("discovery calls during backoff = %d, want 1", calls)
	}
}

func TestProviderDiscoveryUsesBoundedContext(t *testing.T) {
	h := NewOIDCHandler(auth.OIDCConfig{Issuer: "https://accounts.example.com"}, authPublicURL(t, "http://example.test", ""), nil)
	h.discoveryTimeout = 10 * time.Millisecond
	h.discover = func(ctx context.Context, _ string) (*oidc.Provider, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Error("discovery context has no deadline")
		} else if remaining := time.Until(deadline); remaining > h.discoveryTimeout {
			t.Errorf("discovery deadline is too far away: %v", remaining)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
			return nil, errors.New("discovery context was not canceled")
		}
	}

	started := time.Now()
	_, _, err := h.providerConfig(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("discovery error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("discovery took too long: %v", elapsed)
	}
}

func TestLogin_UsesBasePathForRedirectURLAndCookies(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		basePath    string
		redirectURL string
		wantPath    string
		wantSecure  bool
	}{
		{name: "root", baseURL: "http://localhost:8080", redirectURL: "http://localhost:8080/auth/callback", wantPath: "/"},
		{name: "mounted", baseURL: "http://localhost:8080", basePath: "/basepath", redirectURL: "http://localhost:8080/basepath/auth/callback", wantPath: "/basepath"},
		{name: "secure nested mount", baseURL: "https://example.com", basePath: "/ops/stick", redirectURL: "https://example.com/ops/stick/auth/callback", wantPath: "/ops/stick", wantSecure: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewOIDCHandler(auth.OIDCConfig{}, authPublicURL(t, tt.baseURL, tt.basePath), nil)
			h.provider = &oidc.Provider{}
			h.oauth2 = oauth2.Config{
				RedirectURL: tt.redirectURL,
				Endpoint:    oauth2.Endpoint{AuthURL: "https://issuer.example/auth"},
			}
			h.randomReader = bytes.NewReader(make([]byte, 48))

			rr := httptest.NewRecorder()
			h.Login(rr, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
			if rr.Code != http.StatusFound {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
			}
			location, err := url.Parse(rr.Header().Get("Location"))
			if err != nil {
				t.Fatalf("parse redirect location: %v", err)
			}
			if got := location.Query().Get("redirect_uri"); got != tt.redirectURL {
				t.Errorf("redirect_uri = %q, want %q", got, tt.redirectURL)
			}
			cookies := rr.Result().Cookies()
			wantCookies := 2
			if tt.wantPath != "/" {
				wantCookies = 5 // legacy root session/OAuth deletions plus two current cookies
			}
			if len(cookies) != wantCookies {
				t.Fatalf("got %d cookies, want %d", len(cookies), wantCookies)
			}
			currentCookies := 0
			legacySessionCleared := false
			for _, cookie := range cookies {
				if cookie.Path == tt.wantPath && cookie.MaxAge > 0 {
					currentCookies++
					if cookie.Secure != tt.wantSecure {
						t.Errorf("cookie %s Secure = %v, want %v", cookie.Name, cookie.Secure, tt.wantSecure)
					}
					continue
				}
				if tt.wantPath != "/" && cookie.Path == "/" && cookie.MaxAge != -1 {
					t.Errorf("legacy cookie %s MaxAge = %d, want -1", cookie.Name, cookie.MaxAge)
				}
				if tt.wantPath != "/" && cookie.Name == SessionCookieName() && cookie.Path == "/" && cookie.MaxAge == -1 {
					legacySessionCleared = true
				}
				if cookie.Path != tt.wantPath && cookie.Path != "/" {
					t.Errorf("cookie %s Path = %q, want %q or legacy /", cookie.Name, cookie.Path, tt.wantPath)
				}
			}
			if currentCookies != 2 {
				t.Errorf("got %d current cookies, want 2", currentCookies)
			}
			if tt.wantPath != "/" && !legacySessionCleared {
				t.Error("login did not clear the legacy root-scoped session")
			}
		})
	}
}

func TestLogout_UsesBasePathForRedirectAndClearingCookie(t *testing.T) {
	tests := []struct {
		name     string
		basePath string
		wantPath string
		wantURL  string
	}{
		{name: "root", wantPath: "/", wantURL: "/auth/login"},
		{name: "mounted", basePath: "/basepath", wantPath: "/basepath", wantURL: "/basepath/auth/login"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewOIDCHandler(auth.OIDCConfig{}, authPublicURL(t, "http://example.test", tt.basePath), nil)
			rr := httptest.NewRecorder()
			h.Logout(rr, httptest.NewRequest(http.MethodPost, "/auth/logout", nil))

			if rr.Code != http.StatusFound {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
			}
			if got := rr.Header().Get("Location"); got != tt.wantURL {
				t.Errorf("Location = %q, want %q", got, tt.wantURL)
			}
			cookies := rr.Result().Cookies()
			wantCookies := 3
			if tt.wantPath != "/" {
				wantCookies = 6
			}
			if len(cookies) != wantCookies {
				t.Fatalf("got %d cookies, want %d", len(cookies), wantCookies)
			}
			cleared := make(map[string]bool)
			for _, cookie := range cookies {
				if cookie.MaxAge != -1 || cookie.Path != tt.wantPath && cookie.Path != "/" {
					t.Errorf("clearing cookie = %+v", cookie)
				}
				cleared[cookie.Name+"|"+cookie.Path] = true
			}
			for _, name := range []string{SessionCookieName(), pkceStateCookie, pkceVerifierCookie} {
				if !cleared[name+"|"+tt.wantPath] {
					t.Errorf("cookie %s was not cleared at %s", name, tt.wantPath)
				}
				if tt.wantPath != "/" && !cleared[name+"|/"] {
					t.Errorf("legacy root cookie %s was not cleared", name)
				}
			}
		})
	}
}

func TestCallback_UsesBasePathForSuccessRedirectAndCookies(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	var idToken string
	var providerServer *httptest.Server
	providerServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer":                 providerServer.URL,
				"authorization_endpoint": providerServer.URL + "/authorize",
				"token_endpoint":         providerServer.URL + "/token",
				"jwks_uri":               providerServer.URL + "/keys",
			})
		case "/token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "access-token",
				"token_type":   "Bearer",
				"id_token":     idToken,
				"expires_in":   3600,
			})
		case "/keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": "test-key",
				"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}),
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(providerServer.Close)

	signed := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":            providerServer.URL,
		"sub":            "user-1",
		"aud":            "client-id",
		"exp":            time.Now().Add(time.Hour).Unix(),
		"iat":            time.Now().Unix(),
		"name":           "Alice",
		"email":          "alice@example.com",
		"email_verified": true,
	}, nil)
	signed.Header["kid"] = "test-key"
	idToken, err = signed.SignedString(key)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}

	h := NewOIDCHandler(auth.OIDCConfig{
		Issuer:       providerServer.URL,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}, authPublicURL(t, "http://localhost:8080", "/basepath"), []byte("secret-32-bytes-minimum-length!!"))
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=code&state=state", nil)
	req.AddCookie(&http.Cookie{Name: pkceStateCookie, Value: "state"})
	req.AddCookie(&http.Cookie{Name: pkceVerifierCookie, Value: "verifier"})
	rr := httptest.NewRecorder()
	h.Callback(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d: %s", rr.Code, http.StatusFound, rr.Body.String())
	}
	if got := rr.Header().Get("Location"); got != "/basepath/" {
		t.Errorf("Location = %q, want /basepath/", got)
	}
	clearedLegacySession := false
	for _, cookie := range rr.Result().Cookies() {
		if cookie.Path == "/" && cookie.MaxAge == -1 {
			if cookie.Name == SessionCookieName() {
				clearedLegacySession = true
			}
			continue // legacy root OAuth cookie cleanup
		}
		if cookie.Path != "/basepath" {
			t.Errorf("cookie %s Path = %q, want /basepath or legacy /", cookie.Name, cookie.Path)
		}
	}
	if !clearedLegacySession {
		t.Error("callback did not clear the legacy root-scoped session")
	}
}

func TestCallbackRejectsInvalidStateAndMissingVerifier(t *testing.T) {
	tests := []struct {
		name        string
		queryState  string
		stateCookie string
		verifier    string
		wantBody    string
	}{
		{
			name:        "mismatched state",
			queryState:  "different",
			stateCookie: "expected",
			wantBody:    "invalid state\n",
		},
		{
			name:        "missing verifier",
			queryState:  "expected",
			stateCookie: "expected",
			wantBody:    "missing PKCE verifier\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewOIDCHandler(auth.OIDCConfig{}, authPublicURL(t, "http://example.test", ""), nil)
			h.provider = &oidc.Provider{}
			h.oauth2 = oauth2.Config{}
			request := httptest.NewRequest(http.MethodGet, "/auth/callback?code=code&state="+tt.queryState, nil)
			request.AddCookie(&http.Cookie{Name: pkceStateCookie, Value: tt.stateCookie})
			if tt.verifier != "" {
				request.AddCookie(&http.Cookie{Name: pkceVerifierCookie, Value: tt.verifier})
			}
			recorder := httptest.NewRecorder()

			h.Callback(recorder, request)

			if recorder.Code != http.StatusBadRequest || recorder.Body.String() != tt.wantBody {
				t.Fatalf("response = %d %q, want 400 %q", recorder.Code, recorder.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestCallbackRejectsTokenExchangeFailureAndMissingIDToken(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		wantBody   string
	}{
		{
			name:       "token exchange failure",
			status:     http.StatusBadGateway,
			body:       "upstream failure",
			wantStatus: http.StatusInternalServerError,
			wantBody:   "authentication failed\n",
		},
		{
			name:       "missing id token",
			status:     http.StatusOK,
			body:       `{"access_token":"access-token","token_type":"Bearer"}`,
			wantStatus: http.StatusInternalServerError,
			wantBody:   "authentication failed\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, tokenServer := callbackHandlerWithTokenEndpoint(t, tt.status, tt.body)
			defer tokenServer.Close()
			request := callbackRequest("expected", "verifier")
			recorder := httptest.NewRecorder()

			h.Callback(recorder, request)

			if recorder.Code != tt.wantStatus || recorder.Body.String() != tt.wantBody {
				t.Fatalf("response = %d %q, want %d %q", recorder.Code, recorder.Body.String(), tt.wantStatus, tt.wantBody)
			}
		})
	}
}

func TestCallbackRejectsOIDCClaimsWithoutSubject(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	var idToken string
	var providerServer *httptest.Server
	providerServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer":                 providerServer.URL,
				"authorization_endpoint": providerServer.URL + "/authorize",
				"token_endpoint":         providerServer.URL + "/token",
				"jwks_uri":               providerServer.URL + "/keys",
			})
		case "/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-token", "token_type": "Bearer", "id_token": idToken})
		case "/keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
				"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "test-key",
				"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}),
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer providerServer.Close()

	signed := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":  providerServer.URL,
		"aud":  "client-id",
		"exp":  time.Now().Add(time.Hour).Unix(),
		"iat":  time.Now().Unix(),
		"name": "Alice", "email": "alice@example.com", "email_verified": true,
	})
	signed.Header["kid"] = "test-key"
	idToken, err = signed.SignedString(key)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}

	h := NewOIDCHandler(auth.OIDCConfig{
		Issuer:       providerServer.URL,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}, authPublicURL(t, "http://example.test", ""), []byte("secret-32-bytes-minimum-length!!"))
	recorder := httptest.NewRecorder()
	h.Callback(recorder, callbackRequest("expected", "verifier"))

	if recorder.Code != http.StatusInternalServerError || recorder.Body.String() != "authentication failed\n" {
		t.Fatalf("response = %d %q, want 500 authentication failed", recorder.Code, recorder.Body.String())
	}
}

func callbackHandlerWithTokenEndpoint(t *testing.T, status int, body string) (*OIDCHandler, *httptest.Server) {
	t.Helper()
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	h := NewOIDCHandler(auth.OIDCConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}, authPublicURL(t, "http://example.test", ""), []byte("secret-32-bytes-minimum-length!!"))
	h.provider = &oidc.Provider{}
	h.oauth2 = oauth2.Config{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		Endpoint:     oauth2.Endpoint{TokenURL: tokenServer.URL},
	}
	return h, tokenServer
}

func callbackRequest(state, verifier string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/auth/callback?code=code&state="+state, nil)
	request.AddCookie(&http.Cookie{Name: pkceStateCookie, Value: state})
	request.AddCookie(&http.Cookie{Name: pkceVerifierCookie, Value: verifier})
	return request
}

type randomErrorReader struct {
	err error
}

func (r randomErrorReader) Read([]byte) (int, error) {
	return 0, r.err
}

type partialRandomErrorReader struct {
	err error
}

func (r partialRandomErrorReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, r.err
	}
	p[0] = 1
	return 1, r.err
}

func TestRandomBase64ReturnsErrorWithoutPartialValue(t *testing.T) {
	wantErr := errors.New("random source failed")

	got, err := randomBase64(partialRandomErrorReader{err: wantErr}, 32)
	if !errors.Is(err, wantErr) {
		t.Fatalf("randomBase64 error = %v, want %v", err, wantErr)
	}
	if got != "" {
		t.Fatalf("randomBase64 value = %q, want empty value on error", got)
	}
}

func TestLoginAbortsWhenRandomGenerationFails(t *testing.T) {
	wantErr := errors.New("random source failed")
	tests := []struct {
		name         string
		reader       io.Reader
		wantResponse string
	}{
		{
			name:         "PKCE verifier",
			reader:       partialRandomErrorReader{err: wantErr},
			wantResponse: "failed to generate PKCE verifier",
		},
		{
			name: "OAuth state",
			reader: io.MultiReader(
				bytes.NewReader(make([]byte, 32)),
				randomErrorReader{err: wantErr},
			),
			wantResponse: "failed to generate OAuth state",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewOIDCHandler(auth.OIDCConfig{}, authPublicURL(t, "http://example.test", ""), nil)
			h.provider = &oidc.Provider{}
			h.oauth2 = oauth2.Config{Endpoint: oauth2.Endpoint{AuthURL: "https://issuer.example/auth"}}
			h.randomReader = tt.reader

			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
			h.Login(rr, req)

			if rr.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
			}
			if !strings.Contains(rr.Body.String(), tt.wantResponse) {
				t.Fatalf("response body = %q, want %q", rr.Body.String(), tt.wantResponse)
			}
			if location := rr.Header().Get("Location"); location != "" {
				t.Fatalf("unexpected redirect location %q", location)
			}
			if cookies := rr.Result().Cookies(); len(cookies) != 0 {
				t.Fatalf("got %d cookies after random generation failure, want none", len(cookies))
			}
		})
	}
}

func TestLoginFailureLogIncludesContextRequestID(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	h := NewOIDCHandler(auth.OIDCConfig{}, authPublicURL(t, "http://example.test", ""), nil)
	h.provider = &oidc.Provider{}
	h.oauth2 = oauth2.Config{Endpoint: oauth2.Endpoint{AuthURL: "https://issuer.example/auth"}}
	h.randomReader = randomErrorReader{err: errors.New("random source failed")}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	req = req.WithContext(httpx.WithRequestID(req.Context(), "request-42"))
	h.Login(rr, req)

	got := logs.String()
	for _, want := range []string{`"msg":"authentication request failed"`, `"request_id":"request-42"`} {
		if !strings.Contains(got, want) {
			t.Errorf("authentication failure log = %q, want %s", got, want)
		}
	}
}

func authPublicURL(t *testing.T, baseURL, basePath string) publicurl.URL {
	t.Helper()
	publicURL, err := publicurl.Parse(baseURL + basePath)
	if err != nil {
		t.Fatal(err)
	}
	return publicURL
}
