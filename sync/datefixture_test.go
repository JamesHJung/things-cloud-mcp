package sync

import "time"

// dateOnly returns the shape Things actually stores in the date columns:
// midnight **UTC** of the given moment's **local** calendar date.
//
// scheduled_date, today_index_ref and deadline_date are all date-only values
// encoded this way — in a real database every one of them is an exact multiple
// of 86400. (completion_date, by contrast, is a true timestamp.)
//
// Fixtures must use this rather than a wall-clock time. A test that stores
// time.Now() is asserting on a value production never produces, and because
// the old comparison was UTC-on-UTC it passed anyway — which is how the
// local/UTC day-boundary bug survived in a tested code path.
func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// daysFromToday is dateOnly for a day offset from today, local calendar.
func daysFromToday(days int) time.Time {
	return dateOnly(time.Now().AddDate(0, 0, days))
}
