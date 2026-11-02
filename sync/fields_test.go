package sync

import (
	"testing"

	things "github.com/arthursoares/things-cloud-sdk"
)

// Payload shapes in this file are the real ones observed in a Things Cloud
// history: partial updates that the old parser either dropped on the floor or
// filed under UnknownChange.

func TestPartialUpdateCompletion(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	process(t, s, 1, "task-1", things.ItemKindTask7, things.ItemActionCreated, `{"tt":"Thing","tp":0,"st":1}`)

	// {"ss":3,"sp":...,"md":...} — the shape a completion arrives in.
	changes := process(t, s, 2, "task-1", things.ItemKindTask7, things.ItemActionModified,
		`{"ss":3,"sp":1703738457.7402868,"md":1703738457.740577}`)

	if !hasChange[TaskCompleted](changes) {
		t.Fatalf("expected TaskCompleted, got %v", changeTypes(changes))
	}
	if hasChange[UnknownChange](changes) {
		t.Errorf("completion must not be UnknownChange, got %v", changeTypes(changes))
	}

	task, err := s.getTask("task-1")
	if err != nil || task == nil {
		t.Fatalf("getTask failed: %v", err)
	}
	if task.Status != things.TaskStatusCompleted {
		t.Errorf("status = %v, want completed", task.Status)
	}
	if task.CompletionDate == nil {
		t.Error("completion date was not applied")
	}
}

func TestPartialUpdateTodayIndex(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	process(t, s, 1, "task-1", things.ItemKindTask7, things.ItemActionCreated, `{"tt":"Thing","tp":0,"st":1}`)

	// {"ti":N} — a Today reorder. It changes the row but matches no named
	// semantic change, so it used to produce nothing at all.
	changes := process(t, s, 2, "task-1", things.ItemKindTask7, things.ItemActionModified, `{"ti":-1343}`)

	if len(changes) != 1 {
		t.Fatalf("expected exactly 1 change, got %v", changeTypes(changes))
	}
	updated, ok := changes[0].(TaskFieldsUpdated)
	if !ok {
		t.Fatalf("expected TaskFieldsUpdated, got %T", changes[0])
	}
	if len(updated.Fields) != 1 || updated.Fields[0] != "ti" {
		t.Errorf("Fields = %v, want [ti]", updated.Fields)
	}

	if n := countRow(t, s, `SELECT today_index FROM tasks WHERE uuid = 'task-1'`); n != -1343 {
		t.Errorf("today_index = %d, want -1343", n)
	}
}

func TestPartialUpdateInstanceCreationDate(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	process(t, s, 1, "task-1", things.ItemKindTask7, things.ItemActionCreated, `{"tt":"Repeater","tp":0,"st":1}`)

	// {"icsd":...} — a repeating task's next-instance date. This was parsed
	// but never written, so the update was a silent no-op.
	changes := process(t, s, 2, "task-1", things.ItemKindTask7, things.ItemActionModified, `{"icsd":1793664000}`)

	if len(changes) != 1 {
		t.Fatalf("expected exactly 1 change, got %v", changeTypes(changes))
	}
	updated, ok := changes[0].(TaskFieldsUpdated)
	if !ok {
		t.Fatalf("expected TaskFieldsUpdated, got %T", changes[0])
	}
	if len(updated.Fields) != 1 || updated.Fields[0] != "icsd" {
		t.Errorf("Fields = %v, want [icsd]", updated.Fields)
	}

	task, err := s.getTask("task-1")
	if err != nil || task == nil {
		t.Fatalf("getTask failed: %v", err)
	}
	if task.InstanceCreationStartDate == nil {
		t.Fatal("icsd was not persisted")
	}
	if got := task.InstanceCreationStartDate.Unix(); got != 1793664000 {
		t.Errorf("icsd = %d, want 1793664000", got)
	}
}

func TestFullUpsertWithTypeField(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	// A full-object upsert, as Things Cloud sends on create. "tp":1 makes it
	// a project; "rp" is a field this parser does not model.
	full := `{"acrd":null,"agr":[],"ar":["area-1"],"ato":null,"cd":1606502235.984004,` +
		`"dd":null,"dds":null,"dl":[],"do":0,"icc":0,"icp":false,"icsd":null,"ix":-2443,` +
		`"lai":null,"lt":false,"md":1608172476.0,"nt":"note text","pr":[],"rmd":null,` +
		`"rp":null,"rr":null,"rt":[],"sb":0,"sp":null,"sr":1606694400,"ss":0,"st":2,` +
		`"tg":[],"ti":-1343,"tir":1606694400,"tp":1,"tr":false,"tt":"A Project","xx":{}}`

	changes := process(t, s, 1, "proj-1", things.ItemKindTask7, things.ItemActionCreated, full)

	if !hasChange[ProjectCreated](changes) {
		t.Fatalf("expected ProjectCreated, got %v", changeTypes(changes))
	}
	// Every field in a real full upsert is either applied or a recognised
	// no-op, so nothing here is unknown.
	if hasChange[UnknownChange](changes) {
		t.Errorf("full upsert must not report unknown fields, got %v", changeTypes(changes))
	}

	task, err := s.getTask("proj-1")
	if err != nil || task == nil {
		t.Fatalf("getTask failed: %v", err)
	}
	if task.Type != things.TaskTypeProject {
		t.Errorf("type = %v, want project", task.Type)
	}
	if task.Title != "A Project" {
		t.Errorf("title = %q", task.Title)
	}
	if len(task.AreaIDs) != 1 || task.AreaIDs[0] != "area-1" {
		t.Errorf("AreaIDs = %v, want [area-1]", task.AreaIDs)
	}
}

