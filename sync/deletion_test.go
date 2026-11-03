package sync

import (
	"encoding/json"
	"path/filepath"
	"testing"

	things "github.com/arthursoares/things-cloud-sdk"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newTestSyncer(t *testing.T) *Syncer {
	t.Helper()
	syncer, err := Open(filepath.Join(t.TempDir(), "test.db"), nil)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { syncer.Close() })
	return syncer
}

// process feeds one raw Things Cloud item through the sync layer, using the
// JSON payload exactly as the server sends it.
func process(t *testing.T, s *Syncer, index int, uuid string, kind things.ItemKind, action things.ItemAction, payload string) []Change {
	t.Helper()
	idx := index
	changes, err := s.processItems([]things.Item{{
		UUID:        uuid,
		Kind:        kind,
		Action:      action,
		P:           json.RawMessage(payload),
		ServerIndex: &idx,
	}}, index)
	if err != nil {
		t.Fatalf("processItems(%s @ %d) failed: %v", uuid, index, err)
	}
	return changes
}

type row struct {
	found   bool
	deleted int
	inTrash int
	status  int
}

func taskRow(t *testing.T, s *Syncer, uuid string) row {
	t.Helper()
	var r row
	err := s.db.QueryRow(
		`SELECT deleted, in_trash, status FROM tasks WHERE uuid = ?`, uuid,
	).Scan(&r.deleted, &r.inTrash, &r.status)
	if err != nil {
		return row{}
	}
	r.found = true
	return r
}

func countRow(t *testing.T, s *Syncer, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	return n
}

// seedTask inserts a task directly, bypassing the item pipeline.
func seedTask(t *testing.T, s *Syncer, uuid string, taskType things.TaskType, project, heading, area string) {
	t.Helper()
	task := &things.Task{
		UUID:     uuid,
		Title:    uuid,
		Type:     taskType,
		Schedule: things.TaskScheduleAnytime,
	}
	if project != "" {
		task.ParentTaskIDs = []string{project}
	}
	if heading != "" {
		task.ActionGroupIDs = []string{heading}
	}
	if area != "" {
		task.AreaIDs = []string{area}
	}
	if err := s.saveTask(task); err != nil {
		t.Fatalf("seedTask(%s) failed: %v", uuid, err)
	}
}

