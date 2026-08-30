package application

import (
	"context"
	"errors"
	"log/slog"
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
	runCtx, cancel := context.WithCancel(parent)
	defer cancel()
	results := make(chan error, len(components))
	for _, component := range components {
		go func(component Component) { results <- component.Run(runCtx) }(component)
	}

	var errs []error
	shutdownDone := parent.Done()
	remaining := len(components)
	for remaining > 0 {
		select {
		case <-shutdownDone:
			shutdownDone = nil
			slog.InfoContext(parent, "shutdown requested")
			cancel()
		case runErr := <-results:
			if runErr != nil {
				errs = append(errs, runErr)
			}
			cancel()
			remaining--
		}
	}
	return errors.Join(errs...)
}
