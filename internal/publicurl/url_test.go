package publicurl_test

import (
	"strings"
	"testing"

	"stick/internal/publicurl"
)

func TestParseAndCompose(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		wantString string
		wantOrigin string
		wantMount  string
		wantHTTPS  bool
	}{
		{name: "root", value: "http://localhost:8080", wantString: "http://localhost:8080", wantOrigin: "http://localhost:8080"},
		{name: "root slash", value: "https://example.com/", wantString: "https://example.com", wantOrigin: "https://example.com", wantHTTPS: true},
		{name: "mounted", value: "https://example.com/stick/", wantString: "https://example.com/stick", wantOrigin: "https://example.com", wantMount: "/stick", wantHTTPS: true},
		{name: "nested", value: "http://127.0.0.1:8080/ops/stick", wantString: "http://127.0.0.1:8080/ops/stick", wantOrigin: "http://127.0.0.1:8080", wantMount: "/ops/stick"},
		{name: "period in segment", value: "https://example.com/apps/stick.v2", wantString: "https://example.com/apps/stick.v2", wantOrigin: "https://example.com", wantMount: "/apps/stick.v2", wantHTTPS: true},
		{name: "IPv6 authority", value: "http://[::1]:8080", wantString: "http://[::1]:8080", wantOrigin: "http://[::1]:8080"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := publicurl.Parse(tt.value)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := value.String(); got != tt.wantString {
				t.Errorf("String() = %q, want %q", got, tt.wantString)
			}
			if got := value.Origin(); got != tt.wantOrigin {
				t.Errorf("Origin() = %q, want %q", got, tt.wantOrigin)
			}
			if got := value.MountPath(); got != tt.wantMount {
				t.Errorf("MountPath() = %q, want %q", got, tt.wantMount)
			}
			if got := value.IsHTTPS(); got != tt.wantHTTPS {
				t.Errorf("IsHTTPS() = %v, want %v", got, tt.wantHTTPS)
			}
		})
	}
}

func TestParseRejectsInvalidURLs(t *testing.T) {
	invalid := []string{
		"", "localhost:8080", "ftp://example.com", "http:///stick",
		"http://user@example.com", "http://example.com?x=1", "http://example.com/#fragment",
		"http://example.com//", `http://example.com\\stick`, "http://example.com/%2F",
		"http://example.com/ path", "http://example.com/\x191", "http://example.com:abc",
		"http://example.com:", "http://example.com:65536", "http://bad_host.example",
		"http://-example.com", "http://example.com/.", "http://example.com/../stick",
		"http://example.com/stick!", "http://example.com/stïck",
	}
	for _, raw := range invalid {
		t.Run(raw, func(t *testing.T) {
			if _, err := publicurl.Parse(raw); err == nil {
				t.Fatalf("Parse(%q) unexpectedly succeeded", raw)
			} else if !strings.Contains(err.Error(), "PUBLIC_URL") {
				t.Fatalf("Parse(%q) error = %q, want PUBLIC_URL", raw, err)
			}
		})
	}
}

func TestZeroValueFailsClearly(t *testing.T) {
	assertPanics := func(name string, f func()) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("zero-value URL did not panic")
				}
			}()
			f()
		})
	}
	var zero publicurl.URL
	if err := zero.Validate(); err == nil {
		t.Fatal("zero-value URL passed validation")
	}
	assertPanics("String", func() { _ = zero.String() })
	assertPanics("MountPath", func() { zero.MountPath() })
	assertPanics("IsHTTPS", func() { zero.IsHTTPS() })
	assertPanics("Origin", func() { zero.Origin() })
}
