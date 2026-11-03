package thingscloud

import "time"

// String returns a pointer to a string
func String(str string) *string {
	return &str
}

// Status returns a pointer to a TaskStatus
func Status(val TaskStatus) *TaskStatus {
	return &val
}

// Schedule returns a pointer to a TaskSchedule
func Schedule(val TaskSchedule) *TaskSchedule {
	return &val
}

// TaskTypePtr returns a pointer to a TaskType
func TaskTypePtr(val TaskType) *TaskType {
	return &val
}

// Time returns a pointer to a Timestamp, or nil for the zero time.
//
// Timestamp marshals as a fractional Unix epoch via UnixNano, and the zero
// time's UnixNano is far outside the representable range — it would be written
// to Things Cloud as a date in 1754 rather than as "unset".
func Time(val time.Time) *Timestamp {
	if val.IsZero() {
		return nil
	}
	ts := Timestamp(val)
	return &ts
}
