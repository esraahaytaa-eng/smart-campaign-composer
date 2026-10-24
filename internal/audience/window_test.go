package audience

import (
	"testing"
	"time"
)

// D70: last_months counts calendar months back in the campaign's zone; a day
// the target month lacks becomes its last day (31 March - 1 month = 28 Feb).
func TestWindowBounds_LastMonths(t *testing.T) {
	istanbul, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	months := func(n int) window { return window{LastMonths: &n} }
	at := func(y int, m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 30, 0, 0, istanbul) }
	cases := []struct {
		name string
		now  time.Time
		w    window
		want time.Time
	}{
		{"same day a month back", at(2026, 10, 21, 12), months(1), at(2026, 9, 21, 12)},
		{"twelve months", at(2026, 10, 21, 12), months(12), at(2025, 10, 21, 12)},
		{"clamped to February's end", at(2026, 3, 31, 12), months(1), at(2026, 2, 28, 12)},
		{"leap year", at(2028, 3, 31, 12), months(1), at(2028, 2, 29, 12)},
		{"across the year", at(2026, 1, 31, 9), months(2), at(2025, 11, 30, 9)},
		// 21:30 UTC on 30 Sep is already 1 Oct in Istanbul: months count there.
		{"in the campaign zone", time.Date(2026, 9, 30, 21, 30, 0, 0, time.UTC), months(1), at(2026, 9, 1, 0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end := tc.w.bounds(tc.now, istanbul)
			if !start.Equal(tc.want) || end != nil {
				t.Fatalf("want %v and no end, got %v %v", tc.want, start, end)
			}
		})
	}
}
