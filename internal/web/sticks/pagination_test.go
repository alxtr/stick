package sticks

import "testing"

func TestHistoryPageParser(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		parse func(string) int
		want  int
	}{
		{name: "page default", raw: "invalid", parse: parseHistoryPage, want: 1},
		{name: "page negative", raw: "-1", parse: parseHistoryPage, want: 1},
		{name: "page bounded", raw: "999999999", parse: parseHistoryPage, want: maxHistoryPage},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.parse(test.raw); got != test.want {
				t.Fatalf("parse(%q) = %d, want %d", test.raw, got, test.want)
			}
		})
	}
}

func TestHistoryPageLinksAreBounded(t *testing.T) {
	links := historyPageLinks(5000, 10_000)
	if len(links) != maxHistoryPageLinks {
		t.Fatalf("links = %v, want %d entries", links, maxHistoryPageLinks)
	}
	for i := 1; i < len(links); i++ {
		if links[i] != links[i-1]+1 {
			t.Fatalf("links are not contiguous: %v", links)
		}
	}
}
