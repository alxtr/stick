package httpx_test

import (
	"testing"

	"stick/internal/publicurl"
	"stick/internal/web/httpx"
)

func TestPathComposition(t *testing.T) {
	tests := []struct {
		name       string
		basePath   string
		reference  string
		wantPath   string
		wantCookie string
	}{
		{name: "root", reference: "/sticks/{id}", wantPath: "/sticks/{id}", wantCookie: "/"},
		{name: "root query", reference: "/sticks/one?tab=history", wantPath: "/sticks/one?tab=history", wantCookie: "/"},
		{name: "mounted", basePath: "/ops/stick", reference: "/sticks/{id}", wantPath: "/ops/stick/sticks/{id}", wantCookie: "/ops/stick"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			publicURL := mustParse(t, "https://example.test", tt.basePath)
			if got := httpx.Path(publicURL, tt.reference); got != tt.wantPath {
				t.Errorf("Path() = %q, want %q", got, tt.wantPath)
			}
			if got := httpx.Absolute(publicURL, "/auth/callback"); got != publicURL.String()+"/auth/callback" {
				t.Errorf("Absolute() = %q", got)
			}
			if got := httpx.CookiePath(publicURL); got != tt.wantCookie {
				t.Errorf("CookiePath() = %q, want %q", got, tt.wantCookie)
			}
		})
	}
}

func TestPathRejectsUntrustedReferences(t *testing.T) {
	publicURL := mustParse(t, "https://example.test", "/stick")
	for _, reference := range []string{"", "sticks/one", "//evil.example", `/stick\\one`, "/stick#fragment"} {
		t.Run(reference, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("Path(%q) did not panic", reference)
				}
			}()
			_ = httpx.Path(publicURL, reference)
		})
	}
}

func TestContainsLocation(t *testing.T) {
	root := mustParse(t, "https://example.com", "")
	mounted := mustParse(t, "https://example.com", "/ops/stick")

	for _, tt := range []struct {
		name     string
		url      publicurl.URL
		location string
		want     bool
	}{
		{name: "root path", url: root, location: "/sticks/one?tab=history", want: true},
		{name: "mounted root", url: mounted, location: "/ops/stick/", want: true},
		{name: "mounted child", url: mounted, location: "/ops/stick/sticks/one", want: true},
		{name: "mounted fragment", url: mounted, location: "/ops/stick/sticks/one#history", want: true},
		{name: "bare mount", url: mounted, location: "/ops/stick", want: true},
		{name: "outside mount", url: mounted, location: "/outside"},
		{name: "prefix collision", url: mounted, location: "/ops/stick-other"},
		{name: "traversal", url: mounted, location: "/ops/stick/../outside"},
		{name: "escaped traversal", url: mounted, location: "/ops/stick/%2e%2e/outside"},
		{name: "network path", url: root, location: "//evil.example"},
		{name: "absolute", url: root, location: "https://evil.example/"},
		{name: "backslash", url: root, location: `/\\evil.example`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := httpx.ContainsLocation(tt.url, tt.location); got != tt.want {
				t.Errorf("ContainsLocation(%q) = %v, want %v", tt.location, got, tt.want)
			}
		})
	}
}

func mustParse(t *testing.T, baseURL, basePath string) publicurl.URL {
	t.Helper()
	publicURL, err := publicurl.Parse(baseURL + basePath)
	if err != nil {
		t.Fatal(err)
	}
	return publicURL
}
