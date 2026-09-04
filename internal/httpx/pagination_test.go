package httpx_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"stick/internal/httpx"
)

func TestParsePagination(t *testing.T) {
	options := httpx.PaginationOptions{DefaultLimit: 20, MaxLimit: 100, MaxOffset: 1000}
	for _, test := range []struct {
		name       string
		query      string
		wantLimit  int
		wantOffset int
		wantParam  string
	}{
		{name: "defaults", wantLimit: 20},
		{name: "custom", query: "?limit=10&offset=50", wantLimit: 10, wantOffset: 50},
		{name: "invalid limit", query: "?limit=0", wantParam: "limit"},
		{name: "invalid offset", query: "?offset=1001", wantParam: "offset"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/history"+test.query, nil)
			limit, offset, err := httpx.ParsePagination(request, options)
			if test.wantParam == "" {
				if err != nil || limit != test.wantLimit || offset != test.wantOffset {
					t.Fatalf("result = %d, %d, %v", limit, offset, err)
				}
				return
			}
			var paginationErr *httpx.PaginationError
			if !errors.As(err, &paginationErr) || paginationErr.Parameter != test.wantParam {
				t.Fatalf("error = %v, want parameter %q", err, test.wantParam)
			}
		})
	}
}
