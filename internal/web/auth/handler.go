// Package auth owns the browser's /auth OIDC routes.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	coreauth "stick/internal/auth"
	domain "stick/internal/core"
	"stick/internal/publicurl"
	"stick/internal/web/httpx"
	"stick/internal/web/security"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	pkceStateCookie             = "stick_state"
	pkceVerifierCookie          = "stick_pkce"
	defaultOIDCDiscoveryTimeout = 10 * time.Second
	defaultOIDCFailureBackoff   = time.Second
	maxOIDCFailureBackoff       = 30 * time.Second
)

// Handler serves the OIDC login, callback, and logout flow for the browser UI.
type Handler struct {
	providerOptions  coreauth.OIDCConfig
	publicURL        publicurl.URL
	sessionSecret    []byte
	providerMu       sync.Mutex
	provider         *oidc.Provider
	oauth2           oauth2.Config
	discoveryDone    chan struct{}
	lastDiscoveryErr error
	retryDiscoveryAt time.Time
	failureBackoff   time.Duration
	initialBackoff   time.Duration
	discover         func(context.Context, string) (*oidc.Provider, error)
	discoveryTimeout time.Duration
	randomReader     io.Reader
}

// OIDCHandler is the descriptive name retained by the original API.
type OIDCHandler = Handler

// SessionCookieName exposes the shared session cookie name to authentication
// package callers without coupling them to the root composition package.
func SessionCookieName() string { return security.SessionCookieName() }

// NewOIDCHandler returns an OIDC handler with deferred provider discovery.
func NewOIDCHandler(providerOptions coreauth.OIDCConfig, publicURL publicurl.URL, sessionSecret []byte) *Handler {
	// Resolve all derived values now so an invalid zero-value PublicURL fails at
	// composition rather than during the first authentication request.
	_ = httpx.Absolute(publicURL, "/auth/callback")
	h := &Handler{
		providerOptions:  providerOptions,
		publicURL:        publicURL,
		sessionSecret:    append([]byte(nil), sessionSecret...),
		discover:         oidc.NewProvider,
		discoveryTimeout: defaultOIDCDiscoveryTimeout,
		randomReader:     rand.Reader,
		initialBackoff:   defaultOIDCFailureBackoff,
	}
	// Provider discovery is deferred to first use to allow startup without network.
	return h
}

// Login begins the OIDC authorization flow.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	_, oauthConfig, err := h.providerConfig(r.Context())
	if err != nil {
		h.internalError(w, r, "discover OIDC provider", err, "OIDC provider unavailable", http.StatusServiceUnavailable)
		return
	}

	verifier, err := randomBase64(h.randomReader, 32)
	if err != nil {
		h.internalError(w, r, "generate PKCE verifier", err, "failed to generate PKCE verifier", http.StatusInternalServerError)
		return
	}
	state, err := randomBase64(h.randomReader, 16)
	if err != nil {
		h.internalError(w, r, "generate OAuth state", err, "failed to generate OAuth state", http.StatusInternalServerError)
		return
	}

	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	path := httpx.CookiePath(h.publicURL)
	// Remove cookies created by older root-mounted deployments. Without this,
	// a browser can send both the stale Path=/ cookie and the current mounted
	// cookie; net/http may then read the stale value and reject the callback.
	if path != "/" {
		clearCookies(w, "/", security.SessionCookieName(), pkceStateCookie, pkceVerifierCookie)
	}
	setCookie(w, pkceStateCookie, state, 10*time.Minute, h.publicURL.IsHTTPS(), path)
	setCookie(w, pkceVerifierCookie, verifier, 10*time.Minute, h.publicURL.IsHTTPS(), path)

	redirectURL := oauthConfig.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// Callback completes the OIDC authorization flow and creates a session.
func (h *Handler) Callback(w http.ResponseWriter, r *http.Request) {
	provider, oauthConfig, err := h.providerConfig(r.Context())
	if err != nil {
		h.internalError(w, r, "discover OIDC provider", err, "OIDC provider unavailable", http.StatusServiceUnavailable)
		return
	}

	stateCookie, err := r.Cookie(pkceStateCookie)
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	verifierCookie, err := r.Cookie(pkceVerifierCookie)
	if err != nil {
		http.Error(w, "missing PKCE verifier", http.StatusBadRequest)
		return
	}

	token, err := oauthConfig.Exchange(r.Context(), r.URL.Query().Get("code"),
		oauth2.SetAuthURLParam("code_verifier", verifierCookie.Value))
	if err != nil {
		h.internalError(w, r, "exchange OIDC token", err, "authentication failed", http.StatusInternalServerError)
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		h.internalError(w, r, "read OIDC ID token", errors.New("token response did not include id_token"), "authentication failed", http.StatusInternalServerError)
		return
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: h.providerOptions.ClientID})
	idToken, err := verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		h.internalError(w, r, "verify OIDC ID token", err, "authentication failed", http.StatusInternalServerError)
		return
	}
	var claims oidcClaims
	if err := idToken.Claims(&claims); err != nil {
		h.internalError(w, r, "parse OIDC claims", err, "authentication failed", http.StatusInternalServerError)
		return
	}

	identity, err := identityFromOIDCClaims(claims)
	if err != nil {
		h.internalError(w, r, "validate OIDC claims", err, "authentication failed", http.StatusInternalServerError)
		return
	}
	jwtString, err := coreauth.Issue(identity, h.sessionSecret)
	if err != nil {
		h.internalError(w, r, "issue session token", err, "authentication failed", http.StatusInternalServerError)
		return
	}

	path := httpx.CookiePath(h.publicURL)
	setCookie(w, security.SessionCookieName(), jwtString, coreauth.SessionTTL, h.publicURL.IsHTTPS(), path)
	clearCookies(w, path, pkceStateCookie, pkceVerifierCookie)
	if path != "/" {
		clearCookies(w, "/", security.SessionCookieName(), pkceStateCookie, pkceVerifierCookie)
	}
	http.Redirect(w, r, httpx.Path(h.publicURL, "/"), http.StatusFound)
}

