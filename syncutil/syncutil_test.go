package syncutil

import (
	"testing"
	"time"
)

// "Today" for change-log activity means local midnight. time.Truncate gives
// midnight UTC, which at UTC-8 starts the day at 16:00 local and loses most of
// it.
func TestStartOfLocalDay(t *testing.T) {
	t.Parallel()

	minus8 := time.FixedZone("UTC-8", -8*3600)
	plus13 := time.FixedZone("UTC+13", 13*3600)

	cases := []struct {
		name string
		now  time.Time
	}{
		{"evening at UTC-8", time.Date(2026, 11, 2, 20, 0, 0, 0, minus8)},
		{"morning at UTC-8", time.Date(2026, 11, 2, 7, 30, 0, 0, minus8)},
		{"morning at UTC+13", time.Date(2026, 11, 3, 9, 0, 0, 0, plus13)},
		{"midday UTC", time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := startOfLocalDay(tc.now)

			if h, m, s := got.Clock(); h != 0 || m != 0 || s != 0 {
				t.Errorf("not midnight: %s", got)
			}
			if got.Year() != tc.now.Year() || got.YearDay() != tc.now.YearDay() {
				t.Errorf("wrong day: got %s, now is %s", got, tc.now)
			}
			if got.After(tc.now) {
				t.Errorf("start of day %s is after now %s", got, tc.now)
			}
			if tc.now.Sub(got) >= 24*time.Hour {
				t.Errorf("start of day %s is more than a day before now %s", got, tc.now)
			}

			// What the old implementation did, for contrast.
			if truncated := tc.now.Truncate(24 * time.Hour); truncated.Equal(got) {
				if _, off := tc.now.Zone(); off != 0 {
					t.Errorf("Truncate agrees with local midnight at a non-UTC offset; fixture is not exercising the bug")
				}
			}
		})
	}
}
