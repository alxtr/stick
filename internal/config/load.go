package config

import (
	"context"
	"fmt"
)

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