// Logout clears the authentication cookies and redirects to login.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	path := httpx.CookiePath(h.publicURL)
	clearCookies(w, path, security.SessionCookieName(), pkceStateCookie, pkceVerifierCookie)
	if path != "/" {
		clearCookies(w, "/", security.SessionCookieName(), pkceStateCookie, pkceVerifierCookie)
	}
	http.Redirect(w, r, httpx.Path(h.publicURL, "/auth/login"), http.StatusFound)
}

type oidcClaims struct {
	Sub           string `json:"sub"`
	Name          string `json:"name"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
}

func identityFromOIDCClaims(cl oidcClaims) (domain.Identity, error) {
	if strings.TrimSpace(cl.Sub) == "" {
		return domain.Identity{}, errors.New("oidc subject is required")
	}
	return domain.Identity{
		Sub:           cl.Sub,
		Name:          cl.Name,
		Email:         cl.Email,
		EmailVerified: cl.EmailVerified,
	}, nil
}

func (h *Handler) internalError(w http.ResponseWriter, r *http.Request, operation string, err error, message string, status int) {
	httpx.LogError(r.Context(), "authentication request failed", operation, err)
	http.Error(w, message, status)
}

func randomBase64(reader io.Reader, n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func setCookie(w http.ResponseWriter, name, value string, ttl time.Duration, secure bool, path string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearCookies(w http.ResponseWriter, path string, names ...string) {
	for _, name := range names {
		http.SetCookie(w, &http.Cookie{Name: name, Path: path, MaxAge: -1})
	}
}

func (h *Handler) providerConfig(ctx context.Context) (*oidc.Provider, oauth2.Config, error) {
	for {
		h.providerMu.Lock()
		if h.provider != nil {
			provider, oauthCfg := h.provider, h.oauth2
			h.providerMu.Unlock()
			return provider, oauthCfg, nil
		}
		if h.lastDiscoveryErr != nil && time.Now().Before(h.retryDiscoveryAt) {
			err := h.lastDiscoveryErr
			h.providerMu.Unlock()
			return nil, oauth2.Config{}, err
		}
		if done := h.discoveryDone; done != nil {
			h.providerMu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, oauth2.Config{}, ctx.Err()
			}
		}
		done := make(chan struct{})
		h.discoveryDone = done
		h.providerMu.Unlock()

		provider, oauthCfg, err := h.discoverProvider(ctx)

		h.providerMu.Lock()
		if err == nil {
			h.provider = provider
			h.oauth2 = oauthCfg
			h.lastDiscoveryErr = nil
			h.retryDiscoveryAt = time.Time{}
			h.failureBackoff = 0
		} else {
			h.lastDiscoveryErr = err
			h.failureBackoff = nextDiscoveryBackoff(h.failureBackoff, h.initialBackoff)
			h.retryDiscoveryAt = time.Now().Add(h.failureBackoff)
		}
		h.discoveryDone = nil
		close(done)
		h.providerMu.Unlock()
		return provider, oauthCfg, err
	}
}

func (h *Handler) discoverProvider(parent context.Context) (*oidc.Provider, oauth2.Config, error) {
	// Discovery is shared by concurrent callers, so the first caller canceling
	// must not cancel the single in-flight operation for every waiter.
	discoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), h.discoveryTimeout)
	defer cancel()
	provider, err := h.discover(discoveryCtx, h.providerOptions.Issuer)
	if err != nil {
		return nil, oauth2.Config{}, err
	}
	oauthCfg := oauth2.Config{
		ClientID:     h.providerOptions.ClientID,
		ClientSecret: h.providerOptions.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  httpx.Absolute(h.publicURL, "/auth/callback"),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}
	return provider, oauthCfg, nil
}

func nextDiscoveryBackoff(current, initial time.Duration) time.Duration {
	if current <= 0 {
		if initial > maxOIDCFailureBackoff {
			return maxOIDCFailureBackoff
		}
		return initial
	}
	if current >= maxOIDCFailureBackoff/2 {
		return maxOIDCFailureBackoff
	}
	return current * 2
}
