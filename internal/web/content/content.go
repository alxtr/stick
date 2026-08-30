// Package content owns the static browser content embedded by the Web UI.
package content

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
)

// Page identifies a complete page template set. Each page is parsed
// separately because pages define shared template blocks such as title and
// content.
type Page string

const (
	// Dashboard identifies the dashboard page template set.
	Dashboard Page = "dashboard"
	// Detail identifies the stick detail page template set.
	Detail Page = "detail"
	// NewStick identifies the new-stick page template set.
	NewStick Page = "new-stick"
	// Error identifies the error page template set.
	Error Page = "error"
)

//go:embed templates
var templateFiles embed.FS

// The Web UI deliberately has no client-side runtime. Keep its only browser
// asset explicit so adding executable assets requires an architectural change.
//
//go:embed assets/styles.css
var assetFiles embed.FS

var pagePartials = map[Page]string{
	Dashboard: "templates/partials/dashboard/*.html",
	Detail:    "templates/partials/detail/*.html",
	NewStick:  "templates/partials/new-stick/*.html",
}

// ParsePage parses the shared components and one page into an isolated
// template set.
func ParsePage(page Page) (*template.Template, error) {
	pageFile := "templates/" + string(page) + ".html"
	if page != Error && page != Dashboard && page != Detail && page != NewStick {
		return nil, fmt.Errorf("unknown page %q", page)
	}
	patterns := []string{"templates/base.html", "templates/components/*.html"}
	if partials, ok := pagePartials[page]; ok {
		patterns = append(patterns, partials)
	}
	patterns = append(patterns, pageFile)
	return template.New("").ParseFS(templateFiles, patterns...)
}

// Templates returns the embedded HTML content for static content checks.
func Templates() fs.FS { return templateFiles }

// Assets returns the embedded browser assets.
func Assets() fs.FS { return assetFiles }
