package api

import (
	"stick/internal/api/health"
)

// ReadinessChecker is the dependency used by the readiness probe.
type ReadinessChecker = health.ReadinessChecker

// healthHandler is kept as an alias for compatibility with the original root
// API package. New code should use health.Handler directly.
type healthHandler = health.Handler

// readinessTimeout preserves the original package-local test seam.
const readinessTimeout = health.ReadinessTimeout

// NewHealth returns a health handler backed by readiness. New code should use
// health.New directly.
func NewHealth(readiness ReadinessChecker) *healthHandler {
	return health.New(readiness)
}
