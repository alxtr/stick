package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	domain "stick/internal/core"

	"github.com/coreos/go-oidc/v3/oidc"
)

// JWTConfig contains the settings used to validate external bearer tokens.
// Endpoint is an OIDC issuer URL. The provider's discovery document and JWKS
// are used to validate the token signature and registered claims.
type JWTConfig struct {
	Endpoint string
	Audience string
	Scope    string
}

// JWTValidator validates external JWTs issued by the configured identity
// provider. Provider discovery is lazy so an identity-provider outage does
// not prevent the service from starting.
type JWTValidator struct {
	config JWTConfig

	mu               sync.Mutex
	provider         *oidc.Provider
	discoveryDone    chan struct{}
	lastDiscoveryErr error
	retryDiscoveryAt time.Time
	failureBackoff   time.Duration
}

const (
	providerDiscoveryTimeout = 10 * time.Second
	initialDiscoveryBackoff  = time.Second
	maxDiscoveryBackoff      = 30 * time.Second
)

// NewJWTValidator returns a validator for config.
func NewJWTValidator(config JWTConfig) *JWTValidator {
	return &JWTValidator{config: config}
}

// Validate verifies rawToken and returns the identity carried by it.
func (v *JWTValidator) Validate(ctx context.Context, rawToken string) (domain.Identity, error) {
	if strings.TrimSpace(rawToken) == "" {
		return domain.Identity{}, errors.New("token is empty")
	}
	provider, err := v.providerFor(ctx)
	if err != nil {
		return domain.Identity{}, err
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: v.config.Audience})
	token, err := verifier.Verify(ctx, rawToken)
	if err != nil {
		return domain.Identity{}, err
	}

	var claims jwtClaims
	if err := token.Claims(&claims); err != nil {
		return domain.Identity{}, err
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return domain.Identity{}, errors.New("token subject is required")
	}
	if !scopeContains(claims.Scope, claims.SCP, v.config.Scope) {
		return domain.Identity{}, errors.New("token scope is insufficient")
	}

	return domain.Identity{
		Sub:           claims.Subject,
		Name:          claims.Name,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
	}, nil
}

type jwtClaims struct {
	Subject       string      `json:"sub"`
	Name          string      `json:"name"`
	Email         string      `json:"email"`
	EmailVerified bool        `json:"email_verified"`
	Scope         scopeValues `json:"scope"`
	SCP           scopeValues `json:"scp"`
}

type scopeValues []string

func (s *scopeValues) UnmarshalJSON(data []byte) error {
	var values []string
	if err := json.Unmarshal(data, &values); err == nil {
		*s = values
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*s = []string{value}
	return nil
}

func scopeContains(scope, scp scopeValues, required string) bool {
	provided := make(map[string]struct{})
	for _, value := range scope {
		for _, item := range strings.Fields(value) {
			provided[item] = struct{}{}
		}
	}
	for _, value := range scp {
		for _, item := range strings.Fields(value) {
			provided[item] = struct{}{}
		}
	}
	for _, value := range strings.Fields(required) {
		if _, ok := provided[value]; !ok {
			return false
		}
	}
	return true
}

func (v *JWTValidator) providerFor(ctx context.Context) (*oidc.Provider, error) {
	for {
		v.mu.Lock()
		if v.provider != nil {
			provider := v.provider
			v.mu.Unlock()
			return provider, nil
		}
		if v.lastDiscoveryErr != nil && time.Now().Before(v.retryDiscoveryAt) {
			err := v.lastDiscoveryErr
			v.mu.Unlock()
			return nil, err
		}
		if done := v.discoveryDone; done != nil {
			v.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		done := make(chan struct{})
		v.discoveryDone = done
		v.mu.Unlock()

		discoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), providerDiscoveryTimeout)
		provider, err := oidc.NewProvider(oidc.ClientContext(discoveryCtx, &http.Client{Timeout: providerDiscoveryTimeout}), v.config.Endpoint)
		cancel()

		v.mu.Lock()
		if err == nil {
			v.provider = provider
			v.lastDiscoveryErr = nil
			v.retryDiscoveryAt = time.Time{}
			v.failureBackoff = 0
		} else {
			err = fmt.Errorf("discover identity provider: %w", err)
			v.lastDiscoveryErr = err
			v.failureBackoff = nextDiscoveryBackoff(v.failureBackoff)
			v.retryDiscoveryAt = time.Now().Add(v.failureBackoff)
		}
		v.discoveryDone = nil
		close(done)
		v.mu.Unlock()
		return provider, err
	}
}

func nextDiscoveryBackoff(current time.Duration) time.Duration {
	if current <= 0 {
		return initialDiscoveryBackoff
	}
	if current >= maxDiscoveryBackoff/2 {
		return maxDiscoveryBackoff
	}
	return current * 2
}
