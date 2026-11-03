package sync

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	things "github.com/JamesHJung/things-cloud-mcp"
)

func TestIsRetryableError(t *testing.T) {
	t.Parallel()

	timeoutErr := &net.DNSError{Err: "timeout", IsTimeout: true}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"500", &things.HTTPStatusError{StatusCode: 500, Status: "500 Internal Server Error"}, true},
		{"502", &things.HTTPStatusError{StatusCode: 502, Status: "502 Bad Gateway"}, true},
		{"503", &things.HTTPStatusError{StatusCode: 503, Status: "503 Service Unavailable"}, true},
		{"504", &things.HTTPStatusError{StatusCode: 504, Status: "504 Gateway Timeout"}, true},
		{"505 is not in range", &things.HTTPStatusError{StatusCode: 505, Status: "505"}, false},
		{"401", &things.HTTPStatusError{StatusCode: http.StatusUnauthorized, Status: "401"}, false},
		{"409 conflict is handled elsewhere", &things.HTTPStatusError{StatusCode: 409, Status: "409"}, false},
		{"wrapped 503", fmt.Errorf("fetching items: %w",
			&things.HTTPStatusError{StatusCode: 503, Status: "503"}), true},
		{"network timeout", timeoutErr, true},
		{"plain error", errors.New("boom"), false},

		// The whole point of dropping the substring match: these used to be
		// treated as retryable because their text contains a 5xx-looking
		// number.
		{"title containing 503", errors.New(`saving task: title "Ticket 503 follow-up"`), false},
		{"byte count containing 500", errors.New("payload too large: 1500 bytes"), false},
		{"index containing 502", errors.New("processing item at index 4502"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRetryableError(tc.err); got != tc.want {
				t.Errorf("isRetryableError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// The cursor must be committed with the batch it describes. If it is saved
// afterwards, a failure in between replays committed work — which duplicates
// change_log rows and, worse, re-applies note delta patches.
func TestCursorIsPersistedWithTheBatch(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	s.history = &things.History{ID: "history-xyz"}
	s.history.LoadedServerIndex = 12

	if _, err := s.processItems([]things.Item{{
		UUID:   "task-1",
		Kind:   things.ItemKindTask7,
		Action: things.ItemActionCreated,
		P:      []byte(`{"tt":"Thing","tp":0,"st":1}`),
	}}, 11); err != nil {
		t.Fatalf("processItems: %v", err)
	}

	id, idx, err := s.readSyncer().getSyncState()
	if err != nil {
		t.Fatalf("getSyncState: %v", err)
	}
	if id != "history-xyz" {
		t.Errorf("history id = %q, want history-xyz", id)
	}
	if idx != 12 {
		t.Errorf("cursor = %d, want 12 (the index actually read)", idx)
	}
}

// A failed batch must leave the cursor alone, so the batch is retried rather
// than skipped.
func TestFailedBatchDoesNotAdvanceTheCursor(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	if err := s.saveSyncState("history-xyz", 5); err != nil {
		t.Fatalf("seed: %v", err)
	}

	s.history = &things.History{ID: "history-xyz"}
	s.history.LoadedServerIndex = 99

	// A checklist item whose payload cannot be parsed makes processItem fail,
	// which rolls the whole transaction back.
	_, err := s.processItems([]things.Item{{
		UUID:   "check-1",
		Kind:   things.ItemKindChecklistItem3,
		Action: things.ItemActionCreated,
		P:      []byte(`{"ss":"not-a-status"}`),
	}}, 98)
	if err == nil {
		t.Fatal("expected the batch to fail")
	}

	_, idx, err := s.readSyncer().getSyncState()
	if err != nil {
		t.Fatalf("getSyncState: %v", err)
	}
	if idx != 5 {
		t.Errorf("cursor = %d after a failed batch, want 5 (unchanged)", idx)
	}
}

// Rebuild replays the log; it must never move the sync cursor.
func TestRebuildLeavesTheCursorAlone(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	process(t, s, 1, "task-1", things.ItemKindTask7, things.ItemActionCreated, `{"tt":"T","tp":0,"st":1}`)
	if err := s.saveSyncState("history-abc", 4242); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := s.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	id, idx, err := s.readSyncer().getSyncState()
	if err != nil {
		t.Fatalf("getSyncState: %v", err)
	}
	if id != "history-abc" || idx != 4242 {
		t.Errorf("cursor = (%q, %d) after Rebuild, want (history-abc, 4242)", id, idx)
	}
	_ = time.Now
}
