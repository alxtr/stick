package application

import (
	"context"
	"errors"
	"log/slog"

	"golang.org/x/sync/errgroup"
)

// Component is a long-lived application component.
type Component interface {
	Run(context.Context) error
}

// Run runs all components until the parent is canceled or one of them stops,
// then cancels the remaining components and returns their errors.
func Run(parent context.Context, components ...Component) error {
	if len(components) == 0 {
		return nil
	}
	group, groupCtx := errgroup.WithContext(parent)
	runCtx, cancel := context.WithCancel(groupCtx)
	defer cancel()
	stopShutdownLog := context.AfterFunc(parent, func() {
		slog.InfoContext(parent, "shutdown requested")
	})
	defer stopShutdownLog()
	results := make(chan error, len(components))
	for _, component := range components {
		component := component
		group.Go(func() error {
			err := component.Run(runCtx)
			// errgroup only cancels its context for non-nil errors. A clean
			// component stop must also stop the remaining components.
			cancel()
			results <- err
			return err
		})
	}

	// Preserve Run's contract of returning all component errors rather than
	// errgroup's first error.
	group.Wait()
	close(results)
	var errs []error
	for runErr := range results {
		if runErr != nil {
			errs = append(errs, runErr)
		}
	}
	return errors.Join(errs...)
}
