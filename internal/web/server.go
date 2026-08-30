// Package web composes the browser application and owns its HTTP lifecycle.
package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"stick/internal/application"
	coreauth "stick/internal/auth"
	"stick/internal/publicurl"
	webauth "stick/internal/web/auth"
	"stick/internal/web/content"
	"stick/internal/web/dashboard"
	"stick/internal/web/health"
	"stick/internal/web/httpx"
	"stick/internal/web/render"
	"stick/internal/web/security"
	"stick/internal/web/sticks"
	"stick/internal/web/views"
)

// Options contains the settings required to build the browser HTTP server.
type Options struct {
	PublicURL            publicurl.URL
	ListenAddr           string
	OIDC                 coreauth.OIDCConfig
	SessionSecret        []byte
	AdminEmails          []string
	Timezone             *time.Location
	NotificationsEnabled bool
}

// Runner owns the complete HTTP server lifecycle, including its listener,
// handler graph, and bounded graceful shutdown.
type Runner struct {
	listenAddr string
	handler    http.Handler
}

// NewRunner builds the browser application and its HTTP dependency graph.
// Listening is intentionally deferred to Run so startup failures are reported
// through the runner lifecycle rather than escaping during HTTP composition.
func NewRunner(service *application.Service, readiness health.ReadinessChecker, options Options) (*Runner, error) {
	handler, err := newHandler(service, readiness, options)
	if err != nil {
		return nil, fmt.Errorf("web UI: %w", err)
	}
	return &Runner{
		listenAddr: options.ListenAddr,
		handler:    handler,
	}, nil
}

// newHandler builds the complete browser HTTP graph. The route packages own
// their handlers and paths; this function is the single composition boundary
// that wires them to the server-level middleware and mount-aware router.
func newHandler(service *application.Service, readiness health.ReadinessChecker, options Options) (http.Handler, error) {
	if err := options.PublicURL.Validate(); err != nil {
		return nil, fmt.Errorf("public URL: %w", err)
	}

	dashboardTemplate, err := content.ParsePage(content.Dashboard)
	if err != nil {
		return nil, fmt.Errorf("parse dashboard templates: %w", err)
	}
	detailTemplate, err := content.ParsePage(content.Detail)
	if err != nil {
		return nil, fmt.Errorf("parse detail templates: %w", err)
	}
	newStickTemplate, err := content.ParsePage(content.NewStick)
	if err != nil {
		return nil, fmt.Errorf("parse new-stick templates: %w", err)
	}
	errorTemplate, err := content.ParsePage(content.Error)
	if err != nil {
		return nil, fmt.Errorf("parse error templates: %w", err)
	}

	renderer := render.New(
		views.NewMapper(options.PublicURL, options.Timezone, options.NotificationsEnabled),
		errorTemplate,
	)
	csrf := security.CSRFProtection(options.SessionSecret, renderer.ErrorPage)
	browserAuth := security.RequireAuth(options.SessionSecret, options.AdminEmails, options.PublicURL)
	dashboardHandler := dashboard.New(service, renderer, dashboardTemplate, options.NotificationsEnabled)
	sticksHandler := sticks.New(service, options.PublicURL, renderer, detailTemplate, newStickTemplate, options.NotificationsEnabled)
	oidcHandler := webauth.NewOIDCHandler(options.OIDC, options.PublicURL, options.SessionSecret)

	router := httpx.NewRouter(options.PublicURL)
	health.Register(router, health.New(readiness))
	router.SetNotFound(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		renderer.ErrorPage(w, r, http.StatusNotFound)
	}))
	webauth.Register(router.With(csrf), oidcHandler)
	content.RegisterAssets(router)
	dashboard.Register(router.With(browserAuth, csrf), dashboardHandler)
	sticks.Register(router.With(browserAuth, csrf), sticksHandler)

	if options.PublicURL.MountPath() != "" {
		router.HandleMount(http.MethodGet, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			location := httpx.Path(options.PublicURL, "/")
			if request.URL.RawQuery != "" {
				location += "?" + request.URL.RawQuery
			}
			http.Redirect(w, request, location, http.StatusMovedPermanently)
		}))
	}

	middlewares := httpx.Chain(
		httpx.RequestLogger,
		security.Headers)
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
