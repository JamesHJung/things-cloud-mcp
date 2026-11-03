// Package syncutil provides shared utilities for sync-based CLI tools.
package syncutil

import (
	"strings"
	"time"

	things "github.com/JamesHJung/things-cloud-mcp"
	"github.com/JamesHJung/things-cloud-mcp/sync"
)

// FilterChanges returns changes matching the exact change type.
func FilterChanges(changes []sync.Change, changeType string) []sync.Change {
	var result []sync.Change
	for _, c := range changes {
		if c.ChangeType() == changeType {
			result = append(result, c)
		}
	}
	return result
}

// FilterChangesPrefix returns changes with types starting with the prefix.
func FilterChangesPrefix(changes []sync.Change, prefix string) []sync.Change {
	var result []sync.Change
	for _, c := range changes {
		if strings.HasPrefix(c.ChangeType(), prefix) {
			result = append(result, c)
		}
	}
	return result
}

// DaysSinceCreated returns the number of days since a task was created,
// based on the TaskCreated change in the change history.
func DaysSinceCreated(changes []sync.Change) int {
	for _, c := range changes {
		if c.ChangeType() == "TaskCreated" {
			return int(time.Since(c.Timestamp()).Hours() / 24)
		}
	}
	return 0
}

// CountMoves returns the number of times a task was moved (TaskMovedTo* changes).
func CountMoves(changes []sync.Change) int {
	count := 0
	for _, c := range changes {
		if strings.HasPrefix(c.ChangeType(), "TaskMovedTo") {
			count++
		}
	}
	return count
}

// TaskAge returns the number of days since the task was created.
func TaskAge(task *things.Task) int {
	if task.CreationDate.IsZero() {
		return 0
	}
	return int(time.Since(task.CreationDate).Hours() / 24)
}

// DailySummary holds counts of today's activity.
type DailySummary struct {
	Completed    int `json:"completed"`
	Created      int `json:"created"`
	MovedToToday int `json:"movedToToday"`
	Rescheduled  int `json:"rescheduled"`
}

// BuildDailySummary calculates activity stats from today's changes.
// startOfLocalDay returns midnight of now's own day, in now's location.
//
// time.Truncate(24*time.Hour) does not do this: it truncates relative to the
// zero time in UTC, so it yields midnight UTC. At UTC-8 that is 16:00 local,
// so "today's activity" began mid-afternoon and most of the day was missing.
//
// Local midnight is right *here* because ChangesSince compares against
// synced_at, a real timestamp. It would be wrong for the scheduled_date
// columns, which hold date-only values encoded at midnight UTC — see
// currentUTCDayBounds in the sync package.
func startOfLocalDay(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
}

func BuildDailySummary(syncer *sync.Syncer) DailySummary {
	today := startOfLocalDay(time.Now())
	changes, _ := syncer.ChangesSince(today)

	summary := DailySummary{}
	for _, c := range changes {
		switch c.ChangeType() {
		case "TaskCompleted":
			summary.Completed++
		case "TaskCreated":
			summary.Created++
		case "TaskMovedToToday":
			summary.MovedToToday++
		}
		if strings.HasPrefix(c.ChangeType(), "TaskMovedTo") && c.ChangeType() != "TaskMovedToToday" {
			summary.Rescheduled++
		}
	}
	return summary
}
