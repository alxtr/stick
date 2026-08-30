package security

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHeaders(t *testing.T) {
	recorder := httptest.NewRecorder()
	Headers(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	for header, want := range map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "strict-origin-when-cross-origin",
		"Content-Security-Policy": "default-src 'self'",
	} {
		if got := recorder.Header().Get(header); !strings.Contains(got, want) {
			t.Errorf("%s = %q, want substring %q", header, got, want)
		}
	}
	csp := recorder.Header().Get("Content-Security-Policy")
	for _, want := range []string{"script-src 'none'", "style-src 'self'", "font-src 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("Content-Security-Policy = %q, want directive %q", csp, want)
		}
	}
	for _, forbidden := range []string{"'unsafe-inline'", "fonts.googleapis.com", "fonts.gstatic.com"} {
		if strings.Contains(csp, forbidden) {
			t.Errorf("Content-Security-Policy = %q, must not contain %q", csp, forbidden)
		}
	}
}
