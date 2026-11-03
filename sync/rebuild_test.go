package sync

import (
	"testing"

	things "github.com/JamesHJung/things-cloud-mcp"
)

func TestRebuildRepairsMissedDeletions(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	// Build a history, then corrupt the materialized state the way the old
	// sync layer did: the delete was logged but never applied.
	process(t, s, 1, "task-1", things.ItemKindTask7, things.ItemActionCreated, `{"tt":"Doomed","tp":0,"st":1}`)
	process(t, s, 2, "task-keep", things.ItemKindTask7, things.ItemActionCreated, `{"tt":"Keeper","tp":0,"st":1}`)
	process(t, s, 3, "task-1", things.ItemKindTask7, things.ItemActionDeleted, `{}`)

	// Resurrect the deleted row behind the sync layer's back.
	if _, err := s.db.Exec(
		`INSERT OR REPLACE INTO tasks (uuid, type, title, status, schedule, deleted)
		 VALUES ('task-1', 0, 'Doomed', 0, 1, 0)`); err != nil {
		t.Fatalf("seeding corruption failed: %v", err)
	}
	if d, err := s.Diagnose(); err != nil {
		t.Fatalf("Diagnose failed: %v", err)
	} else if d.StaleDeletes != 1 {
		t.Fatalf("setup: StaleDeletes = %d, want 1", d.StaleDeletes)
	}

	res, err := s.Rebuild()
	if err != nil {
		t.Fatalf("Rebuild failed: %v", err)
	}
	if res.ItemsReplayed != 3 {
		t.Errorf("ItemsReplayed = %d, want 3", res.ItemsReplayed)
	}

	if r := taskRow(t, s, "task-1"); r.found {
		t.Error("deleted task survived the rebuild")
	}
	if r := taskRow(t, s, "task-keep"); !r.found {
		t.Error("live task was lost in the rebuild")
	}

	d, err := s.Diagnose()
	if err != nil {
		t.Fatalf("Diagnose failed: %v", err)
	}
	if d.StaleDeletes != 0 {
		t.Errorf("StaleDeletes = %d after rebuild, want 0", d.StaleDeletes)
	}
}

func TestRebuildRepairsMissingCascade(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	process(t, s, 1, "proj-1", things.ItemKindTask7, things.ItemActionCreated, `{"tt":"Project","tp":1,"st":1}`)
	process(t, s, 2, "task-1", things.ItemKindTask7, things.ItemActionCreated,
		`{"tt":"Child","tp":0,"st":1,"pr":["proj-1"]}`)
	process(t, s, 3, "task-1", things.ItemKindTask7, things.ItemActionModified,
		`{"ss":3,"sp":1703738457.74,"md":1703738457.74}`)
	process(t, s, 4, "proj-1", things.ItemKindTask7, things.ItemActionModified, `{"tr":true}`)

	// Undo the cascade, reproducing the observed state: a completed child
	// left untrashed under a trashed project.
	if _, err := s.db.Exec(`UPDATE tasks SET in_trash = 0 WHERE uuid = 'task-1'`); err != nil {
		t.Fatalf("seeding corruption failed: %v", err)
	}
	d, err := s.Diagnose()
	if err != nil {
		t.Fatalf("Diagnose failed: %v", err)
	}
	if d.OrphanedChildren != 1 || d.OrphanedCompleted != 1 {
		t.Fatalf("setup: orphaned = %d (completed %d), want 1 / 1", d.OrphanedChildren, d.OrphanedCompleted)
	}

	if _, err := s.Rebuild(); err != nil {
		t.Fatalf("Rebuild failed: %v", err)
	}

	if r := taskRow(t, s, "task-1"); r.inTrash != 1 {
		t.Errorf("child in_trash = %d after rebuild, want 1", r.inTrash)
	}
	d, err = s.Diagnose()
	if err != nil {
		t.Fatalf("Diagnose failed: %v", err)
	}
	if d.OrphanedChildren != 0 {
		t.Errorf("OrphanedChildren = %d after rebuild, want 0", d.OrphanedChildren)
	}
}

func TestRebuildPreservesChangeLogAndCursor(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	process(t, s, 1, "task-1", things.ItemKindTask7, things.ItemActionCreated, `{"tt":"T","tp":0,"st":1}`)
	if err := s.saveSyncState("history-abc", 99); err != nil {
		t.Fatalf("saveSyncState failed: %v", err)
	}

	before := countRow(t, s, `SELECT COUNT(*) FROM change_log`)

	if _, err := s.Rebuild(); err != nil {
		t.Fatalf("Rebuild failed: %v", err)
	}

	if after := countRow(t, s, `SELECT COUNT(*) FROM change_log`); after != before {
		t.Errorf("change_log rows = %d after rebuild, want %d (the log is the source of truth)", after, before)
	}

	historyID, idx, err := s.readSyncer().getSyncState()
	if err != nil {
		t.Fatalf("getSyncState failed: %v", err)
	}
	if historyID != "history-abc" || idx != 99 {
		t.Errorf("sync cursor = (%q, %d), want (\"history-abc\", 99)", historyID, idx)
	}
}

