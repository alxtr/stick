package auth

import (
	"fmt"
	"strings"
	"time"

	domain "stick/internal/core"

	"github.com/golang-jwt/jwt/v5"
)

// SessionTTL is the lifetime of a signed session token.
const SessionTTL = 24 * time.Hour

const sessionIssuer = "stick"
const sessionAudience = "stick-session"

type sessionClaims struct {
	jwt.RegisteredClaims
	Name          string `json:"name"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
}

// Issue creates a signed JWT for the given identity.
func Issue(identity domain.Identity, secret []byte) (string, error) {
	if strings.TrimSpace(identity.Sub) == "" {
		return "", fmt.Errorf("identity subject is required")
	}
	now := time.Now()
	claims := sessionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    sessionIssuer,
			Subject:   identity.Sub,
			Audience:  jwt.ClaimStrings{sessionAudience},
			ExpiresAt: jwt.NewNumericDate(now.Add(SessionTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
		Name:          identity.Name,
		Email:         identity.Email,
		EmailVerified: identity.EmailVerified,
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
}

// Verify parses and validates a signed JWT, returning the identity it carries.
func Verify(tokenString string, secret []byte) (domain.Identity, error) {
	var claims sessionClaims
	_, err := jwt.ParseWithClaims(tokenString, &claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return secret, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(sessionIssuer),
		jwt.WithAudience(sessionAudience),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return domain.Identity{}, err
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return domain.Identity{}, fmt.Errorf("token subject is required")
	}
	return domain.Identity{
		Sub:           claims.Subject,
		Name:          claims.Name,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
	}, nil
}
