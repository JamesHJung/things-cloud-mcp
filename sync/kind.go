package sync

import (
	"strings"

	things "github.com/arthursoares/things-cloud-sdk"
)

// entityFamily is the schema-version-independent classification of an
// ItemKind. Things Cloud versions its entity names ("Task3", "Task4",
// "Task6", "Task7", "Area2", "Area3", "ChecklistItem3", ...) and bumps the
// suffix whenever the app's storage schema changes. Matching kinds against a
// hard-coded list therefore silently breaks on every new Things release: that
// is exactly how "Task7" ended up in the UnknownChange bucket, taking a whole
// trash-emptying batch of deletions with it.
//
// classifyKind strips the trailing version digits and classifies on the base
// name instead, so future "Task8"/"Area4" kinds route correctly without a
// code change.
type entityFamily int

const (
	familyUnknown entityFamily = iota
	familyTask
	familyArea
	familyTag
	familyChecklistItem
	familyTombstone
	// familyIgnored covers kinds that are recognised but deliberately not
	// materialised (settings, and the mail-to-inbox "Command" envelopes
	// whose effects arrive separately as ordinary Task items).
	familyIgnored
)

// baseKindName strips a trailing schema-version suffix from an item kind,
// e.g. "Task7" -> "Task", "ChecklistItem3" -> "ChecklistItem".
func baseKindName(kind things.ItemKind) string {
	s := string(kind)
	return strings.TrimRight(s, "0123456789")
}

// classifyKind maps a raw Things Cloud item kind onto an entity family.
func classifyKind(kind things.ItemKind) entityFamily {
	switch baseKindName(kind) {
	case "Task":
		return familyTask
	case "Area":
		return familyArea
	case "Tag":
		return familyTag
	case "ChecklistItem", "CheckListItem":
		return familyChecklistItem
	case "Tombstone":
		return familyTombstone
	case "Settings", "Command":
		return familyIgnored
	default:
		return familyUnknown
	}
}

// materializesEntity reports whether a family has rows in the materialized
// tables. Ignored kinds (settings, commands) and unrecognised ones do not, so
// an empty payload on one of them cannot be read as a deletion.
func materializesEntity(f entityFamily) bool {
	switch f {
	case familyTask, familyArea, familyTag, familyChecklistItem, familyTombstone:
		return true
	}
	return false
}
