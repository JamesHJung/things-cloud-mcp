package sync

import (
	"testing"
	"time"

	things "github.com/JamesHJung/things-cloud-mcp"
)

// Changes read back from change_log must report the type they were recorded
// with. Returning UnknownChange for all of them made a completion
// indistinguishable from a parse failure and broke ChangeType() filtering.
func TestChangeLogReadsBackRealChangeTypes(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	process(t, s, 1, "task-1", things.ItemKindTask7, things.ItemActionCreated,
		`{"tt":"Thing","tp":0,"st":1}`)
	process(t, s, 2, "task-1", things.ItemKindTask7, things.ItemActionModified,
		`{"ss":3,"sp":1703738457.74,"md":1703738457.74}`)

	changes, err := s.ChangesForEntity("task-1")
	if err != nil {
		t.Fatalf("ChangesForEntity: %v", err)
	}
	if len(changes) == 0 {
		t.Fatal("no changes read back")
	}

	seen := map[string]bool{}
	for _, c := range changes {
		seen[c.ChangeType()] = true
		if c.ChangeType() == "UnknownChange" {
			t.Errorf("replayed change reported as UnknownChange; details lost")
		}
		if c.EntityUUID() != "task-1" {
			t.Errorf("EntityUUID = %q", c.EntityUUID())
		}
	}
	for _, want := range []string{"TaskCreated", "TaskCompleted"} {
		if !seen[want] {
			t.Errorf("expected %s among replayed types, got %v", want, keys(seen))
		}
	}
}

// The payload and the raw envelope travel with the entry, so a consumer can
// re-derive what the server actually sent.
func TestLoggedChangeCarriesPayloadAndEnvelope(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	process(t, s, 7, "task-1", things.ItemKindTask7, things.ItemActionCreated,
		`{"tt":"Thing","tp":0,"st":1}`)

	changes, err := s.ChangesForEntity("task-1")
	if err != nil {
		t.Fatalf("ChangesForEntity: %v", err)
	}

	lc, ok := changes[0].(LoggedChange)
	if !ok {
		t.Fatalf("expected LoggedChange, got %T", changes[0])
	}
	if lc.Payload() == "" {
		t.Error("payload not carried")
	}
	kind, action, present := lc.Envelope()
	if !present {
		t.Fatal("envelope not carried")
	}
	if kind != things.ItemKindTask7 {
		t.Errorf("kind = %q, want Task7", kind)
	}
	if action != things.ItemActionCreated {
		t.Errorf("action = %d, want %d", action, things.ItemActionCreated)
	}
	if lc.ServerIndex() != 7 {
		t.Errorf("ServerIndex = %d, want 7", lc.ServerIndex())
	}
	if lc.Timestamp().IsZero() {
		t.Error("timestamp not carried")
	}
}

// Pre-v5 rows have no envelope; that must read as absent, not as kind "" with
// action 0 (which would look like a legitimate create).
func TestLoggedChangeEnvelopeAbsentForLegacyRows(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	if _, err := s.db.Exec(`
		INSERT INTO change_log (server_index, synced_at, change_type, entity_type, entity_uuid, payload)
		VALUES (3, ?, 'TaskCreated', 'Task', 'legacy-1', '{}')`, time.Now().Unix()); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	changes, err := s.ChangesForEntity("legacy-1")
	if err != nil {
		t.Fatalf("ChangesForEntity: %v", err)
	}
	lc := changes[0].(LoggedChange)
	if lc.ChangeType() != "TaskCreated" {
		t.Errorf("ChangeType = %q", lc.ChangeType())
	}
	if _, _, present := lc.Envelope(); present {
		t.Error("legacy row reported an envelope it never stored")
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
