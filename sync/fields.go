package sync

import (
	"encoding/json"
	"sort"
)

// Task payload field classification
//
// Things Cloud sends partial updates: a completion arrives as
// {"ss":3,"sp":...,"md":...}, a Today reorder as {"ti":N}, a repeating series'
// next instance as {"icsd":...}. Such an update produces no *named* semantic
// change, and the sync layer used to drop it on the floor — which is how
// legitimate writes ended up indistinguishable from genuinely unparsed events.
//
// The three sets below make that distinction explicit:
//
//   - appliedTaskFields  written to the tasks row by applyTaskPayload
//   - ignoredTaskFields  part of the wire format, deliberately not materialized
//   - anything else      genuinely unknown, and reported as such
var appliedTaskFields = map[string]bool{
	"cd":   true, // creation date
	"md":   true, // modification date
	"ss":   true, // status
	"tt":   true, // title
	"nt":   true, // note (string or delta patch)
	"sr":   true, // scheduled date
	"tir":  true, // today index reference
	"sp":   true, // completion date
	"dd":   true, // deadline date
	"ix":   true, // index
	"ar":   true, // area ids
	"pr":   true, // parent (project) ids
	"agr":  true, // action group (heading) ids
	"tr":   true, // in trash
	"st":   true, // schedule
	"tp":   true, // type (task/project/heading)
	"ti":   true, // today index
	"do":   true, // due order
	"ato":  true, // alarm time offset
	"tg":   true, // tag ids
	"rt":   true, // recurrence template ids
	"dl":   true, // delegate ids
	"icsd": true, // instance creation start date (repeating tasks)
}

var ignoredTaskFields = map[string]bool{
	"rr":   true, // repeater configuration
	"lt":   true, // leavable
	"icp":  true, // is completed by children
	"icc":  true, // is completed count
	"sb":   true, // subtask behavior
	"lai":  true, // last action item id
	"rmd":  true, // reminder date
	"acrd": true, // action required date
	"dds":  true, // deadline suppression
	"xx":   true, // extension data
	"rp":   true, // newer repeater encoding
}

// payloadFields returns the top-level JSON keys an item payload carries.
// A payload that does not parse as an object yields no fields.
func payloadFields(raw json.RawMessage) []string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// classifyTaskFields splits carried payload keys into the ones written to the
// row and the ones this parser does not model at all. Keys in
// ignoredTaskFields are recognised no-ops and appear in neither result.
func classifyTaskFields(carried []string) (applied, unknown []string) {
	for _, f := range carried {
		switch {
		case appliedTaskFields[f]:
			applied = append(applied, f)
		case ignoredTaskFields[f]:
			// known, intentionally not materialized
		default:
			unknown = append(unknown, f)
		}
	}
	return applied, unknown
}
