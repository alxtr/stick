package httpx

import "net/http"

// Middleware wraps an HTTP handler with cross-cutting HTTP behavior.
type Middleware = func(http.Handler) http.Handler

// Chain combines middleware in outermost-to-innermost order.
func Chain(stack ...Middleware) Middleware {
	return func(final http.Handler) http.Handler {
		for index := len(stack) - 1; index >= 0; index-- {
			final = stack[index](final)
		}
		return final
	}
}
