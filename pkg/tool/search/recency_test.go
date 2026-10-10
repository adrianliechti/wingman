package search

import (
	"testing"
	"time"
)

func TestRecencySince_CalendarBoundaries(t *testing.T) {
	for _, test := range []struct{ now, window, want string }{
		{"2026-03-31T10:00:00Z", "month", "2026-02-28"},
		{"2024-03-31T10:00:00Z", "month", "2024-02-29"},
		{"2024-02-29T10:00:00Z", "year", "2023-02-28"},
		{"2026-01-31T10:00:00Z", "month", "2025-12-31"},
		{"2026-01-01T10:00:00Z", "week", "2025-12-25"},
		{"2026-03-01T00:30:00+02:00", "day", "2026-02-27"},
	} {
		now, _ := time.Parse(time.RFC3339, test.now)
		got, err := recencySince(test.window, now)
		if err != nil || got.Format("2006-01-02T15:04:05Z07:00") != test.want+"T00:00:00Z" {
			t.Fatalf("%s %s: %v, err %v", test.now, test.window, got, err)
		}
	}
}