func hasChange[T Change](changes []Change) bool {
	for _, c := range changes {
		if _, ok := c.(T); ok {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 1. entity kind classification — the root cause of the ignored deletions
// ---------------------------------------------------------------------------

func TestClassifyKind(t *testing.T) {
	t.Parallel()

	cases := []struct {
		kind string
		want entityFamily
	}{
		// The kind that regressed: Things bumped Task6 -> Task7 and every
		// item of the new kind fell through to UnknownChange.
		{"Task7", familyTask},
		{"Task6", familyTask},
		{"Task4", familyTask},
		{"Task3", familyTask},
		{"Task", familyTask},
		// Future version bumps must route without a code change.
		{"Task8", familyTask},
		{"Task12", familyTask},
		{"Area", familyArea},
		{"Area2", familyArea},
		{"Area3", familyArea},
		{"Tag", familyTag},
		{"Tag3", familyTag},
		{"Tag4", familyTag},
		{"ChecklistItem", familyChecklistItem},
		{"ChecklistItem3", familyChecklistItem},
		{"Tombstone2", familyTombstone},
		{"Settings3", familyIgnored},
		{"Settings4", familyIgnored},
		{"Settings5", familyIgnored},
		{"Command", familyIgnored},
		{"Command3", familyIgnored},
		// Genuinely unknown stays unknown.
		{"Widget9", familyUnknown},
		{"", familyUnknown},
	}

	for _, tc := range cases {
		if got := classifyKind(things.ItemKind(tc.kind)); got != tc.want {
			t.Errorf("classifyKind(%q) = %v, want %v", tc.kind, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. deletions with an empty payload
// ---------------------------------------------------------------------------

func TestTask7DeleteWithEmptyPayload(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	// A task with a tag and a checklist item, as emptying the trash would find it.
	process(t, s, 1, "task-1", things.ItemKindTask7, things.ItemActionCreated,
		`{"tt":"Doomed","tp":0,"st":1,"tg":["tag-1"]}`)
	process(t, s, 2, "check-1", things.ItemKindChecklistItem3, things.ItemActionCreated,
		`{"tt":"step","ts":["task-1"]}`)

	if n := countRow(t, s, `SELECT COUNT(*) FROM task_tags WHERE task_uuid = 'task-1'`); n != 1 {
		t.Fatalf("setup: expected 1 task_tags row, got %d", n)
	}
	if n := countRow(t, s, `SELECT COUNT(*) FROM checklist_items WHERE task_uuid = 'task-1' AND deleted = 0`); n != 1 {
		t.Fatalf("setup: expected 1 live checklist item, got %d", n)
	}

	// The delete Things Cloud actually sends: t=2, e=Task7, p={}.
	changes := process(t, s, 3, "task-1", things.ItemKindTask7, things.ItemActionDeleted, `{}`)

	if !hasChange[TaskDeleted](changes) {
		t.Fatalf("expected TaskDeleted, got %#v", changeTypes(changes))
	}
	if hasChange[UnknownChange](changes) {
		t.Errorf("empty-payload delete must not produce UnknownChange, got %v", changeTypes(changes))
	}

	if r := taskRow(t, s, "task-1"); r.deleted != 1 {
		t.Errorf("task row deleted = %d, want 1", r.deleted)
	}
	if n := countRow(t, s, `SELECT COUNT(*) FROM task_tags WHERE task_uuid = 'task-1'`); n != 0 {
		t.Errorf("task_tags not cascaded: %d rows remain", n)
	}
	if n := countRow(t, s, `SELECT COUNT(*) FROM checklist_items WHERE task_uuid = 'task-1' AND deleted = 0`); n != 0 {
		t.Errorf("checklist_items not cascaded: %d live rows remain", n)
	}
}

func TestDeleteOfUnseenTaskIsStillRecorded(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	// A deletion for a uuid we never materialized must not vanish silently,
	// or change_log stops being a complete account of the history.
	changes := process(t, s, 1, "never-synced", things.ItemKindTask7, things.ItemActionDeleted, `{}`)

	if !hasChange[TaskDeleted](changes) {
		t.Fatalf("expected TaskDeleted, got %v", changeTypes(changes))
	}
	if n := countRow(t, s, `SELECT COUNT(*) FROM change_log WHERE entity_uuid = 'never-synced'`); n != 1 {
		t.Errorf("expected the deletion to be logged once, got %d rows", n)
	}
}

func TestDeleteRecordsRawEnvelope(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	process(t, s, 1, "task-1", things.ItemKindTask7, things.ItemActionCreated, `{"tt":"T","tp":0}`)
	process(t, s, 2, "task-1", things.ItemKindTask7, things.ItemActionDeleted, `{}`)

	var kind string
	var action int
	if err := s.db.QueryRow(
		`SELECT item_kind, item_action FROM change_log WHERE server_index = 2`,
	).Scan(&kind, &action); err != nil {
		t.Fatalf("reading envelope failed: %v", err)
	}
	if kind != "Task7" || action != int(things.ItemActionDeleted) {
		t.Errorf("envelope = (%q, %d), want (\"Task7\", 2)", kind, action)
	}
}

// ---------------------------------------------------------------------------
// 3. container cascades
// ---------------------------------------------------------------------------

func TestProjectTrashedCascadesToChildren(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	// project -> heading -> task, plus a task directly under the project.
	process(t, s, 1, "proj-1", things.ItemKindTask7, things.ItemActionCreated, `{"tt":"Project","tp":1,"st":1}`)
	seedTask(t, s, "head-1", things.TaskTypeHeading, "proj-1", "", "")
	seedTask(t, s, "task-direct", things.TaskTypeTask, "proj-1", "", "")
	seedTask(t, s, "task-nested", things.TaskTypeTask, "proj-1", "head-1", "")
	seedTask(t, s, "task-elsewhere", things.TaskTypeTask, "", "", "")

	changes := process(t, s, 2, "proj-1", things.ItemKindTask7, things.ItemActionModified, `{"tr":true}`)
	if !hasChange[ProjectTrashed](changes) {
		t.Fatalf("expected ProjectTrashed, got %v", changeTypes(changes))
	}

	for _, uuid := range []string{"head-1", "task-direct", "task-nested"} {
		if r := taskRow(t, s, uuid); r.inTrash != 1 {
			t.Errorf("%s in_trash = %d, want 1 (project was trashed)", uuid, r.inTrash)
		}
	}
	if r := taskRow(t, s, "task-elsewhere"); r.inTrash != 0 {
		t.Errorf("task outside the project must not be trashed, in_trash = %d", r.inTrash)
	}
}

func TestProjectUntrashedUnCascades(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	process(t, s, 1, "proj-1", things.ItemKindTask7, things.ItemActionCreated, `{"tt":"Project","tp":1,"st":1}`)
	seedTask(t, s, "task-1", things.TaskTypeTask, "proj-1", "", "")

	process(t, s, 2, "proj-1", things.ItemKindTask7, things.ItemActionModified, `{"tr":true}`)
	if r := taskRow(t, s, "task-1"); r.inTrash != 1 {
		t.Fatalf("setup: child not trashed")
	}

	changes := process(t, s, 3, "proj-1", things.ItemKindTask7, things.ItemActionModified, `{"tr":false}`)
	if !hasChange[ProjectRestored](changes) {
		t.Fatalf("expected ProjectRestored, got %v", changeTypes(changes))
	}
	if r := taskRow(t, s, "task-1"); r.inTrash != 0 {
		t.Errorf("child in_trash = %d after restore, want 0", r.inTrash)
	}
}

func TestCompletedChildIsTrashedWithItsProject(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	// This is the observed case: tasks completed inside a project that was
	// later trashed kept status=3 and in_trash=0.
	process(t, s, 1, "proj-1", things.ItemKindTask7, things.ItemActionCreated, `{"tt":"Project","tp":1,"st":1}`)
	process(t, s, 2, "task-1", things.ItemKindTask7, things.ItemActionCreated,
		`{"tt":"Done thing","tp":0,"st":1,"pr":["proj-1"]}`)
	process(t, s, 3, "task-1", things.ItemKindTask7, things.ItemActionModified,
		`{"ss":3,"sp":1703738457.74,"md":1703738457.74}`)

	if r := taskRow(t, s, "task-1"); r.status != int(things.TaskStatusCompleted) {
		t.Fatalf("setup: child status = %d, want 3", r.status)
	}

	process(t, s, 4, "proj-1", things.ItemKindTask7, things.ItemActionModified, `{"tr":true}`)

	r := taskRow(t, s, "task-1")
	if r.inTrash != 1 {
		t.Errorf("completed child in_trash = %d, want 1", r.inTrash)
	}
	if r.status != int(things.TaskStatusCompleted) {
		t.Errorf("trashing must not change status, got %d", r.status)
	}
}

func TestTrashedHeadingCascadesToItsTasks(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	seedTask(t, s, "head-1", things.TaskTypeHeading, "proj-1", "", "")
	seedTask(t, s, "task-1", things.TaskTypeTask, "proj-1", "head-1", "")

	process(t, s, 1, "head-1", things.ItemKindTask7, things.ItemActionModified, `{"tr":true}`)

	if r := taskRow(t, s, "task-1"); r.inTrash != 1 {
		t.Errorf("task under trashed heading in_trash = %d, want 1", r.inTrash)
	}
}

func TestProjectDeletedCascadesToChildren(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	process(t, s, 1, "proj-1", things.ItemKindTask7, things.ItemActionCreated, `{"tt":"Project","tp":1,"st":1}`)
	seedTask(t, s, "head-1", things.TaskTypeHeading, "proj-1", "", "")
	seedTask(t, s, "task-1", things.TaskTypeTask, "proj-1", "head-1", "")

	changes := process(t, s, 2, "proj-1", things.ItemKindTask7, things.ItemActionDeleted, `{}`)
	if !hasChange[ProjectDeleted](changes) {
		t.Fatalf("expected ProjectDeleted, got %v", changeTypes(changes))
	}

	for _, uuid := range []string{"proj-1", "head-1", "task-1"} {
		if r := taskRow(t, s, uuid); r.deleted != 1 {
			t.Errorf("%s deleted = %d, want 1", uuid, r.deleted)
		}
	}
}

func TestHeadingDeletedCascadesToItsTasks(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	seedTask(t, s, "head-1", things.TaskTypeHeading, "proj-1", "", "")
	seedTask(t, s, "task-1", things.TaskTypeTask, "proj-1", "head-1", "")

	changes := process(t, s, 1, "head-1", things.ItemKindTask7, things.ItemActionDeleted, `{}`)
	if !hasChange[HeadingDeleted](changes) {
		t.Fatalf("expected HeadingDeleted, got %v", changeTypes(changes))
	}
	if r := taskRow(t, s, "task-1"); r.deleted != 1 {
		t.Errorf("task under deleted heading deleted = %d, want 1", r.deleted)
	}
}

func TestTaskDeletedCascadesToSubtasks(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	seedTask(t, s, "task-parent", things.TaskTypeTask, "", "", "")
	seedTask(t, s, "task-child", things.TaskTypeTask, "task-parent", "", "")

	process(t, s, 1, "task-parent", things.ItemKindTask7, things.ItemActionDeleted, `{}`)

	if r := taskRow(t, s, "task-child"); r.deleted != 1 {
		t.Errorf("subtask deleted = %d, want 1", r.deleted)
	}
}

func TestAreaDeletedUnfilesContentsByDefault(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	process(t, s, 1, "area-1", things.ItemKindArea3, things.ItemActionCreated, `{"tt":"Area"}`)
	seedTask(t, s, "proj-1", things.TaskTypeProject, "", "", "area-1")
	seedTask(t, s, "task-in-project", things.TaskTypeTask, "proj-1", "", "")
	seedTask(t, s, "task-in-area", things.TaskTypeTask, "", "", "area-1")
	seedTask(t, s, "task-elsewhere", things.TaskTypeTask, "", "", "area-2")

	changes := process(t, s, 2, "area-1", things.ItemKindArea3, things.ItemActionDeleted, `{}`)
	if !hasChange[AreaDeleted](changes) {
		t.Fatalf("expected AreaDeleted, got %v", changeTypes(changes))
	}

	// Things leaves an area's contents alone, so the rows survive and only
	// the reference to the vanished area is cleared.
	for _, uuid := range []string{"proj-1", "task-in-project", "task-in-area"} {
		r := taskRow(t, s, uuid)
		if !r.found || r.deleted != 0 {
			t.Errorf("%s should survive an area delete, found=%v deleted=%d", uuid, r.found, r.deleted)
		}
	}
	if n := countRow(t, s,
		`SELECT COUNT(*) FROM tasks WHERE uuid IN ('proj-1','task-in-area') AND area_uuid IS NULL`); n != 2 {
		t.Error("area reference should have been cleared on the area's direct children")
	}
	if r := taskRow(t, s, "task-elsewhere"); r.deleted != 0 {
		t.Errorf("task in another area must survive, deleted = %d", r.deleted)
	}
	if n := countRow(t, s,
		`SELECT COUNT(*) FROM tasks WHERE uuid = 'task-elsewhere' AND area_uuid = 'area-2'`); n != 1 {
		t.Error("another area's reference must not be touched")
	}
}

// Completing a project logs it but leaves its unfinished to-dos alone in
// Things, so the sync layer deliberately does not cascade status.
func TestProjectCompletedDoesNotCascadeStatus(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	process(t, s, 1, "proj-1", things.ItemKindTask7, things.ItemActionCreated, `{"tt":"Project","tp":1,"st":1}`)
	seedTask(t, s, "task-1", things.TaskTypeTask, "proj-1", "", "")

	changes := process(t, s, 2, "proj-1", things.ItemKindTask7, things.ItemActionModified,
		`{"ss":3,"sp":1703738457.74,"md":1703738457.74}`)
	if !hasChange[ProjectCompleted](changes) {
		t.Fatalf("expected ProjectCompleted, got %v", changeTypes(changes))
	}

	r := taskRow(t, s, "task-1")
	if r.status != int(things.TaskStatusPending) {
		t.Errorf("child status = %d, want 0 (completion does not cascade)", r.status)
	}
	if r.inTrash != 0 {
		t.Errorf("child in_trash = %d, want 0", r.inTrash)
	}
}

func changeTypes(changes []Change) []string {
	out := make([]string, 0, len(changes))
	for _, c := range changes {
		out = append(out, c.ChangeType())
	}
	return out
}

func TestAreaDeleteRemovesContentsMode(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)
	s.SetAreaDeleteMode(AreaDeleteRemovesContents)

	process(t, s, 1, "area-1", things.ItemKindArea3, things.ItemActionCreated, `{"tt":"Area"}`)
	seedTask(t, s, "proj-1", things.TaskTypeProject, "", "", "area-1")
	seedTask(t, s, "task-in-project", things.TaskTypeTask, "proj-1", "", "")

	process(t, s, 2, "area-1", things.ItemKindArea3, things.ItemActionDeleted, `{}`)

	for _, uuid := range []string{"proj-1", "task-in-project"} {
		if r := taskRow(t, s, uuid); r.deleted != 1 {
			t.Errorf("%s deleted = %d, want 1 in remove-contents mode", uuid, r.deleted)
		}
	}
}

// The transaction-scoped Syncer built by withDB must carry the configured
// mode, or the setting would be silently ignored during a real sync.
func TestAreaDeleteModePropagatesToTransaction(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)
	s.SetAreaDeleteMode(AreaDeleteRemovesContents)

	if got := s.withDB(s.rawDB).areaDeleteMode; got != AreaDeleteRemovesContents {
		t.Errorf("withDB lost areaDeleteMode: got %v", got)
	}
}
