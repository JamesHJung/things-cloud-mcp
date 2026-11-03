package sync

import (
	"testing"
	"time"

	things "github.com/JamesHJung/things-cloud-mcp"
)

// Things stores scheduled_date / today_index_ref / deadline_date as date-only
// values: midnight UTC of the intended calendar day. The day window must
// therefore be built from the *local* calendar date but expressed at *UTC*
// midnight.
//
// These tests pin that with a fixed clock and fixed zones, so they hold
// regardless of the machine's time zone or the time of day — unlike the
// ambient-clock fixtures, which only expose the bug during the hours when the
// local and UTC dates disagree.
func TestDayBoundsUsesLocalDateAtUTCMidnight(t *testing.T) {
	t.Parallel()

	minus8 := time.FixedZone("UTC-8", -8*3600)
	plus13 := time.FixedZone("UTC+13", 13*3600)

	cases := []struct {
		name string
		now  time.Time
		// The calendar day the window must cover, as a UTC-midnight date.
		wantDay time.Time
	}{
		{
			// 20:00 on the 2nd at UTC-8 is already 04:00 on the 3rd in UTC.
			// The window must still be the 2nd — this is the regression.
			name:    "evening at UTC-8, after UTC midnight",
			now:     time.Date(2026, 11, 2, 20, 0, 0, 0, minus8),
			wantDay: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC),
		},
		{
			name:    "morning at UTC-8, same UTC date",
			now:     time.Date(2026, 11, 2, 9, 0, 0, 0, minus8),
			wantDay: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC),
		},
		{
			// 09:00 on the 3rd at UTC+13 is still 20:00 on the 2nd in UTC.
			name:    "morning at UTC+13, before UTC midnight",
			now:     time.Date(2026, 11, 3, 9, 0, 0, 0, plus13),
			wantDay: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC),
		},
		{
			name:    "midday UTC",
			now:     time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC),
			wantDay: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end := dayBoundsAt(tc.now)

			if start != tc.wantDay.Unix() {
				t.Errorf("start = %d (%s), want %d (%s)",
					start, time.Unix(start, 0).UTC().Format(time.RFC3339),
					tc.wantDay.Unix(), tc.wantDay.Format(time.RFC3339))
			}
			if end-start != 86400 {
				t.Errorf("window is %ds, want exactly 86400", end-start)
			}
			// A task scheduled for that day must land inside the window.
			if tc.wantDay.Unix() < start || tc.wantDay.Unix() >= end {
				t.Error("a task scheduled for this day falls outside the window")
			}
			// Neither neighbour may.
			for _, off := range []int64{-86400, 86400} {
				if v := tc.wantDay.Unix() + off; v >= start && v < end {
					t.Errorf("a task %+d days away falls inside the window", off/86400)
				}
			}
		})
	}
}

func TestIsTodayAtComparesLocalDate(t *testing.T) {
	t.Parallel()

	minus8 := time.FixedZone("UTC-8", -8*3600)
	plus13 := time.FixedZone("UTC+13", 13*3600)
	day := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}

	cases := []struct {
		name   string
		target time.Time
		now    time.Time
		want   bool
	}{
		{
			// The regression: evening at UTC-8, where now.UTC() is tomorrow.
			name:   "today's date, evening at UTC-8",
			target: day(2026, 11, 2),
			now:    time.Date(2026, 11, 2, 20, 0, 0, 0, minus8),
			want:   true,
		},
		{
			name:   "tomorrow's date, evening at UTC-8",
			target: day(2026, 11, 3),
			now:    time.Date(2026, 11, 2, 20, 0, 0, 0, minus8),
			want:   false,
		},
		{
			name:   "yesterday's date, evening at UTC-8",
			target: day(2026, 11, 1),
			now:    time.Date(2026, 11, 2, 20, 0, 0, 0, minus8),
			want:   false,
		},
		{
			name:   "today's date, morning at UTC+13",
			target: day(2026, 11, 3),
			now:    time.Date(2026, 11, 3, 9, 0, 0, 0, plus13),
			want:   true,
		},
		{
			name:   "today's date at UTC",
			target: day(2026, 11, 2),
			now:    time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC),
			want:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTodayAt(&tc.target, tc.now); got != tc.want {
				t.Errorf("isTodayAt(%s, %s) = %v, want %v",
					tc.target.Format("2006-01-02"), tc.now.Format(time.RFC3339), got, tc.want)
			}
		})
	}
}

// taskLocationAt must put a task scheduled for the local day in Today, even
// when UTC has already rolled over.
// anytimeTask builds an Anytime task with the given date-only sr and tir.
func anytimeTask(sr, tir *time.Time) *things.Task {
	return &things.Task{
		Schedule:            things.TaskScheduleAnytime,
		ScheduledDate:       sr,
		TodayIndexReference: tir,
	}
}

// taskLocationAt must put a task scheduled for the local day in Today, even
// when UTC has already rolled over.
func TestTaskLocationAtRespectsLocalDate(t *testing.T) {
	t.Parallel()

	minus8 := time.FixedZone("UTC-8", -8*3600)
	now := time.Date(2026, 11, 2, 20, 0, 0, 0, minus8)
	today := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	tomorrow := today.Add(24 * time.Hour)

	if got := taskLocationAt(anytimeTask(&today, nil), now); got != LocationToday {
		t.Errorf("scheduled for the local day: got %v, want Today", got)
	}
	if got := taskLocationAt(anytimeTask(nil, &today), now); got != LocationToday {
		t.Errorf("tir on the local day: got %v, want Today", got)
	}
	if got := taskLocationAt(anytimeTask(&tomorrow, nil), now); got == LocationToday {
		t.Error("scheduled for tomorrow must not be Today")
	}
}
