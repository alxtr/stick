package auth_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"stick/internal/auth"
)

func TestJWTValidatorValidatesExternalToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var providerURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_, _ = fmt.Fprintf(w, `{"issuer":%q,"jwks_uri":%q}`, providerURL, providerURL+"/keys")
		case "/keys":
			keyData := map[string]any{
				"keys": []map[string]any{{
					"kty": "RSA", "kid": "test-key", "use": "sig", "alg": "RS256",
					"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB",
				}},
			}
			_ = json.NewEncoder(w).Encode(keyData)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	providerURL = server.URL

	validator := auth.NewJWTValidator(auth.JWTConfig{Endpoint: server.URL, Audience: "stick-api", Scope: "stick:use"})
	token := signedJWT(t, key, map[string]any{
		"iss": server.URL, "aud": []string{"stick-api"}, "sub": "user-1",
		"name": "Alice", "email": "alice@example.com", "email_verified": true,
		"scope": "openid stick:use", "exp": time.Now().Add(time.Hour).Unix(),
	})
	identity, err := validator.Validate(t.Context(), token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if identity.Sub != "user-1" || identity.Name != "Alice" || identity.Email != "alice@example.com" || !identity.EmailVerified {
		t.Fatalf("identity = %+v", identity)
	}
}

func TestJWTValidatorRequiresScope(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var providerURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/.well-known/openid-configuration" {
			_, _ = fmt.Fprintf(w, `{"issuer":%q,"jwks_uri":%q}`, providerURL, providerURL+"/keys")
			return
		}
		keyData := map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "kid": "test-key", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB",
		}}}
		_ = json.NewEncoder(w).Encode(keyData)
	}))
	defer server.Close()
	providerURL = server.URL

	validator := auth.NewJWTValidator(auth.JWTConfig{Endpoint: server.URL, Audience: "stick-api", Scope: "stick:write"})
	token := signedJWT(t, key, map[string]any{
		"iss": server.URL, "aud": "stick-api", "sub": "user-1", "scope": "stick:read",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if _, err := validator.Validate(t.Context(), token); err == nil {
		t.Fatal("expected token with insufficient scope to be rejected")
	}
}

func signedJWT(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header := `{"alg":"RS256","kid":"test-key","typ":"JWT"}`
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.RawURLEncoding.EncodeToString([]byte(header)) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(encoded))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return encoded + "." + base64.RawURLEncoding.EncodeToString(signature)
}
