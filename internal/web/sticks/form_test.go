package sticks

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseFormBoundsAndParsesBody(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("name=Deploy+Key"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	if err := parseBoundedForm(response, request); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	if got := request.Form.Get("name"); got != "Deploy Key" {
		t.Fatalf("name = %q, want Deploy Key", got)
	}

	oversized := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("padding="+strings.Repeat("x", 9<<10)))
	oversized.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := parseBoundedForm(httptest.NewRecorder(), oversized); err == nil {
		t.Fatal("ParseForm accepted an oversized body")
	}
}
