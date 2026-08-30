// Package auth contains transport-neutral authentication helpers.
package auth

import (
	"context"
	"strings"

	domain "stick/internal/core"
)

type identityContextKey struct{}

// WithIdentity returns a context containing the authenticated identity.
func WithIdentity(ctx context.Context, identity domain.Identity) context.Context {
	return context.WithValue(ctx, identityContextKey{}, identity)
}

// IdentityFromContext returns the authenticated identity stored in the
// context, or its zero value when the request has not been authenticated.
func IdentityFromContext(ctx context.Context) domain.Identity {
	identity, _ := ctx.Value(identityContextKey{}).(domain.Identity)
	return identity
}

// AdminSet normalizes configured administrator email addresses for request
// authentication.
func AdminSet(emails []string) map[string]struct{} {
	admins := make(map[string]struct{}, len(emails))
	for _, email := range emails {
		if email = normalizeEmail(email); email != "" {
			admins[email] = struct{}{}
		}
	}
	return admins
}

// WithAdminStatus derives administrator status from a verified email address.
func WithAdminStatus(identity domain.Identity, admins map[string]struct{}) domain.Identity {
	_, identity.IsAdmin = admins[normalizeEmail(identity.Email)]
	identity.IsAdmin = identity.EmailVerified && identity.IsAdmin
	return identity
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
