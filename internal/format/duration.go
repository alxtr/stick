// Package format formats domain values for display in the UI and outbound
// notifications.
package format

import (
	"fmt"
	"time"
)

// Duration formats a duration using the compact representation used by the
// application. Callers should round to their desired precision first.
func Duration(value time.Duration) string {
	hours := int(value.Hours())
	minutes := int(value.Minutes()) % 60
	if hours > 0 {
		return fmt.Sprintf("%dh %02dmin", hours, minutes)
	}
	return fmt.Sprintf("%d min", minutes)
}
