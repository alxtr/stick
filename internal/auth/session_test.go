package auth_test

import (
	"testing"
	"time"

	"stick/internal/auth"
	domain "stick/internal/core"

	"github.com/golang-jwt/jwt/v5"
)

func TestSessionRoundTrip(t *testing.T) {
	secret := []byte("test-secret-key-32-bytes-minimum!")
	identity := domain.Identity{Sub: "user123", Name: "Alice", Email: "alice@example.com", EmailVerified: true}

	token, err := auth.Issue(identity, secret)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	got, err := auth.Verify(token, secret)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Sub != identity.Sub || got.Email != identity.Email || !got.EmailVerified {
		t.Errorf("got %+v, want %+v", got, identity)
	}
}

func TestSessionIssuesStableIssuerAudienceAndTTL(t *testing.T) {
	secret := []byte("test-secret-key-32-bytes-minimum!")
	before := time.Now()
	tokenString, err := auth.Issue(domain.Identity{Sub: "user123"}, secret)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := jwt.Parse(tokenString, func(*jwt.Token) (any, error) { return secret, nil })
	if err != nil {
		t.Fatal(err)
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatalf("claims type = %T", parsed.Claims)
	}
	if claims["iss"] != "stick" {
		t.Fatalf("issuer = %v", claims["iss"])
	}
	audience, ok := claims["aud"].([]any)
	if !ok || len(audience) != 1 || audience[0] != "stick-session" {
		t.Fatalf("audience = %#v", claims["aud"])
	}
	issuedAt := time.Unix(int64(claims["iat"].(float64)), 0)
	expiresAt := time.Unix(int64(claims["exp"].(float64)), 0)
	if ttl := expiresAt.Sub(issuedAt); ttl != auth.SessionTTL {
		t.Fatalf("session TTL = %v, want %v", ttl, auth.SessionTTL)
	}
	if issuedAt.Before(before.Add(-time.Second)) || issuedAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("issued-at time = %v", issuedAt)
	}
}

func TestSessionRejectsMissingOrIncorrectIssuerAndAudience(t *testing.T) {
	secret := []byte("test-secret-key-32-bytes-minimum!")
	tests := []struct {
		name     string
		issuer   any
		audience any
	}{
		{name: "missing issuer", audience: []string{"stick-session"}},
		{name: "wrong issuer", issuer: "other", audience: []string{"stick-session"}},
		{name: "missing audience", issuer: "stick"},
		{name: "wrong audience", issuer: "stick", audience: []string{"other"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := jwt.MapClaims{
				"sub": "user123", "exp": time.Now().Add(time.Hour).Unix(),
			}
			if tt.issuer != nil {
				claims["iss"] = tt.issuer
			}
			if tt.audience != nil {
				claims["aud"] = tt.audience
			}
			tokenString, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := auth.Verify(tokenString, secret); err == nil {
				t.Fatal("expected token to be rejected")
			}
		})
	}
}

func TestSessionRejectsMissingExpiration(t *testing.T) {
	secret := []byte("test-secret-key-32-bytes-minimum!")
	tokenString, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": "stick", "aud": []string{"stick-session"}, "sub": "user123",
	}).SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Verify(tokenString, secret); err == nil {
		t.Fatal("expected token without expiration to be rejected")
	}
}

func TestSessionRejectsEmptySubject(t *testing.T) {
	secret := []byte("test-secret-key-32-bytes-minimum!")
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": "stick",
		"aud": []string{"stick-session"},
		"sub": "",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tokenString, err := token.SignedString(secret)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}

	if _, err := auth.Verify(tokenString, secret); err == nil {
		t.Fatal("expected empty subject to be rejected")
	}
}

func TestSessionIssueRejectsEmptySubject(t *testing.T) {
	_, err := auth.Issue(domain.Identity{Email: "alice@example.com"}, []byte("test-secret-key-32-bytes-minimum!"))
	if err == nil {
		t.Fatal("expected empty subject to be rejected")
	}
}

func TestSessionRejectsInvalidSecret(t *testing.T) {
	secret := []byte("correct-secret-key-32-bytes-min!!")
	identity := domain.Identity{Sub: "u1", Name: "Alice", Email: "a@example.com"}

	token, _ := auth.Issue(identity, secret)
	_, err := auth.Verify(token, []byte("wrong-secret-key-32-bytes-minimun"))
	if err == nil {
		t.Error("expected error for wrong secret")
	}
}

func TestSessionRejectsOtherHMACAlgorithms(t *testing.T) {
	secret := []byte("test-secret-key-32-bytes-minimum!")
	for _, method := range []jwt.SigningMethod{
		jwt.SigningMethodHS384,
		jwt.SigningMethodHS512,
	} {
		t.Run(method.Alg(), func(t *testing.T) {
			token := jwt.NewWithClaims(method, jwt.MapClaims{
				"sub": "user123",
				"exp": time.Now().Add(time.Hour).Unix(),
			})
			tokenString, err := token.SignedString(secret)
			if err != nil {
				t.Fatalf("SignedString: %v", err)
			}

			if _, err := auth.Verify(tokenString, secret); err == nil {
				t.Fatalf("expected %s token to be rejected", method.Alg())
			}
		})
	}
}
