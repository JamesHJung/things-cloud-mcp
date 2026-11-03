package sync

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	things "github.com/JamesHJung/things-cloud-mcp"
)

// Note: database/sql types are used via the dbExecutor interface defined in sync.go

// processItems processes a batch of Things Cloud items into semantic changes.
// The baseIndex is the starting server index for this batch.
func (s *Syncer) processItems(items []things.Item, baseIndex int) ([]Change, error) {
	if len(items) == 0 {
		return nil, nil
	}

	// Wrap entire batch in a transaction for massive performance improvement
	tx, err := s.rawDB.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() // No-op if committed

	txSyncer := s.withDB(tx)

	var allChanges []Change

	for i, item := range items {
		serverIndex := baseIndex + i
		if item.ServerIndex != nil {
			serverIndex = *item.ServerIndex
		}
		ts := time.Now()

		changes, err := txSyncer.processItem(item, serverIndex, ts)
		if err != nil {
			return nil, fmt.Errorf("processing item %s: %w", item.UUID, err)
		}

		// Log each change
		for _, change := range changes {
			payload, _ := json.Marshal(item.P)
			if err := txSyncer.logChange(serverIndex, change, string(payload), item.Kind, item.Action); err != nil {
				return nil, fmt.Errorf("logging change: %w", err)
			}
		}

		allChanges = append(allChanges, changes...)
	}

	// Persist the cursor in the same transaction as the batch it covers, so
	// the two can never disagree: either both land or neither does. A resume
	// then continues after the last committed batch instead of replaying it.
	// Replaying matters beyond duplicate change_log rows — note updates can
	// arrive as delta patches, and applying one twice corrupts the note.
	if s.history != nil {
		if err := txSyncer.saveSyncState(s.history.ID, s.history.LoadedServerIndex); err != nil {
			return nil, fmt.Errorf("saving sync state: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return allChanges, nil
}

// processItem routes an item to the correct handler based on its Kind.
//
// Routing goes through classifyKind rather than an explicit kind list: Things
// Cloud bumps the version suffix on entity names between app releases, and
// matching exact names meant a new "Task7" kind silently became UnknownChange.
func (s *Syncer) processItem(item things.Item, serverIndex int, ts time.Time) ([]Change, error) {
	switch classifyKind(item.Kind) {
	case familyTask:
		return s.processTaskItem(item, serverIndex, ts)
	case familyArea:
		return s.processAreaItem(item, serverIndex, ts)
	case familyTag:
		return s.processTagItem(item, serverIndex, ts)
	case familyChecklistItem:
		return s.processChecklistItem(item, serverIndex, ts)
	case familyTombstone:
		return s.processTombstone(item, serverIndex, ts)
	case familyIgnored:
		// Settings, and the mail-to-inbox "Command" envelopes whose effect
		// also arrives as a regular Task item. Recognised, not materialised.
		return nil, nil
	default:
		// Genuinely unrecognised entity kind.
		return []Change{UnknownChange{
			baseChange: baseChange{serverIndex: serverIndex, timestamp: ts},
			entityType: string(item.Kind),
			entityUUID: item.UUID,
			Details:    fmt.Sprintf("unknown item kind: %s", item.Kind),
		}}, nil
	}
}

// isEmptyPayload reports whether an item carries no fields at all. Things
// Cloud sends "{}" (or nothing) as the payload of a deletion.
func isEmptyPayload(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "{}" || trimmed == "null"
}

// processTaskItem handles task/project/heading items.
func (s *Syncer) processTaskItem(item things.Item, serverIndex int, ts time.Time) ([]Change, error) {
	// Get the old state
	old, err := s.getTask(item.UUID)
	if err != nil {
		return nil, fmt.Errorf("getting task %s: %w", item.UUID, err)
	}

	base := baseChange{serverIndex: serverIndex, timestamp: ts}

	// Handle deletion. Things Cloud sends these as t=2 with an empty payload;
	// the entity's own type is only knowable from the row we already hold.
	if item.Action == things.ItemActionDeleted {
		// Cascade before the parent row disappears. Children reference the
		// parent by project_uuid/heading_uuid, so this is safe in either
		// order, but doing it first keeps the intent obvious.
		if _, err := s.cascadeDelete(item.UUID); err != nil {
			return nil, err
		}
		if old != nil {
			if err := s.markTaskDeleted(item.UUID); err != nil {
				return nil, fmt.Errorf("marking task deleted: %w", err)
			}
			return detectTaskChanges(old, nil, serverIndex, ts), nil
		}
		// The row was never synced (or was already purged). Still record the
		// deletion so change_log stays a complete account of the history.
		return []Change{TaskDeleted{taskChange: taskChange{
			baseChange: base,
			Task:       &things.Task{UUID: item.UUID},
		}}}, nil
	}

	// Unmarshal the payload. A payload we cannot parse is genuinely unknown —
	// report it rather than aborting the whole sync batch.
	var payload things.TaskActionItemPayload
	if err := json.Unmarshal(item.P, &payload); err != nil {
		return []Change{UnknownChange{
			baseChange: base,
			entityType: string(item.Kind),
			entityUUID: item.UUID,
			Details:    fmt.Sprintf("unparsable task payload: %v", err),
		}}, nil
	}

	carried := payloadFields(item.P)
	applied, unknown := classifyTaskFields(carried)

	// Apply payload to build new state
	newTask := applyTaskPayload(old, item.UUID, payload)

	// Save the new state
	if err := s.saveTask(newTask); err != nil {
		return nil, fmt.Errorf("saving task: %w", err)
	}

	// Detect and return changes
	changes := detectTaskChanges(old, newTask, serverIndex, ts)
	assignmentChanges, err := s.detectTaskAssignmentChanges(old, newTask, serverIndex, ts)
	if err != nil {
		return nil, fmt.Errorf("detecting task assignments: %w", err)
	}
	changes = append(changes, assignmentChanges...)

	// An update that carried fields we applied but that matched no named
	// semantic change used to vanish entirely (partial updates such as
	// {"ti":N} or {"icsd":...}). Record it so the write is still visible.
	if len(changes) == 0 && len(applied) > 0 {
		changes = append(changes, TaskFieldsUpdated{
			taskChange: taskChange{baseChange: base, Task: newTask},
			Fields:     applied,
		})
	}

	// Fields the wire format carries that this parser does not model at all.
	// Surfacing them is what makes "UnknownChange" actionable.
	if len(unknown) > 0 {
		changes = append(changes, UnknownChange{
			baseChange: base,
			entityType: string(item.Kind),
			entityUUID: item.UUID,
			Details:    "unhandled task fields: " + strings.Join(unknown, ","),
		})
	}

	if err := s.applyTaskCascades(item.UUID, changes); err != nil {
		return nil, err
	}

	return changes, nil
}

// applyTaskCascades re-derives the child effects of a container change.
//
// Things propagates these in its UI but Things Cloud only sends the item for
// the container, so without this a project's to-dos stay live under a trashed
// or deleted parent.
func (s *Syncer) applyTaskCascades(uuid string, changes []Change) error {
	for _, c := range changes {
		switch c.(type) {
		// Trashing a project or heading trashes everything inside it.
		case ProjectTrashed, TaskTrashed:
			if _, err := s.cascadeTrash(uuid, true); err != nil {
				return err
			}
		// ...and restoring it brings them back.
		case ProjectRestored, TaskRestored:
			if _, err := s.cascadeTrash(uuid, false); err != nil {
				return err
			}
		case ProjectDeleted, HeadingDeleted, TaskDeleted:
			if _, err := s.cascadeDelete(uuid); err != nil {
				return err
			}
		}
	}
	return nil
}

// processAreaItem handles area items.
func (s *Syncer) processAreaItem(item things.Item, serverIndex int, ts time.Time) ([]Change, error) {
	// Get the old state
	old, err := s.getArea(item.UUID)
	if err != nil {
		return nil, fmt.Errorf("getting area %s: %w", item.UUID, err)
	}

	// Handle deletion. Things Cloud sends no items for the area's contents, so
	// their fate is decided locally: by default the rows are kept and their
	// now-dangling area reference is cleared. See AreaDeleteMode.
	if item.Action == things.ItemActionDeleted {
		if _, err := s.cascadeAreaDelete(item.UUID); err != nil {
			return nil, err
		}
		if old != nil {
			if err := s.markAreaDeleted(item.UUID); err != nil {
				return nil, fmt.Errorf("marking area deleted: %w", err)
			}
		}
		return detectAreaChanges(old, nil, serverIndex, ts), nil
	}

	// Unmarshal the payload
	var payload things.AreaActionItemPayload
	if err := json.Unmarshal(item.P, &payload); err != nil {
		return nil, fmt.Errorf("unmarshaling area payload: %w", err)
	}

	// Build new state from old or create new
	newArea := &things.Area{UUID: item.UUID}
	if old != nil {
		// Copy old state
		newArea.Title = old.Title
	}

	// Apply payload fields
	if payload.Title != nil {
		newArea.Title = *payload.Title
	}

	// Save the new state
	if err := s.saveArea(newArea); err != nil {
		return nil, fmt.Errorf("saving area: %w", err)
	}

	// Detect and return changes
	return detectAreaChanges(old, newArea, serverIndex, ts), nil
}

// processTagItem handles tag items.
func (s *Syncer) processTagItem(item things.Item, serverIndex int, ts time.Time) ([]Change, error) {
	// Get the old state
	old, err := s.getTag(item.UUID)
	if err != nil {
		return nil, fmt.Errorf("getting tag %s: %w", item.UUID, err)
	}

	// Handle deletion
	if item.Action == things.ItemActionDeleted {
		if old != nil {
			if err := s.markTagDeleted(item.UUID); err != nil {
				return nil, fmt.Errorf("marking tag deleted: %w", err)
			}
		}
		return detectTagChanges(old, nil, serverIndex, ts), nil
	}

	// Unmarshal the payload
	var payload things.TagActionItemPayload
	if err := json.Unmarshal(item.P, &payload); err != nil {
		return nil, fmt.Errorf("unmarshaling tag payload: %w", err)
	}

	// Build new state from old or create new
	newTag := &things.Tag{UUID: item.UUID}
	if old != nil {
		// Copy old state
		newTag.Title = old.Title
		newTag.ShortHand = old.ShortHand
		newTag.ParentTagIDs = old.ParentTagIDs
	}

	// Apply payload fields
	if payload.Title != nil {
		newTag.Title = *payload.Title
	}
	if payload.ShortHand != nil {
		newTag.ShortHand = *payload.ShortHand
	}
	if payload.ParentTagIDs != nil {
		newTag.ParentTagIDs = *payload.ParentTagIDs
	}

	// Save the new state
	if err := s.saveTag(newTag); err != nil {
		return nil, fmt.Errorf("saving tag: %w", err)
	}

	// Detect and return changes
	return detectTagChanges(old, newTag, serverIndex, ts), nil
}

// processChecklistItem handles checklist items.
func (s *Syncer) processChecklistItem(item things.Item, serverIndex int, ts time.Time) ([]Change, error) {
	// Get the old state
	old, err := s.getChecklistItem(item.UUID)
	if err != nil {
		return nil, fmt.Errorf("getting checklist item %s: %w", item.UUID, err)
	}

	// Handle deletion
	if item.Action == things.ItemActionDeleted {
		if old != nil {
			if err := s.markChecklistItemDeleted(item.UUID); err != nil {
				return nil, fmt.Errorf("marking checklist item deleted: %w", err)
			}
		}
		return detectChecklistChanges(old, nil, nil, serverIndex, ts), nil
	}

	// Unmarshal the payload
	var payload things.CheckListActionItemPayload
	if err := json.Unmarshal(item.P, &payload); err != nil {
		return nil, fmt.Errorf("unmarshaling checklist payload: %w", err)
	}

	// Build new state from old or create new
	newItem := &things.CheckListItem{UUID: item.UUID}
	if old != nil {
		// Copy old state
		newItem.Title = old.Title
		newItem.Status = old.Status
		newItem.Index = old.Index
		newItem.CreationDate = old.CreationDate
		newItem.ModificationDate = old.ModificationDate
		newItem.CompletionDate = old.CompletionDate
		newItem.TaskIDs = old.TaskIDs
	}

	// Apply payload fields
	if payload.Title != nil {
		newItem.Title = *payload.Title
	}
	if payload.Status != nil {
		newItem.Status = *payload.Status
	}
	if payload.Index != nil {
		newItem.Index = *payload.Index
	}
	if payload.CreationDate != nil {
		newItem.CreationDate = *payload.CreationDate.Time()
	}
	if payload.ModificationDate != nil {
		newItem.ModificationDate = payload.ModificationDate.Time()
	}
	if payload.CompletionDate != nil {
		newItem.CompletionDate = payload.CompletionDate.Time()
	}
	if payload.TaskIDs != nil {
		newItem.TaskIDs = *payload.TaskIDs
	}

	// Get the parent task for context in changes (if available)
	var task *things.Task
	if len(newItem.TaskIDs) > 0 {
		task, _ = s.getTask(newItem.TaskIDs[0])
	}

	// Save the new state
	if err := s.saveChecklistItem(newItem); err != nil {
		return nil, fmt.Errorf("saving checklist item: %w", err)
	}

	// Detect and return changes
	return detectChecklistChanges(old, newItem, task, serverIndex, ts), nil
}

// processTombstone handles tombstone deletion records.
// Tombstones are used for permanent deletions in Things Cloud.
func (s *Syncer) processTombstone(item things.Item, serverIndex int, ts time.Time) ([]Change, error) {
	// Unmarshal the tombstone payload
	var payload things.TombstoneActionItemPayload
	if err := json.Unmarshal(item.P, &payload); err != nil {
		return nil, fmt.Errorf("unmarshaling tombstone payload: %w", err)
	}

	deletedUUID := payload.DeletedObjectID
	var changes []Change

	// Try to find and delete the object in each table
	// Check if it's a task
	if task, _ := s.getTask(deletedUUID); task != nil {
		if _, err := s.cascadeDelete(deletedUUID); err != nil {
			return nil, err
		}
		if err := s.markTaskDeleted(deletedUUID); err != nil {
			return nil, fmt.Errorf("marking task deleted via tombstone: %w", err)
		}
		changes = append(changes, detectTaskChanges(task, nil, serverIndex, ts)...)
		return changes, nil
	}

	// Check if it's an area
	if area, _ := s.getArea(deletedUUID); area != nil {
		if _, err := s.cascadeAreaDelete(deletedUUID); err != nil {
			return nil, err
		}
		if err := s.markAreaDeleted(deletedUUID); err != nil {
			return nil, fmt.Errorf("marking area deleted via tombstone: %w", err)
		}
		changes = append(changes, detectAreaChanges(area, nil, serverIndex, ts)...)
		return changes, nil
	}

	// Check if it's a tag
	if tag, _ := s.getTag(deletedUUID); tag != nil {
		if err := s.markTagDeleted(deletedUUID); err != nil {
			return nil, fmt.Errorf("marking tag deleted via tombstone: %w", err)
		}
		changes = append(changes, detectTagChanges(tag, nil, serverIndex, ts)...)
		return changes, nil
	}

	// Check if it's a checklist item
	if checklistItem, _ := s.getChecklistItem(deletedUUID); checklistItem != nil {
		if err := s.markChecklistItemDeleted(deletedUUID); err != nil {
			return nil, fmt.Errorf("marking checklist item deleted via tombstone: %w", err)
		}
		changes = append(changes, detectChecklistChanges(checklistItem, nil, nil, serverIndex, ts)...)
		return changes, nil
	}

	// Object not found - might have been deleted already or never synced
	return nil, nil
}

// applyTaskPayload applies a task payload to an existing task state (or creates a new one).
func applyTaskPayload(old *things.Task, uuid string, p things.TaskActionItemPayload) *things.Task {
	// Start with old state or create new with defaults
	t := &things.Task{
		UUID:     uuid,
		Schedule: things.TaskScheduleAnytime, // Default schedule for new tasks
	}

	if old != nil {
		// Copy all fields from old state
		t.CreationDate = old.CreationDate
		t.ModificationDate = old.ModificationDate
		t.Status = old.Status
		t.Title = old.Title
		t.Note = old.Note
		t.ScheduledDate = old.ScheduledDate
		t.CompletionDate = old.CompletionDate
		t.DeadlineDate = old.DeadlineDate
		t.Index = old.Index
		t.AreaIDs = old.AreaIDs
		t.ParentTaskIDs = old.ParentTaskIDs
		t.ActionGroupIDs = old.ActionGroupIDs
		t.InTrash = old.InTrash
		t.Schedule = old.Schedule
		t.Type = old.Type
		t.TodayIndex = old.TodayIndex
		t.TodayIndexReference = old.TodayIndexReference
		t.DueOrder = old.DueOrder
		t.AlarmTimeOffset = old.AlarmTimeOffset
		t.TagIDs = old.TagIDs
		t.RecurrenceIDs = old.RecurrenceIDs
		t.DelegateIDs = old.DelegateIDs
		t.InstanceCreationStartDate = old.InstanceCreationStartDate
	}

	// Apply each non-nil field from payload
	if p.CreationDate != nil {
		t.CreationDate = *p.CreationDate.Time()
	}
	if p.ModificationDate != nil {
		t.ModificationDate = p.ModificationDate.Time()
	}
	if p.Status != nil {
		t.Status = *p.Status
	}
	if p.Title != nil {
		t.Title = *p.Title
	}
	if p.HasScheduledDate() {
		t.ScheduledDate = nil
		if p.ScheduledDate != nil {
			t.ScheduledDate = p.ScheduledDate.Time()
		}
	}
	if p.HasTaskIR() {
		// TaskIR (tir) is the today-index-reference date; when set to today,
		// the task appears in the Today view regardless of sr value.
		t.TodayIndexReference = nil
		if p.TaskIR != nil {
			t.TodayIndexReference = p.TaskIR.Time()
		}
	}
	if (p.HasScheduledDate() || p.Schedule != nil) && !p.HasTaskIR() {
		// A reschedule without tir means the task no longer carries an
		// explicit Today placement from the previous state.
		t.TodayIndexReference = nil
	}
	if p.HasCompletionDate() {
		t.CompletionDate = nil
		if p.CompletionDate != nil {
			t.CompletionDate = p.CompletionDate.Time()
		}
	}
	if p.HasDeadlineDate() {
		t.DeadlineDate = nil
		if p.DeadlineDate != nil {
			t.DeadlineDate = p.DeadlineDate.Time()
		}
	}
	if p.Index != nil {
		t.Index = *p.Index
	}
	if p.AreaIDs != nil {
		t.AreaIDs = *p.AreaIDs
	}
	if p.ParentTaskIDs != nil {
		t.ParentTaskIDs = *p.ParentTaskIDs
	}
	if p.ActionGroupIDs != nil {
		t.ActionGroupIDs = *p.ActionGroupIDs
	}
	if p.InTrash != nil {
		t.InTrash = *p.InTrash
	}
	if p.Schedule != nil {
		t.Schedule = *p.Schedule
	}
	if p.Type != nil {
		t.Type = *p.Type
	}
	if p.TaskIndex != nil {
		t.TodayIndex = *p.TaskIndex
	}
	if p.DueOrder != nil {
		t.DueOrder = *p.DueOrder
	}
	if p.AlarmTimeOffset != nil {
		t.AlarmTimeOffset = p.AlarmTimeOffset
	}
	if p.TagIDs != nil {
		t.TagIDs = p.TagIDs
	}
	if p.RecurrenceTaskIDs != nil {
		t.RecurrenceIDs = *p.RecurrenceTaskIDs
	}
	if p.DelegateIDs != nil {
		t.DelegateIDs = *p.DelegateIDs
	}
	if p.InstanceCreationStartDate != nil {
		// icsd: next-instance date of a repeating task. Previously parsed but
		// never written, so a repeating series' only update was a no-op.
		t.InstanceCreationStartDate = p.InstanceCreationStartDate.Time()
	}

	// Handle Note specially: can be string or Note struct with patches
	if len(p.Note) > 0 {
		t.Note = parseNotePayload(t.Note, p.Note)
	}

	return t
}

func (s *Syncer) detectTaskAssignmentChanges(old, new *things.Task, serverIndex int, ts time.Time) ([]Change, error) {
	if old == nil || new == nil || new.Type != things.TaskTypeTask {
		return nil, nil
	}

	base := baseChange{serverIndex: serverIndex, timestamp: ts}
	tc := taskChange{baseChange: base, Task: new}
	var changes []Change

	oldParentID := firstID(old.ParentTaskIDs)
	newParentID := firstID(new.ParentTaskIDs)
	if oldParentID != newParentID {
		oldProject, err := s.lookupProjectContainer(oldParentID)
		if err != nil {
			return nil, err
		}
		newProject, err := s.lookupProjectContainer(newParentID)
		if err != nil {
			return nil, err
		}
		if oldProject != nil || newProject != nil {
			changes = append(changes, TaskAssignedToProject{
				taskChange: tc,
				Project:    newProject,
				OldProject: oldProject,
			})
		}
	}

	oldAreaID := firstID(old.AreaIDs)
	newAreaID := firstID(new.AreaIDs)
	if oldAreaID != newAreaID {
		oldArea, err := s.lookupArea(oldAreaID)
		if err != nil {
			return nil, err
		}
		newArea, err := s.lookupArea(newAreaID)
		if err != nil {
			return nil, err
		}
		if oldArea != nil || newArea != nil {
			changes = append(changes, TaskAssignedToArea{
				taskChange: tc,
				Area:       newArea,
				OldArea:    oldArea,
			})
		}
	}

	return changes, nil
}

func firstID(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func (s *Syncer) lookupProjectContainer(uuid string) (*things.Task, error) {
	if uuid == "" {
		return nil, nil
	}
	task, err := s.getTask(uuid)
	if err != nil {
		return nil, fmt.Errorf("getting project %s: %w", uuid, err)
	}
	if task == nil || task.Type != things.TaskTypeProject {
		return nil, nil
	}
	return task, nil
}

func (s *Syncer) lookupArea(uuid string) (*things.Area, error) {
	if uuid == "" {
		return nil, nil
	}
	area, err := s.getArea(uuid)
	if err != nil {
		return nil, fmt.Errorf("getting area %s: %w", uuid, err)
	}
	return area, nil
}

// parseNotePayload parses the note field from a task payload.
// The note can be either a plain string or a structured Note object with patches.
func parseNotePayload(currentNote string, raw json.RawMessage) string {
	// First, try to unmarshal as a string (most common case)
	var noteStr string
	if err := json.Unmarshal(raw, &noteStr); err == nil {
		return noteStr
	}

	// Try to unmarshal as a structured Note
	var note things.Note
	if err := json.Unmarshal(raw, &note); err != nil {
		// If both fail, return the current note unchanged
		return currentNote
	}

	// Handle based on note type
	switch note.Type {
	case things.NoteTypeFullText:
		// Full text replacement
		return note.Value
	case things.NoteTypeDelta:
		// Apply patches to current note
		return things.ApplyPatches(currentNote, note.Patches)
	default:
		// Unknown type, return value if present
		if note.Value != "" {
			return note.Value
		}
		return currentNote
	}
}
