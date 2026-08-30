package sticks

import (
	"net/http/httptest"
	"testing"

	"stick/internal/publicurl"
)

func TestSafeRedirect_StaysInsideBasePath(t *testing.T) {
	for _, tt := range []struct {
		name     string
		basePath string
		to       string
		want     string
	}{
		{name: "root deployment allows internal path", to: "/sticks/aa001", want: "/sticks/aa001"},
		{name: "root deployment rejects host-relative path", to: "//evil.example", want: "/sticks/aa001"},
		{name: "root deployment rejects backslash host-relative path", to: "/\\evil.example", want: "/sticks/aa001"},
		{name: "mounted deployment allows mounted path", basePath: "/basepath", to: "/basepath/", want: "/basepath/"},
		{name: "mounted deployment rejects outside path", basePath: "/basepath", to: "/outside", want: "/basepath/sticks/aa001"},
		{name: "mounted deployment rejects prefix collision", basePath: "/basepath", to: "/basepath-other", want: "/basepath/sticks/aa001"},
		{name: "mounted deployment rejects traversal", basePath: "/basepath", to: "/basepath/../outside", want: "/basepath/sticks/aa001"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/", nil)
			q := req.URL.Query()
			q.Set("redirect_to", tt.to)
			req.URL.RawQuery = q.Encode()
			if err := req.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			publicURL, err := publicurl.Parse("http://example.test" + tt.basePath)
			if err != nil {
				t.Fatal(err)
			}
			if got := safeRedirect(req, publicURL, "/sticks/aa001"); got != tt.want {
				t.Errorf("safeRedirect() = %q, want %q", got, tt.want)
			}
		})
	}
}
