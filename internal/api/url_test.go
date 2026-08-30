package api_test

import (
	"testing"

	"stick/internal/api"
	"stick/internal/publicurl"
)

func TestPathComposition(t *testing.T) {
	tests := []struct {
		name      string
		basePath  string
		reference string
		want      string
	}{
		{name: "root", reference: "/api/v1/sticks", want: "/api/v1/sticks"},
		{name: "mounted", basePath: "/ops/stick", reference: "/api/v1/sticks", want: "/ops/stick/api/v1/sticks"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			publicURL := mustParse(t, "https://example.test", tt.basePath)
			if got := api.Path(publicURL, tt.reference); got != tt.want {
				t.Errorf("Path() = %q, want %q", got, tt.want)
			}
			if got := api.Absolute(publicURL, tt.reference); got != publicURL.String()+tt.reference {
				t.Errorf("Absolute() = %q", got)
			}
		})
	}
}

func TestPathRejectsUntrustedReferences(t *testing.T) {
	publicURL := mustParse(t, "https://example.test", "/stick")
	for _, reference := range []string{"", "api/v1/sticks", "//evil.example", `/stick\\one`, "/stick#fragment"} {
		t.Run(reference, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("Path(%q) did not panic", reference)
				}
			}()
			_ = api.Path(publicURL, reference)
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
