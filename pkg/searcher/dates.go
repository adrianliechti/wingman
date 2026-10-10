package searcher

import (
	"errors"
	"strings"
	"time"
)

// ParseDateBound accepts RFC 3339 timestamps or UTC calendar dates. An upper
// calendar bound includes the entire day by returning the next midnight;
// explicit timestamps are preserved as exclusive upper bounds.
func ParseDateBound(value string, upper bool) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return &t, nil
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		if upper {
			t = t.AddDate(0, 0, 1)
			if t.Year() > 9999 {
				return nil, errors.New("upper date bound is out of range")
			}
		}
		return &t, nil
	}
	return nil, errors.New("expected RFC 3339 or YYYY-MM-DD")
}
