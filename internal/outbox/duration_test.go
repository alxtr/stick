package outbox

import (
	"testing"
	"time"
)

func TestDuration(t *testing.T) {
	tests := []struct {
		name  string
		value time.Duration
		want  string
	}{
		{name: "minutes", value: 42 * time.Minute, want: "42 min"},
		{name: "hours and minutes", value: 2*time.Hour + 3*time.Minute, want: "2h 03min"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := formatDuration(test.value); got != test.want {
				t.Fatalf("Duration(%v) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}
