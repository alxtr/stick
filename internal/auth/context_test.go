package auth_test

import (
	"context"
	"testing"

	"stick/internal/auth"
	domain "stick/internal/core"
)

func TestIdentityContext(t *testing.T) {
	identity := domain.Identity{Sub: "u1", Name: "Alice", Email: "alice@example.com", IsAdmin: true}
	ctx := auth.WithIdentity(context.Background(), identity)

	if got := auth.IdentityFromContext(ctx); got != identity {
		t.Errorf("identity = %+v, want %+v", got, identity)
	}
}

func TestAdminStatusMatchesVerifiedEmailsCaseInsensitively(t *testing.T) {
	admins := auth.AdminSet([]string{" Alice@Example.COM ", " "})

	identity := auth.WithAdminStatus(domain.Identity{
		Email:         "alice@example.com",
		EmailVerified: true,
	}, admins)
	if !identity.IsAdmin {
		t.Fatal("verified email did not match administrator with different casing")
	}

	unverified := auth.WithAdminStatus(domain.Identity{
		Email: "ALICE@EXAMPLE.COM",
	}, admins)
	if unverified.IsAdmin {
		t.Fatal("unverified email received administrator status")
	}

	empty := auth.WithAdminStatus(domain.Identity{EmailVerified: true}, admins)
	if empty.IsAdmin {
		t.Fatal("empty configured email granted administrator status")
	}
}
