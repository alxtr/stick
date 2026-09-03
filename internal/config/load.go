package config

import (
	"context"
	"fmt"
)

// Provider applies configuration values to the accumulated configuration.
// Providers are applied in the order supplied to Load. A provider must only
// change values present in its source and must not reset the accumulated
// configuration.
type Provider interface {
	Apply(context.Context, *Config) error
}

// Load applies providers in order and normalizes the resulting configuration.
// Later providers override values supplied by earlier providers.
func Load(ctx context.Context, providers ...Provider) (Config, error) {
	config := Config{}
	for _, provider := range providers {
		if err := provider.Apply(ctx, &config); err != nil {
			return Config{}, fmt.Errorf("%T: %w", provider, err)
		}
	}
	return normalize(config)
}
