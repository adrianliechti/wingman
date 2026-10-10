package searcher

import (
	"testing"
	"time"
)

func TestParseDateBound(t *testing.T) {
	for _, tc := range []struct {
		value string
		upper bool
		want  string
	}{
		{"", false, ""},
		{"  ", true, ""},
		{"2026-10-10", false, "2026-10-10T00:00:00Z"},
		{" 2026-10-10 ", true, "2026-10-11T00:00:00Z"},
		{"2028-02-29", true, "2028-03-01T00:00:00Z"},
		{"2026-12-31", true, "2027-01-01T00:00:00Z"},
		{"2026-10-10T00:00:00Z", true, "2026-10-10T00:00:00Z"},
		{"2026-10-10T12:30:00.123+02:00", true, "2026-10-10T10:30:00.123Z"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			got, err := ParseDateBound(tc.value, tc.upper)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if got != nil {
					t.Fatalf("got %v, want nil", got)
				}
				return
			}
			if got == nil || got.UTC().Format(time.RFC3339Nano) != tc.want {
				t.Fatalf("got %v, want %s", got, tc.want)
			}
		})
	}
	for _, value := range []string{"yesterday", "2026-02-30", "2026-13-01", "2026-10-10T12:00:00"} {
		if _, err := ParseDateBound(value, false); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
	if _, err := ParseDateBound("9999-12-31", true); err == nil {
		t.Error("accepted overflowing upper date")
	}
}
