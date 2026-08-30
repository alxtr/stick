// Package render provides the shared, buffered HTML rendering boundary for
// Web UI route packages.
package render

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"net/http"

	"stick/internal/auth"
	"stick/internal/web/httpx"
	"stick/internal/web/views"
)

// Renderer renders page templates and the browser-safe error page. A renderer
// is a value so each route package can depend on this boundary without
// depending on the root web composition package.
type Renderer struct {
	Mapper        views.Mapper
	ErrorTemplate *template.Template
}

// New returns a renderer configured with the shared view mapper and error
// template.
func New(mapper views.Mapper, errorTemplate *template.Template) Renderer {
	return Renderer{Mapper: mapper, ErrorTemplate: errorTemplate}
}

// Render renders a successful page, buffering template output so an execution
// error cannot expose a partial response.
func (r Renderer) Render(w http.ResponseWriter, request *http.Request, operation string, tmpl *template.Template, data any) {
	r.RenderStatus(w, request, operation, tmpl, data, http.StatusOK)
}

// RenderStatus renders a page with an explicit HTTP status.
func (r Renderer) RenderStatus(
	w http.ResponseWriter,
	request *http.Request,
	operation string,
	tmpl *template.Template,
	data any,
	status int,
) {
	var buffer bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buffer, "base", data); err != nil {
		r.InternalError(w, request, operation, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status >= http.StatusBadRequest {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.WriteHeader(status)
	_, _ = buffer.WriteTo(w)
}

// InternalError logs an implementation error and renders a generic 500 page.
func (r Renderer) InternalError(w http.ResponseWriter, request *http.Request, operation string, err error) {
	httpx.LogError(request.Context(), "request failed", operation, err)
	r.ErrorPage(w, request, http.StatusInternalServerError)
}

// ErrorPage renders a local, mount-aware HTML error response. It is safe to
// use as middleware's error callback.
func (r Renderer) ErrorPage(w http.ResponseWriter, request *http.Request, status int) {
	if r.ErrorTemplate == nil {
		r.writeFallbackErrorPage(w, request, status)
		return
	}
	view := r.Mapper.ErrorPage(request, auth.IdentityFromContext(request.Context()), status)
	var buffer bytes.Buffer
	if err := r.ErrorTemplate.ExecuteTemplate(&buffer, "base", view); err != nil {
		httpx.LogError(request.Context(), "request failed", "render error page", err)
		r.writeFallbackErrorPage(w, request, status)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = buffer.WriteTo(w)
}

func (r Renderer) writeFallbackErrorPage(w http.ResponseWriter, request *http.Request, status int) {
	requestID := ""
	if status >= http.StatusInternalServerError {
		requestID = httpx.RequestID(request.Context())
	}
	requestIDMarkup := ""
	if requestID != "" {
		requestIDMarkup = fmt.Sprintf("<p>Request ID: <code>%s</code></p>", html.EscapeString(requestID))
	}
	body := fmt.Sprintf(
		"<!DOCTYPE html><html lang=\"en\"><head><meta charset=\"UTF-8\"><title>%d · stick</title></head><body><main><h1>Request failed</h1><p>We could not complete your request.</p>%s</main></body></html>",
		status,
		requestIDMarkup,
	)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
