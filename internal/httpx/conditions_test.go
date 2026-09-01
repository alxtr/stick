package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"stick/internal/httpx"
)

func TestParseIfMatch(t *testing.T) {
	for _, test := range []struct {
		value string
		want  int64
		valid bool
	}{
		{value: `"1"`, want: 1, valid: true},
		{value: `"0"`},
		{value: `*`},
		{value: `"1", "2"`},
		{value: `W/"1"`},
	} {
		t.Run(test.value, func(t *testing.T) {
			got, err := httpx.ParseIfMatch(test.value)
			if test.valid {
				if err != nil || got != test.want {
					t.Fatalf("ParseIfMatch = %d, %v; want %d", got, err, test.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("ParseIfMatch(%q) succeeded", test.value)
			}
		})
	}
}

func TestIfMatchVersionWritesValidationErrors(t *testing.T) {
	for _, test := range []struct {
		name       string
		header     string
		wantStatus int
		wantOK     bool
	}{
		{name: "missing", wantStatus: http.StatusPreconditionRequired},
		{name: "invalid", header: `"1", "2"`, wantStatus: http.StatusBadRequest},
		{name: "valid", header: `"3"`, wantStatus: http.StatusOK, wantOK: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPatch, "/", nil)
			request.Header.Set("If-Match", test.header)
			recorder := httptest.NewRecorder()
			version, ok := httpx.IfMatchVersion(recorder, request)
			if recorder.Code != test.wantStatus || ok != test.wantOK {
				t.Fatalf("result = %d, %t, want %d, %t", recorder.Code, ok, test.wantStatus, test.wantOK)
			}
			if test.wantOK && version != 3 {
				t.Errorf("version = %d, want 3", version)
			}
		})
	}
}
