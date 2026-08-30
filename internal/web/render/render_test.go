package render

import (
	"bytes"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"stick/internal/web/content"
	"stick/internal/web/httpx"
)

func TestUITemplatesUseOnlyTheEmbeddedStylesheet(t *testing.T) {
	assets, err := fs.Glob(content.Assets(), "assets/*")
	if err != nil {
		t.Fatalf("glob assets: %v", err)
	}
	if len(assets) == 0 {
		t.Fatal("UI has no embedded stylesheet")
	}
	for _, asset := range assets {
		if !strings.HasSuffix(asset, ".css") {
			t.Errorf("UI embeds non-stylesheet browser asset %q", asset)
		}
	}

	for _, path := range embeddedTemplatePaths(t) {
		contents, err := fs.ReadFile(content.Templates(), path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		markup := string(contents)
		for _, forbidden := range []string{"<style", "style=", "fonts.googleapis.com", "fonts.gstatic.com"} {
			if strings.Contains(markup, forbidden) {
				t.Errorf("%s contains forbidden inline/external style reference %q", path, forbidden)
			}
		}
	}

	stylesheet, err := fs.ReadFile(content.Assets(), "assets/styles.css")
	if err != nil {
		t.Fatalf("read embedded stylesheet: %v", err)
	}
	css := string(stylesheet)
	if strings.Contains(css, "http://") || strings.Contains(css, "https://") || !strings.Contains(css, "system-ui") {
		t.Error("embedded stylesheet must use local system fonts without external references")
	}
}

func TestUITemplatesContainNoClientSideScripting(t *testing.T) {
	forbidden := map[string]*regexp.Regexp{
		"script element":       regexp.MustCompile(`(?i)<\s*script\b`),
		"inline event handler": regexp.MustCompile(`(?i)\son[a-z]+\s*=`),
		"JavaScript URL":       regexp.MustCompile(`(?i)javascript\s*:`),
		"HTMX attribute":       regexp.MustCompile(`(?i)\bhx-[a-z-]+\s*=`),
	}
	for _, path := range embeddedTemplatePaths(t) {
		contents, err := fs.ReadFile(content.Templates(), path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for name, pattern := range forbidden {
			if pattern.Match(contents) {
				t.Errorf("%s contains forbidden %s", path, name)
			}
		}
	}
}

func embeddedTemplatePaths(t *testing.T) []string {
	t.Helper()
	var paths []string
	err := fs.WalkDir(content.Templates(), "templates", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".html") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("UI has no embedded templates")
	}
	return paths
}

func TestUIRenderBuffersAndDoesNotExposeTemplateErrors(t *testing.T) {
	const sensitiveError = "sensitive template implementation detail"
	tmpl := template.Must(template.New("base").Funcs(template.FuncMap{
		"fail": func() (string, error) { return "", errors.New(sensitiveError) },
	}).Parse(`{{define "base"}}prefix {{fail}}{{end}}`))
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	renderer := Renderer{}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request = request.WithContext(httpx.WithRequestID(request.Context(), "request-42"))
	recorder := httptest.NewRecorder()

	renderer.Render(recorder, request, "render test page", tmpl, nil)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	if body := recorder.Body.String(); strings.Contains(body, "prefix") || strings.Contains(body, sensitiveError) ||
		!strings.Contains(body, "Request failed") || !strings.Contains(body, "request-42") {
		t.Fatalf("response exposed buffered template output/error: %q", body)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want HTML", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	for _, want := range []string{`"request_id":"request-42"`, `"operation":"render test page"`, sensitiveError} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log missing %q: %s", want, logs.String())
		}
	}
}

func TestUIRenderStatusWritesHTMLStatusAndErrorCacheHeaders(t *testing.T) {
	tmpl := template.Must(template.New("base").Parse(`{{define "base"}}rendered{{end}}`))
	renderer := Renderer{}

	for _, test := range []struct {
		name         string
		status       int
		cacheControl string
	}{
		{name: "success", status: http.StatusOK},
		{name: "error", status: http.StatusUnprocessableEntity, cacheControl: "no-store"},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/", nil)

			renderer.RenderStatus(recorder, request, "render status test", tmpl, nil, test.status)

			if recorder.Code != test.status || recorder.Body.String() != "rendered" {
				t.Fatalf("response = %d %q, want %d rendered", recorder.Code, recorder.Body.String(), test.status)
			}
			if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
				t.Errorf("Content-Type = %q, want HTML", got)
			}
			if got := recorder.Header().Get("Cache-Control"); got != test.cacheControl {
				t.Errorf("Cache-Control = %q, want %q", got, test.cacheControl)
			}
		})
	}
}
