// Package server composes the HTTP application and owns its lifecycle.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"stick/internal/api/health"
	"stick/internal/api/sticks"
	"stick/internal/application"
	"stick/internal/auth"
	"stick/internal/publicurl"
	"stick/internal/web/httpx"
)

// Options contains the settings required to build the HTTP server.
type Options struct {
	PublicURL            string
	ListenAddr           string
	JWT                  auth.JWTConfig
	AdminEmails          []string
	NotificationsEnabled bool
}

// Runner owns the complete HTTP server lifecycle, including its listener,
// handler graph, and bounded graceful shutdown.
type Runner struct {
	listenAddr string
	handler    http.Handler
}

// NewRunner builds the HTTP application and its dependency graph.
// Listening is intentionally deferred to Run so startup failures are reported
// through the runner lifecycle rather than escaping during HTTP composition.
func NewRunner(service *application.Service, readiness health.ReadinessChecker, options Options) (*Runner, error) {
	handler, err := newHandler(service, readiness, options)
	if err != nil {
		return nil, fmt.Errorf("HTTP server: %w", err)
	}
	return &Runner{
		listenAddr: options.ListenAddr,
		handler:    handler,
	}, nil
}

// newHandler builds the complete HTTP graph.
func newHandler(service *application.Service, readiness health.ReadinessChecker, options Options) (http.Handler, error) {
	publicURL, err := publicurl.Parse(options.PublicURL)
	if err != nil {
		return nil, fmt.Errorf("public URL: %w", err)
	}
	parsedPublicURL, err := url.Parse(publicURL)
	if err != nil {
		return nil, fmt.Errorf("public URL: %w", err)
	}

	router := httpx.NewRouter(parsedPublicURL.Path)
	healthHandler := health.New(readiness)
	sticksHandler := sticks.New(service, auth.NewJWTValidator(options.JWT), options.AdminEmails, publicURL, options.NotificationsEnabled)

	healthHandler.Register(router)
	sticksHandler.Register(router)
	router.SetNotFound(http.HandlerFunc(httpx.NotFound))

	middlewares := httpx.Chain(
		httpx.RequestLogger,
		httpx.Headers)
	return middlewares(router), nil
}

// Run starts the HTTP server and shuts it down when ctx is canceled.
func (r *Runner) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", r.listenAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()

	slog.InfoContext(ctx, "server listening", "address", listener.Addr().String())
	server := newHTTPServer(listener.Addr().String(), r.handler)
	err = serveWithContext(ctx, server, listener, shutdownTimeout)

	slog.InfoContext(ctx, "server stopped")
	return err
}

const (
	httpReadTimeout       = 15 * time.Second
	httpReadHeaderTimeout = 5 * time.Second
	httpWriteTimeout      = 30 * time.Second
	httpIdleTimeout       = 60 * time.Second
	shutdownTimeout       = 15 * time.Second
)

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadTimeout:       httpReadTimeout,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
	}
}

// serveWithContext owns the HTTP serving lifecycle. Whether serving stops
// because the context is canceled or the listener fails, active handlers get
// the same bounded opportunity to finish before application resources close.
func serveWithContext(ctx context.Context, server *http.Server, listener net.Listener, timeout time.Duration) error {
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()

	var runErr error
	serveReturned := false
	select {
	case runErr = <-serveErr:
		serveReturned = true
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	shutdownErr := server.Shutdown(shutdownCtx)
	cancel()
	if shutdownErr != nil {
		_ = server.Close()
	}

	if !serveReturned {
		runErr = <-serveErr
	}
	if errors.Is(runErr, http.ErrServerClosed) {
		runErr = nil
	}

	if shutdownErr != nil {
		shutdownErr = fmt.Errorf("graceful shutdown: %w", shutdownErr)
	}
	return errors.Join(runErr, shutdownErr)
}
