package content_test

import (
	"testing"

	"stick/internal/web/content"
)

func TestParsePage(t *testing.T) {
	for _, page := range []content.Page{
		content.Dashboard,
		content.Detail,
		content.NewStick,
		content.Error,
	} {
		t.Run(string(page), func(t *testing.T) {
			if _, err := content.ParsePage(page); err != nil {
				t.Fatalf("ParsePage: %v", err)
			}
		})
	}
}

func TestParsePageRejectsUnknownPage(t *testing.T) {
	if _, err := content.ParsePage("unknown"); err == nil {
		t.Fatal("ParsePage accepted an unknown page")
	}
}
