package config

import "context"

// Provider applies configuration values to the accumulated raw configuration.
// Providers are applied in the order supplied to Load. A provider must only
// change values present in its source and must not reset the accumulated
// configuration.
type Provider interface {
	Apply(context.Context, *rawConfig) error
}
