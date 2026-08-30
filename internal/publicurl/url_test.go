package publicurl_test

import (
	"strings"
	"testing"

	"stick/internal/publicurl"
)

func TestParseAndCompose(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "root", value: "http://localhost:8080", want: "http://localhost:8080"},
		{name: "root slash", value: "https://example.com/", want: "https://example.com"},
		{name: "mounted", value: "https://example.com/stick/", want: "https://example.com/stick"},
		{name: "nested", value: "http://127.0.0.1:8080/ops/stick", want: "http://127.0.0.1:8080/ops/stick"},
		{name: "period in segment", value: "https://example.com/apps/stick.v2", want: "https://example.com/apps/stick.v2"},
		{name: "IPv6 authority", value: "http://[::1]:8080", want: "http://[::1]:8080"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := publicurl.Parse(tt.value)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if value != tt.want {
				t.Errorf("Parse() = %q, want %q", value, tt.want)
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
