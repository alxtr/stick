package config

import (
	"context"
	"fmt"
)

// Provider applies configuration values to the accumulated raw configuration.
// Providers are applied in the order supplied to Load. A provider must only
// change values present in its source and must not reset the accumulated
// configuration.
type Provider interface {
	Apply(context.Context, *rawConfig) error
}

// Load applies providers in order and normalizes the resulting configuration.
// Later providers override values supplied by earlier providers.
func Load(ctx context.Context, providers ...Provider) (Config, error) {
	raw := rawConfig{}
	for _, provider := range providers {
		if err := provider.Apply(ctx, &raw); err != nil {
			return Config{}, fmt.Errorf("%T: %w", provider, err)
		}
	}
	return normalize(raw)
}
