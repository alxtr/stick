package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"stick/internal/httpx"
)

func TestWriteJSONAndNotFound(t *testing.T) {
	recorder := httptest.NewRecorder()
	httpx.WriteJSON(recorder, http.StatusCreated, map[string]string{"status": "ok"})
	if recorder.Code != http.StatusCreated || recorder.Body.String() != `{"status":"ok"}` {
		t.Fatalf("response = %d %q", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Content-Type") != "application/json" || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers = %v", recorder.Header())
	}

	notFound := httptest.NewRecorder()
	httpx.NotFound(notFound, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if notFound.Code != http.StatusNotFound || notFound.Body.String() != `{"error":"not found"}` {
		t.Fatalf("not found response = %d %q", notFound.Code, notFound.Body.String())
	}
}

func TestWriteCollectionSupportsConditionalRequests(t *testing.T) {
	first := httptest.NewRecorder()
	httpx.WriteCollection(first, httptest.NewRequest(http.MethodGet, "/", nil), []string{"one"})
	etag := first.Header().Get("ETag")
	if first.Code != http.StatusOK || etag == "" {
		t.Fatalf("first response = %d, ETag %q", first.Code, etag)
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	httpx.WriteCollection(second, request, []string{"one"})
	if second.Code != http.StatusNotModified || second.Body.Len() != 0 {
		t.Fatalf("conditional response = %d %q", second.Code, second.Body.String())
	}
}

func TestDecodeJSONRejectsInvalidBodies(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "malformed", body: "{"},
		{name: "unknown field", body: `{"name":"value","extra":true}`},
		{name: "trailing value", body: `{"name":"value"}{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			recorder := httptest.NewRecorder()
			var destination struct {
				Name string `json:"name"`
			}
			if httpx.DecodeJSON(recorder, request, &destination) {
				t.Fatal("DecodeJSON accepted invalid body")
			}
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", recorder.Code)
			}
		})
	}
}

func TestHeadersAddsSecurityHeader(t *testing.T) {
	handler := httpx.Headers(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusNoContent || recorder.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("response = %d, headers = %v", recorder.Code, recorder.Header())
	}
}
