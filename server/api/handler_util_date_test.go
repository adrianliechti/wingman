package api

import (
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestValueDate(t *testing.T) {
	for input, want := range map[string]*time.Time{
		"":                     nil,
		"2031-05-14":           ptr(time.Date(2031, 5, 14, 0, 0, 0, 0, time.UTC)),
		"2031-05-14T09:00:00Z": ptr(time.Date(2031, 5, 14, 9, 0, 0, 0, time.UTC)),
	} {
		r := httptest.NewRequest("POST", "/search", nil)
		r.Form = url.Values{"since": {input}}
		got, err := valueDate(r, "since")
		if err != nil {
			t.Fatalf("%q: %v", input, err)
		}
		if (got == nil) != (want == nil) || (got != nil && !got.Equal(*want)) {
			t.Fatalf("%q: got %v want %v", input, got, want)
		}
	}
	r := httptest.NewRequest("POST", "/search", nil)
	r.Form = url.Values{"until": {"yesterday"}}
	if _, err := valueDate(r, "until"); err == nil {
		t.Fatal("accepted an unparsable date")
	}
}

func TestValueUntilIncludesCalendarDayAndPreservesTimestamp(t *testing.T) {
	for input, want := range map[string]string{
		"2026-10-10":                    "2026-10-11T00:00:00Z",
		"2026-12-31":                    "2027-01-01T00:00:00Z",
		"2026-10-10T00:00:00Z":          "2026-10-10T00:00:00Z",
		"2026-10-10T12:30:00.123+02:00": "2026-10-10T10:30:00.123Z",
	} {
		r := httptest.NewRequest("POST", "/search", nil)
		r.Form = url.Values{"until": {input}}
		got, err := valueDate(r, "until")
		if err != nil || got == nil || got.UTC().Format(time.RFC3339Nano) != want {
			t.Fatalf("%q: got %v, %v; want %s", input, got, err, want)
		}
	}
}

func ptr(t time.Time) *time.Time { return &t }
