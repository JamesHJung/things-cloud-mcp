package sync

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	things "github.com/JamesHJung/things-cloud-mcp"
)

// RebuildResult summarises a rebuild.
type RebuildResult struct {
	// ItemsReplayed is the number of distinct Things Cloud items replayed.
	ItemsReplayed int
	// LogRows is the number of change_log rows those items were recovered from.
	LogRows int
	// Inferred is the number of items whose envelope had to be inferred
	// because they were logged before schema version 5 recorded it.
	Inferred int
	// Tasks, Projects, Headings, Areas, Tags, ChecklistItems are the live row
	// counts after the replay.
	Tasks, Projects, Headings, Areas, Tags, ChecklistItems int
}

// Rebuild drops the materialized tables and replays change_log from server
// index 0, rebuilding local state from the payloads already on disk.
//
// This repairs a database written by an older version of the sync layer — one
// that dropped "Task7" deletions into the UnknownChange bucket and never
// cascaded container changes to children — without re-pulling the history
// from Things Cloud. It needs no network access and no credentials.
//
// change_log and sync_state are left untouched, so the sync cursor survives
// and the next Sync() picks up where it left off. Cascades are re-derived from
// the replayed items rather than read back out of the log, because the log
// records only the items Things Cloud actually sent.
func (s *Syncer) Rebuild() (RebuildResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var res RebuildResult

	items, rows, inferred, err := s.replayableItems()
	if err != nil {
		return res, err
	}
	res.LogRows = rows
	res.Inferred = inferred
	res.ItemsReplayed = len(items)

	tx, err := s.rawDB.Begin()
	if err != nil {
		return res, fmt.Errorf("beginning rebuild transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // No-op once committed

	txSyncer := s.withDB(tx)

	// Drop the materialized state only. change_log is the source of truth
	// here, and sync_state carries the cursor we must not lose.
	for _, table := range []string{"task_tags", "area_tags", "checklist_items", "tasks", "areas", "tags"} {
		if _, err := tx.Exec(`DELETE FROM ` + table); err != nil {
			return res, fmt.Errorf("clearing %s: %w", table, err)
		}
	}

	// Replay in history order. processItem is reused verbatim so a rebuild and
	// a fresh sync cannot drift apart; the resulting changes are discarded
	// rather than re-logged, since change_log already holds them.
	ts := time.Now()
	for _, it := range items {
		if _, err := txSyncer.processItem(it.item, it.serverIndex, ts); err != nil {
			return res, fmt.Errorf("replaying item %s at index %d: %w", it.item.UUID, it.serverIndex, err)
		}
	}

	if err := txSyncer.purgeDeleted(); err != nil {
		return res, fmt.Errorf("purging deleted rows: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return res, fmt.Errorf("committing rebuild: %w", err)
	}

	if err := s.readSyncer().countRows(&res); err != nil {
		return res, err
	}

	return res, nil
}

// sqlIsDelete is the SQL counterpart of inferEnvelope's action inference, for
// use on a change_log row aliased as "c".
//
// Rows written at schema version 5 or later carry item_action directly. Older
// rows have to be inferred, and change_type is what makes that possible: an
// empty "{}" payload means a deletion on an UnknownChange row (the mislabelled
// trash-emptying batch) but *not* on a TaskCreated row, which also occurs.
const sqlIsDelete = `(
	c.item_action = 2
	OR (c.item_action IS NULL AND (
		c.change_type IN ('TaskDeleted', 'ProjectDeleted', 'HeadingDeleted')
		OR (
			c.change_type = 'UnknownChange'
			AND COALESCE(TRIM(c.payload), '{}') IN ('{}', '', 'null')
			-- Conservative on purpose: task kinds are the only ones that ever
			-- reached the Unknown bucket, and Command/Settings payloads are
			-- also empty without being deletions.
			AND COALESCE(NULLIF(c.item_kind, ''), c.entity_type) LIKE 'Task%'
		)
	))
)`

type replayItem struct {
	serverIndex int
	item        things.Item
}

// replayableItems reconstructs the Things Cloud items recorded in change_log.
//
// A single item can produce several change_log rows (one per detected semantic
// change), all carrying the same payload. Rows are therefore collapsed on
// (server_index, entity_uuid), which uniquely identifies a source item: one
// history slot holds at most one entry per UUID.
func (s *Syncer) replayableItems() (items []replayItem, logRows int, inferred int, err error) {
	rows, err := s.rawDB.Query(`
		SELECT server_index, change_type, entity_type, entity_uuid, payload, item_kind, item_action
		FROM change_log
		ORDER BY server_index, id
	`)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("reading change_log: %w", err)
	}
	defer rows.Close()

	type key struct {
		index int
		uuid  string
	}
	seen := make(map[key]bool)

	for rows.Next() {
		var (
			serverIndex                        int
			changeType, entityType, entityUUID string
			payload                            sql.NullString
			itemKind                           sql.NullString
			itemAction                         sql.NullInt64
		)
		if err := rows.Scan(&serverIndex, &changeType, &entityType, &entityUUID,
			&payload, &itemKind, &itemAction); err != nil {
			return nil, 0, 0, err
		}
		logRows++

		k := key{serverIndex, entityUUID}
		if seen[k] {
			continue
		}
		seen[k] = true

		raw := json.RawMessage("{}")
		if payload.Valid && strings.TrimSpace(payload.String) != "" {
			raw = json.RawMessage(payload.String)
		}

		kind := things.ItemKind(itemKind.String)
		action := things.ItemAction(itemAction.Int64)
		if !itemKind.Valid || !itemAction.Valid {
			// Logged before migration5 started recording the envelope.
			kind, action = inferEnvelope(changeType, entityType, entityUUID, raw)
			inferred++
		}

		items = append(items, replayItem{
			serverIndex: serverIndex,
			item: things.Item{
				UUID:        entityUUID,
				P:           raw,
				Kind:        kind,
				Action:      action,
				ServerIndex: &serverIndex,
			},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, err
	}

	return items, logRows, inferred, nil
}

// inferEnvelope reconstructs the (kind, action) of a pre-migration5 log row.
//
// entity_type on those rows is the *change's* entity ("Task", "Project",
// "Heading", ...) for recognised changes, but the raw Things Cloud kind
// ("Task7", "Command3", ...) for UnknownChange rows, so both spellings have
// to be handled.
//
// The action is recovered from change_type, which is reliable where the
// payload alone is not: an empty "{}" payload means a deletion for the Task7
// rows that triggered this work, but the same empty payload also appears on
// genuine TaskCreated rows.
func inferEnvelope(changeType, entityType, entityUUID string, payload json.RawMessage) (things.ItemKind, things.ItemAction) {
	// Whether the item was a legacy kind decides whether its identifiers get
	// derived, so the inferred kind has to get that right. entity_type cannot
	// tell us — it records the semantic type ("Task", "Area"), not the raw
	// kind — but the identifier shape can: only a pre-migration item keys its
	// object by a hyphenated UUID.
	legacy := things.IsLegacyIdentifier(entityUUID)

	kind := things.ItemKind(entityType)
	switch entityType {
	case "Task", "Project", "Heading":
		kind = things.ItemKindTask
		if legacy {
			kind = things.ItemKindTask4
		}
	case "ChecklistItem", "CheckListItem":
		kind = things.ItemKindChecklistItem3
		if legacy {
			kind = things.ItemKindChecklistItem
		}
	case "Area":
		kind = things.ItemKindArea3
		if legacy {
			kind = things.ItemKindArea
		}
	case "Tag":
		kind = things.ItemKindTag4
		if legacy {
			kind = things.ItemKindTag
		}
	}

	action := things.ItemActionModified
	switch {
	case strings.HasSuffix(changeType, "Deleted"):
		action = things.ItemActionDeleted
	case changeType == "UnknownChange" && isEmptyPayload(payload) && materializesEntity(classifyKind(kind)):
		// An entity item carrying no fields at all is a deletion. This is the
		// trash-emptying batch that the old parser could not classify.
		action = things.ItemActionDeleted
	}

	return kind, action
}

// countRows fills in the live row counts after a rebuild.
func (s *Syncer) countRows(res *RebuildResult) error {
	counts := []struct {
		query string
		dest  *int
	}{
		{`SELECT COUNT(*) FROM tasks WHERE deleted = 0 AND type = 0`, &res.Tasks},
		{`SELECT COUNT(*) FROM tasks WHERE deleted = 0 AND type = 1`, &res.Projects},
		{`SELECT COUNT(*) FROM tasks WHERE deleted = 0 AND type = 2`, &res.Headings},
		{`SELECT COUNT(*) FROM areas WHERE deleted = 0`, &res.Areas},
		{`SELECT COUNT(*) FROM tags WHERE deleted = 0`, &res.Tags},
		{`SELECT COUNT(*) FROM checklist_items WHERE deleted = 0`, &res.ChecklistItems},
	}
	for _, c := range counts {
		if err := s.db.QueryRow(c.query).Scan(c.dest); err != nil {
			return fmt.Errorf("counting rows: %w", err)
		}
	}
	return nil
}

// Diagnostics reports the integrity problems this version of the sync layer
// was written to fix, so a rebuild can be verified rather than assumed.
type Diagnostics struct {
	// OrphanedChildren counts live tasks sitting untrashed under a trashed
	// project or heading — the symptom of a missing container cascade.
	// A healthy database reports 0.
	OrphanedChildren int
	// OrphanedCompleted is the subset of those that are also completed.
	OrphanedCompleted int
	// StaleDeletes counts rows still present despite the history containing a
	// deletion for them. A healthy database reports 0.
	StaleDeletes int
	// UnresolvedProject/Heading/Area count references to containers that have
	// no row. These are not necessarily corruption: Things Cloud's retained
	// history does not always include the create event for an entity that an
	// older payload references, and such a reference can never be resolved
	// from change_log alone.
	UnresolvedProject int
	UnresolvedHeading int
	UnresolvedArea    int
	// LegacyUnknownRows counts change_log rows recorded as UnknownChange
	// whose entity kind the current parser does understand. These are
	// historical artifacts of the old parser: change_log is immutable, so a
	// rebuild fixes the state they describe but does not relabel the rows.
	LegacyUnknownRows int
	// GenuinelyUnknownRows counts UnknownChange rows whose entity kind this
	// parser still does not recognise, and UnknownKinds names those kinds.
	// This is the number that extending the parser should drive to 0.
	GenuinelyUnknownRows int
	UnknownKinds         []string
}

// Diagnose inspects the materialized tables without modifying anything.
func (s *Syncer) Diagnose() (Diagnostics, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	db := s.readSyncer().db
	var d Diagnostics

	const trashedContainers = `(SELECT uuid FROM tasks WHERE in_trash = 1 AND deleted = 0)`

	scalars := []struct {
		query string
		dest  *int
	}{
		{`SELECT COUNT(*) FROM tasks c WHERE c.deleted = 0 AND c.in_trash = 0 AND (
			c.project_uuid IN ` + trashedContainers + `
			OR c.heading_uuid IN ` + trashedContainers + `)`, &d.OrphanedChildren},
		{`SELECT COUNT(*) FROM tasks c WHERE c.deleted = 0 AND c.in_trash = 0 AND c.status = 3 AND (
			c.project_uuid IN ` + trashedContainers + `
			OR c.heading_uuid IN ` + trashedContainers + `)`, &d.OrphanedCompleted},
		// A row that survived a logged deletion, counted only when the
		// deletion is the entity's most recent event (a uuid can legitimately
		// be deleted and recreated later in the history).
		{`SELECT COUNT(*) FROM tasks t
			WHERE t.deleted = 0 AND EXISTS (
				SELECT 1 FROM change_log c
				WHERE c.entity_uuid = t.uuid
				  AND ` + sqlIsDelete + `
				  AND c.server_index = (
					SELECT MAX(c2.server_index) FROM change_log c2 WHERE c2.entity_uuid = t.uuid
				  )
			)`, &d.StaleDeletes},
		{`SELECT COUNT(*) FROM tasks c WHERE c.project_uuid IS NOT NULL AND c.project_uuid <> ''
			AND NOT EXISTS (SELECT 1 FROM tasks p WHERE p.uuid = c.project_uuid)`, &d.UnresolvedProject},
		{`SELECT COUNT(*) FROM tasks c WHERE c.heading_uuid IS NOT NULL AND c.heading_uuid <> ''
			AND NOT EXISTS (SELECT 1 FROM tasks p WHERE p.uuid = c.heading_uuid)`, &d.UnresolvedHeading},
		{`SELECT COUNT(*) FROM tasks c WHERE c.area_uuid IS NOT NULL AND c.area_uuid <> ''
			AND NOT EXISTS (SELECT 1 FROM areas a WHERE a.uuid = c.area_uuid)`, &d.UnresolvedArea},
	}
	for _, c := range scalars {
		if err := db.QueryRow(c.query).Scan(c.dest); err != nil {
			return d, fmt.Errorf("diagnosing: %w", err)
		}
	}

	// Split the UnknownChange backlog into "the old parser could not classify
	// this, but we can now" and "still unclassified".
	rows, err := db.Query(`
		SELECT COALESCE(NULLIF(item_kind, ''), entity_type) AS kind, COUNT(*)
		FROM change_log
		WHERE change_type = 'UnknownChange'
		GROUP BY kind
		ORDER BY kind
	`)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return d, err
		}
		if classifyKind(things.ItemKind(kind)) == familyUnknown {
			d.GenuinelyUnknownRows += n
			d.UnknownKinds = append(d.UnknownKinds, kind)
		} else {
			d.LegacyUnknownRows += n
		}
	}

	return d, rows.Err()
}