// Rows written before schema version 5 carry no item_kind/item_action, so the
// envelope has to be inferred. This is the exact shape of the broken trash
// batch: e=Task7, p={}, logged as UnknownChange.
func TestRebuildInfersLegacyEnvelopes(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	insert := func(index int, changeType, entityType, uuid, payload string) {
		t.Helper()
		if _, err := s.db.Exec(`
			INSERT INTO change_log (server_index, synced_at, change_type, entity_type, entity_uuid, payload)
			VALUES (?, 1, ?, ?, ?, ?)
		`, index, changeType, entityType, uuid, payload); err != nil {
			t.Fatalf("insert legacy row failed: %v", err)
		}
	}

	insert(1, "TaskCreated", "Task", "task-1", `{"tt":"Doomed","tp":0,"st":1}`)
	insert(2, "TaskCreated", "Task", "task-2", `{"tt":"Keeper","tp":0,"st":1}`)
	// A pre-v5 create whose payload is also "{}" — proof that an empty
	// payload alone cannot be read as a deletion.
	insert(3, "TaskCreated", "Task", "task-3", `{}`)
	// The mislabelled deletion.
	insert(4, "UnknownChange", "Task7", "task-1", `{}`)

	res, err := s.Rebuild()
	if err != nil {
		t.Fatalf("Rebuild failed: %v", err)
	}
	if res.Inferred != 4 {
		t.Errorf("Inferred = %d, want 4", res.Inferred)
	}

	if r := taskRow(t, s, "task-1"); r.found {
		t.Error("legacy UnknownChange deletion was not applied")
	}
	if r := taskRow(t, s, "task-2"); !r.found {
		t.Error("task-2 should have been recreated by the replay")
	}
	if r := taskRow(t, s, "task-3"); !r.found {
		t.Error("an empty-payload TaskCreated must not be read as a deletion")
	}
}

func TestInferEnvelope(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		changeType string
		entityType string
		payload    string
		wantKind   things.ItemKind
		wantAction things.ItemAction
	}{
		{"task delete", "TaskDeleted", "Task", `{}`, things.ItemKindTask, things.ItemActionDeleted},
		{"project delete", "ProjectDeleted", "Project", `{}`, things.ItemKindTask, things.ItemActionDeleted},
		{"heading delete", "HeadingDeleted", "Heading", `{}`, things.ItemKindTask, things.ItemActionDeleted},
		{"area delete", "AreaDeleted", "Area", `{}`, things.ItemKindArea, things.ItemActionDeleted},
		{"tag delete", "TagDeleted", "Tag", `{}`, things.ItemKindTag, things.ItemActionDeleted},
		{"checklist delete", "ChecklistItemDeleted", "ChecklistItem", `{}`,
			things.ItemKindChecklistItem, things.ItemActionDeleted},
		// The mislabelled trash batch.
		{"unknown task7 empty payload", "UnknownChange", "Task7", `{}`,
			things.ItemKind("Task7"), things.ItemActionDeleted},
		// An empty payload on a named create is a create, not a delete.
		{"empty payload create", "TaskCreated", "Task", `{}`, things.ItemKindTask, things.ItemActionModified},
		{"normal update", "TaskCompleted", "Task", `{"ss":3}`, things.ItemKindTask, things.ItemActionModified},
		// An empty payload on a non-entity kind is not a deletion.
		{"command empty payload", "UnknownChange", "Command3", `{}`,
			things.ItemKind("Command3"), things.ItemActionModified},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, action := inferEnvelope(tc.changeType, tc.entityType, []byte(tc.payload))
			if kind != tc.wantKind {
				t.Errorf("kind = %q, want %q", kind, tc.wantKind)
			}
			if action != tc.wantAction {
				t.Errorf("action = %d, want %d", action, tc.wantAction)
			}
		})
	}
}

// A single item can produce several change_log rows; the replay must treat
// them as one item rather than applying the payload once per row.
func TestRebuildCollapsesRowsFromTheSameItem(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	process(t, s, 1, "task-1", things.ItemKindTask7, things.ItemActionCreated, `{"tt":"T","tp":0,"st":2}`)
	// This update yields both TaskCompleted and TaskMovedToAnytime.
	changes := process(t, s, 2, "task-1", things.ItemKindTask7, things.ItemActionModified,
		`{"ss":3,"md":1703738457.740577,"st":1,"sp":1703738457.7402868}`)
	if len(changes) < 2 {
		t.Fatalf("setup: expected multiple changes from one item, got %v", changeTypes(changes))
	}

	items, logRows, _, err := s.replayableItems()
	if err != nil {
		t.Fatalf("replayableItems failed: %v", err)
	}
	if logRows <= len(items) {
		t.Fatalf("setup: expected more log rows (%d) than items (%d)", logRows, len(items))
	}
	if len(items) != 2 {
		t.Errorf("replayable items = %d, want 2 (one per history slot)", len(items))
	}
}