func TestUnmodelledFieldIsReportedAsUnknown(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	process(t, s, 1, "task-1", things.ItemKindTask7, things.ItemActionCreated, `{"tt":"Thing","tp":0,"st":1}`)

	// A field no version of this parser knows. "Unknown" should mean exactly
	// this, and should name the field so the parser can be extended.
	changes := process(t, s, 2, "task-1", things.ItemKindTask7, things.ItemActionModified, `{"zzz":42}`)

	var unknown *UnknownChange
	for i := range changes {
		if u, ok := changes[i].(UnknownChange); ok {
			unknown = &u
		}
	}
	if unknown == nil {
		t.Fatalf("expected UnknownChange, got %v", changeTypes(changes))
	}
	if want := "unhandled task fields: zzz"; unknown.Details != want {
		t.Errorf("Details = %q, want %q", unknown.Details, want)
	}
}

func TestRecognisedNoOpFieldsProduceNoChange(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	process(t, s, 1, "task-1", things.ItemKindTask7, things.ItemActionCreated, `{"tt":"Thing","tp":0,"st":1}`)

	// Part of the wire format, deliberately not materialized: neither an
	// update we can show nor something unknown.
	changes := process(t, s, 2, "task-1", things.ItemKindTask7, things.ItemActionModified, `{"xx":{"sn":{}},"lt":false}`)

	if len(changes) != 0 {
		t.Errorf("expected no changes for ignored-only payload, got %v", changeTypes(changes))
	}
}

func TestCommandAndSettingsItemsAreIgnoredNotUnknown(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	// Mail-to-inbox command envelopes: their effect also arrives as a regular
	// Task item, so applying them would duplicate the task.
	for _, kind := range []things.ItemKind{
		things.ItemKindCommand, things.ItemKindCommand3,
		things.ItemKindSettings, things.ItemKindSettings4, things.ItemKindSettings5,
	} {
		changes := process(t, s, 1, "x-"+string(kind), kind, things.ItemActionCreated,
			`{"tp":1,"if":{"id":"abc","tt":"Forwarded mail"}}`)
		if len(changes) != 0 {
			t.Errorf("%s: expected no changes, got %v", kind, changeTypes(changes))
		}
	}

	if n := countRow(t, s, `SELECT COUNT(*) FROM tasks`); n != 0 {
		t.Errorf("command/settings items must not materialize tasks, got %d", n)
	}
}

func TestUnknownEntityKindStaysUnknown(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	changes := process(t, s, 1, "thing-1", things.ItemKind("Widget9"), things.ItemActionCreated, `{"a":1}`)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %v", changeTypes(changes))
	}
	if _, ok := changes[0].(UnknownChange); !ok {
		t.Fatalf("expected UnknownChange, got %T", changes[0])
	}
}

func TestUnparsablePayloadDoesNotAbortTheBatch(t *testing.T) {
	t.Parallel()
	s := newTestSyncer(t)

	// A payload we cannot read should be reported, not fatal: one bad item
	// must not strand the whole sync.
	changes := process(t, s, 1, "task-1", things.ItemKindTask7, things.ItemActionModified, `"not an object"`)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %v", changeTypes(changes))
	}
	if _, ok := changes[0].(UnknownChange); !ok {
		t.Fatalf("expected UnknownChange, got %T", changes[0])
	}
}

func TestClassifyTaskFields(t *testing.T) {
	t.Parallel()

	applied, unknown := classifyTaskFields(payloadFields([]byte(`{"ss":3,"sp":1,"md":2,"xx":{},"zzz":1}`)))

	wantApplied := []string{"md", "sp", "ss"} // sorted
	if len(applied) != len(wantApplied) {
		t.Fatalf("applied = %v, want %v", applied, wantApplied)
	}
	for i := range wantApplied {
		if applied[i] != wantApplied[i] {
			t.Fatalf("applied = %v, want %v", applied, wantApplied)
		}
	}
	if len(unknown) != 1 || unknown[0] != "zzz" {
		t.Errorf("unknown = %v, want [zzz]", unknown)
	}
}
