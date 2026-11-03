package thingscloud

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The zero time must marshal as absent, not as a date in 1754. Timestamp
// encodes via UnixNano, which is meaningless for the zero value.
func TestTimeRejectsZeroTime(t *testing.T) {
	t.Parallel()

	if got := Time(time.Time{}); got != nil {
		bs, _ := json.Marshal(got)
		t.Errorf("Time(zero) = %v (marshals as %s), want nil", got, bs)
	}

	real := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	ts := Time(real)
	if ts == nil {
		t.Fatal("Time(real) = nil")
	}
	bs, err := json.Marshal(ts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.HasPrefix(string(bs), "17") {
		t.Errorf("marshalled as %s, want a 2026 epoch", bs)
	}
}

// A commit body is a JSON object keyed by UUID, so two operations on one item
// cannot both survive. Silently dropping one is worse than refusing.
type fakeItem struct{ id string }

func (f fakeItem) UUID() string { return f.id }

func TestWriteRejectsDuplicateUUIDs(t *testing.T) {
	t.Parallel()

	h := &History{ID: "h", Client: New("http://127.0.0.1:1", "a@b.c", "pw")}

	err := h.Write(fakeItem{id: "3KpQ7mWnXbTd2vZyRf9Jah"}, fakeItem{id: "3KpQ7mWnXbTd2vZyRf9Jah"})
	if err == nil {
		t.Fatal("expected a duplicate-UUID error")
	}
	if !strings.Contains(err.Error(), "duplicate UUID") {
		t.Errorf("error = %q, want it to mention the duplicate", err)
	}
	// It must fail before any request is attempted — a connection error here
	// would mean the guard did not run.
	if strings.Contains(err.Error(), "connection refused") {
		t.Error("the duplicate guard ran after the request was sent")
	}
}
