// Package httpx provides reusable net/http routing and middleware
// infrastructure.
package httpx

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// Router registers mount-aware net/http routes with optional scoped middleware.
type Router struct {
	mux         *http.ServeMux
	mountPath   string
	middlewares []Middleware
	notFound    http.Handler
}

// NewRouter returns a mount-aware router. mountPath is the optional path from
// an already parsed and validated public URL; the router has no reason to know
// the URL's origin.
func NewRouter(mountPath string) *Router {
	return &Router{mux: http.NewServeMux(), mountPath: mountPath}
}

func (r *Router) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	_, pattern := r.mux.Handler(request)
	if pattern == "" && r.notFound != nil && r.containsRequestPath(request.URL.Path) {
		// ServeMux also reports an empty pattern for its generated 405 handler.
		// Treat those method errors as local not-found responses as well.
		r.notFound.ServeHTTP(response, request)
		return
	}
	r.mux.ServeHTTP(response, request)
}

// SetNotFound sets the response for unmatched routes contained by the
// configured application mount. Requests outside a non-root mount retain the
// standard library's isolated 404 response.
func (r *Router) SetNotFound(handler http.Handler) {
	r.notFound = handler
}

// With returns a scoped router that shares routes while applying an additional
// middleware stack to routes registered through it.
func (r *Router) With(stack ...Middleware) *Router {
	scoped := *r
	scoped.middlewares = append(slices.Clone(r.middlewares), stack...)
	return &scoped
}

// Handle registers a route with the router and optional middleware.
func (r *Router) Handle(method, reference string, handler http.Handler, middlewares ...Middleware) {
	combined := append(slices.Clone(r.middlewares), middlewares...)
	r.mux.Handle(method+" "+r.path(reference), Chain(combined...)(handler))
}

// HandleFunc registers a route backed by an http.HandlerFunc.
func (r *Router) HandleFunc(method, reference string, handler http.HandlerFunc, middlewares ...Middleware) {
	r.Handle(method, reference, handler, middlewares...)
}

func (r *Router) containsRequestPath(path string) bool {
	mount := r.mountPath
	return mount == "" || path == mount || strings.HasPrefix(path, mount+"/")
}

func (r *Router) path(reference string) string {
	if reference == "" || reference[0] != '/' || strings.HasPrefix(reference, "//") || strings.ContainsAny(reference, `\#`) {
		panic(fmt.Sprintf("invalid application-relative reference %q", reference))
	}
	return r.mountPath + reference
}
