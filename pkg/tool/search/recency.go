package search

import (
	"errors"
	"strings"
	"time"
)

func recencySince(window string, now time.Time) (time.Time, error) {
	now = now.UTC()
	year, month, day := now.Date()
	start := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	switch strings.ToLower(strings.TrimSpace(window)) {
	case "day":
		return start.AddDate(0, 0, -1), nil
	case "week":
		return start.AddDate(0, 0, -7), nil
	case "month":
		start = time.Date(year, month, 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	case "year":
		start = time.Date(year-1, month, 1, 0, 0, 0, 0, time.UTC)
	default:
		return time.Time{}, errors.New("search: recency must be day, week, month or year")
	}
	// Clamp month/year windows to the last existing day instead of rolling
	// March 31 back into March or February 29 forward into March.
	lastDay := start.AddDate(0, 1, -1).Day()
	return start.AddDate(0, 0, min(day, lastDay)-1), nil
}
