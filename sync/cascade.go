package sync

import (
	"fmt"
	"strings"
)

// Cascade rules
//
// Things applies container operations to everything inside the container: when
// a project is moved to the trash its to-dos and headings go with it, and when
// a project is deleted outright so are its children. Things Cloud, however,
// only emits an item for the container itself — it does not emit one item per
// affected child. A sync layer that applies each item in isolation therefore
// ends up with children that are still live under a trashed or deleted parent.
//
// The helpers below re-derive those child effects locally. They are applied as
// a side effect of the parent's item and are deliberately *not* written to
// change_log: change_log stays a faithful record of the items Things Cloud
// actually sent, so replaying it (see Rebuild) re-derives the same cascade
// rather than double-applying a stale one.

// containerDescendants returns the UUIDs of every task, heading and sub-project
// transitively contained by the given container, via either project_uuid
// (project -> child) or heading_uuid (heading -> child).
//
// The UNION in the recursive term dedupes rows, which also makes the walk
// terminate if Things ever hands us a cyclic parent reference.
func (s *Syncer) containerDescendants(uuid string) ([]string, error) {
	if uuid == "" {
		return nil, nil
	}

	rows, err := s.db.Query(`
		WITH RECURSIVE descendants(uuid) AS (
			SELECT uuid FROM tasks
			WHERE (project_uuid = ? OR heading_uuid = ?) AND uuid <> ?
			UNION
			SELECT t.uuid FROM tasks t
			JOIN descendants d ON (t.project_uuid = d.uuid OR t.heading_uuid = d.uuid)
		)
		SELECT uuid FROM descendants
	`, uuid, uuid, uuid)
	if err != nil {
		return nil, fmt.Errorf("walking descendants of %s: %w", uuid, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// areaChildren returns the UUIDs of tasks and projects filed directly under an area.
func (s *Syncer) areaChildren(areaUUID string) ([]string, error) {
	if areaUUID == "" {
		return nil, nil
	}

	rows, err := s.db.Query(`SELECT uuid FROM tasks WHERE area_uuid = ?`, areaUUID)
	if err != nil {
		return nil, fmt.Errorf("listing children of area %s: %w", areaUUID, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// sqlVarChunk bounds how many bound parameters go into one statement.
// SQLite caps variables per statement, so an IN clause built from a cascade of
// unknown size has to be chunked rather than assumed to fit.
const sqlVarChunk = 500

// chunk splits uuids into slices of at most sqlVarChunk elements.
func chunk(uuids []string) [][]string {
	var out [][]string
	for len(uuids) > sqlVarChunk {
		out = append(out, uuids[:sqlVarChunk])
		uuids = uuids[sqlVarChunk:]
	}
	if len(uuids) > 0 {
		out = append(out, uuids)
	}
	return out
}

// placeholders builds "(?, ?, ?)" for an IN clause along with the args.
func placeholders(uuids []string) (string, []any) {
	marks := make([]string, len(uuids))
	args := make([]any, len(uuids))
	for i, u := range uuids {
		marks[i] = "?"
		args[i] = u
	}
	return "(" + strings.Join(marks, ", ") + ")", args
}

// cascadeTrash sets (or clears) in_trash on every descendant of a container.
// Returns the number of child rows whose in_trash value actually changed.
func (s *Syncer) cascadeTrash(containerUUID string, inTrash bool) (int, error) {
	uuids, err := s.containerDescendants(containerUUID)
	if err != nil {
		return 0, err
	}

	target := 0
	if inTrash {
		target = 1
	}

	total := 0
	for _, batch := range chunk(uuids) {
		in, args := placeholders(batch)
		// Only touch rows that disagree, so the reported count is meaningful.
		res, err := s.db.Exec(
			`UPDATE tasks SET in_trash = ? WHERE uuid IN `+in+` AND in_trash <> ? AND deleted = 0`,
			append(append([]any{target}, args...), target)...,
		)
		if err != nil {
			return 0, fmt.Errorf("cascading trash from %s: %w", containerUUID, err)
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}
	return total, nil
}

// cascadeDelete marks every descendant of a container deleted, along with the
// dependent checklist items and tag links of those descendants.
// Returns the number of child task rows newly marked deleted.
func (s *Syncer) cascadeDelete(containerUUID string) (int, error) {
	// containerDescendants is already transitive, so no further walk is needed.
	uuids, err := s.containerDescendants(containerUUID)
	if err != nil {
		return 0, err
	}
	return s.markTasksDeleted(uuids)
}

// deleteTasks marks the given tasks deleted together with everything they
// transitively contain. Use it when the input is a set of container roots
// whose descendants have not been walked yet.
func (s *Syncer) deleteTasks(roots []string) (int, error) {
	if len(roots) == 0 {
		return 0, nil
	}

	seen := make(map[string]bool, len(roots))
	var all []string
	for _, r := range roots {
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		all = append(all, r)

		kids, err := s.containerDescendants(r)
		if err != nil {
			return 0, err
		}
		for _, k := range kids {
			if k != "" && !seen[k] {
				seen[k] = true
				all = append(all, k)
			}
		}
	}

	return s.markTasksDeleted(all)
}

// markTasksDeleted flags the exact set of tasks given, plus their checklist
// items and tag links. It does not walk the hierarchy.
func (s *Syncer) markTasksDeleted(uuids []string) (int, error) {
	total := 0
	for _, batch := range chunk(uuids) {
		in, args := placeholders(batch)

		res, err := s.db.Exec(`UPDATE tasks SET deleted = 1 WHERE uuid IN `+in+` AND deleted = 0`, args...)
		if err != nil {
			return 0, fmt.Errorf("marking tasks deleted: %w", err)
		}
		n, _ := res.RowsAffected()
		total += int(n)

		if _, err := s.db.Exec(`UPDATE checklist_items SET deleted = 1 WHERE task_uuid IN `+in, args...); err != nil {
			return 0, fmt.Errorf("cascading delete to checklist_items: %w", err)
		}
		if _, err := s.db.Exec(`DELETE FROM task_tags WHERE task_uuid IN `+in, args...); err != nil {
			return 0, fmt.Errorf("cascading delete to task_tags: %w", err)
		}
	}
	return total, nil
}

// AreaDeleteMode controls what happens to an area's contents when Things
// Cloud reports the area deleted.
//
// Things Cloud sends only the area's own deletion, so the local effect on its
// projects and to-dos has to be chosen here. This is the one cascade rule that
// could not be verified against the Things app while this was written, and it
// is the only one that removes rows a user might still expect to see, so it is
// a deliberate switch rather than a hard-coded assumption.
type AreaDeleteMode int

const (
	// AreaDeleteRemovesContents deletes the projects and to-dos filed under a
	// deleted area, matching Things' own "its projects and to-dos will also
	// be deleted" behaviour. This is the default.
	AreaDeleteRemovesContents AreaDeleteMode = iota
	// AreaDeleteUnfilesContents keeps those rows and just clears their area,
	// so nothing is lost locally if Things turns out to unfile rather than
	// delete.
	AreaDeleteUnfilesContents
)

// SetAreaDeleteMode chooses how area deletions cascade. See AreaDeleteMode.
func (s *Syncer) SetAreaDeleteMode(mode AreaDeleteMode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.areaDeleteMode = mode
}

// cascadeAreaDelete applies the configured AreaDeleteMode to an area's
// contents. Returns the number of task rows affected.
func (s *Syncer) cascadeAreaDelete(areaUUID string) (int, error) {
	children, err := s.areaChildren(areaUUID)
	if err != nil {
		return 0, err
	}
	if len(children) == 0 {
		return 0, nil
	}

	if s.areaDeleteMode == AreaDeleteUnfilesContents {
		total := 0
		for _, batch := range chunk(children) {
			in, args := placeholders(batch)
			res, err := s.db.Exec(`UPDATE tasks SET area_uuid = NULL WHERE uuid IN `+in, args...)
			if err != nil {
				return 0, fmt.Errorf("unfiling contents of area %s: %w", areaUUID, err)
			}
			n, _ := res.RowsAffected()
			total += int(n)
		}
		return total, nil
	}

	return s.deleteTasks(children)
}
