// Package season converts between season years and NBA season strings.
// A season is identified by its starting year: 2025 means "2025-26".
package season

import (
	"fmt"
	"time"
)

// rolloverMonth is when the NBA season year advances. October 1 puts the
// offseason (July-September) with the season that just ended.
const rolloverMonth = time.October

// Current returns the season year for the given time.
func Current(now time.Time) int {
	if now.Month() >= rolloverMonth {
		return now.Year()
	}
	return now.Year() - 1
}

// String formats a season year the way stats.nba.com expects, e.g. "2025-26".
func String(year int) string {
	return fmt.Sprintf("%d-%02d", year, (year+1)%100)
}
