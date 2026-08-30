package netutil

import (
	"net/url"
	"testing"
)

func TestIsLoopbackHost(t *testing.T) {
	for _, test := range []struct {
		host string
		want bool
	}{
		{host: "localhost", want: true},
		{host: "LOCALHOST.", want: true},
		{host: "127.0.0.1", want: true},
		{host: "::1", want: true},
		{host: "example.com", want: false},
		{host: "192.168.1.10", want: false},
	} {
		t.Run(test.host, func(t *testing.T) {
			if got := IsLoopbackHost(test.host); got != test.want {
				t.Errorf("IsLoopbackHost(%q) = %v, want %v", test.host, got, test.want)
			}
		})
	}
}

func TestSafeEndpointOmitsSensitiveURLComponents(t *testing.T) {
	if got := SafeEndpoint("https://hooks.example.test/path?token=secret#fragment"); got != "https://hooks.example.test" {
		t.Fatalf("SafeEndpoint() = %q, want authority only", got)
	}
}

func TestIsHTTPSOrLoopbackHTTP(t *testing.T) {
	for _, test := range []struct {
		endpoint string
		want     bool
	}{
		{endpoint: "https://example.com", want: true},
		{endpoint: "http://localhost:8080", want: true},
		{endpoint: "http://example.com", want: false},
		{endpoint: "ftp://localhost", want: false},
	} {
		t.Run(test.endpoint, func(t *testing.T) {
			endpoint, err := url.Parse(test.endpoint)
			if err != nil {
				t.Fatal(err)
			}
			if got := IsHTTPSOrLoopbackHTTP(endpoint); got != test.want {
				t.Errorf("IsHTTPSOrLoopbackHTTP(%q) = %v, want %v", test.endpoint, got, test.want)
			}
		})
	}
}
