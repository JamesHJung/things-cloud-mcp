# Legacy identifier derivation: a known, unfixed data defect

**Status:** open. Diagnosed and measured 2026-11-03; deliberately not fixed yet.
Upstream has not fixed it in its sync engine either.

## The mechanism

Things Cloud migrated from UUID-style identifiers to Base58 identifiers and
**wrote no mapping records into the history**. The current identifier for a
pre-migration object is derived by hashing the old one:

```
Base58( SHA1(legacyID)[:16] )
```

Hashing is byte-exact: the input must be the identifier text exactly as it
appears in the history, so any normalisation (lowercasing a UUID, trimming)
produces a different identifier. Recurring-task instances use composite
identifiers of the form `<uuid>-YYYYMMDD` and hash in two steps — first the
`<uuid>` prefix alone, then its raw 16-byte digest followed by the
`-YYYYMMDD` text.

The consequence for a replaying sync engine: **legacy-kind items key objects by
the old identifier, while current-kind items key the same object by the derived
one.** `Task3`, `Task4`, `Task`, `ChecklistItem`, `ChecklistItem2`, `Area`,
`Area2`, `Tag`, `Tag3` and `Tombstone` are legacy; `Task6`, `Task7`,
`ChecklistItem3`, `Area3`, `Tag4` and `Tombstone2` are current.

Credit for the discovery is upstream's
([`arthursoares/things-cloud-sdk`](https://github.com/arthursoares/things-cloud-sdk),
`EncodeLegacyIdentifier`). It applies the derivation in `state/memory` and the
write paths — but **not** in its own `sync` package, which has the same defect
this one does.

## What it does to the database

This engine uses `item.UUID` verbatim for every kind, so one object becomes two
rows: the legacy row created by the old items, and a second row created by the
later current-kind events. Measured on a real account (1,222 live tasks):

| | |
|---|---|
| legacy-keyed rows | **650** — 589 tasks, 41 checklist items, 13 areas, 7 tags |
| tasks existing as a **torn pair** | **53** |
| rows whose project/area/heading reference is a legacy-style id | **535** |

The tear is not benign. In the pairs sampled, the legacy row holds the title
while the derived row holds the newer status:

| legacy row | derived row |
|---|---|
| `"Build a slide deck around orga…"` status=3 | `""` status=0 |
| `"Drain in Bathroom"` status=0 | `""` status=3 |
| `"Salesforce Stablization"` status=0 | `""` status=3 |

So completions were applied to an invisible, titleless row while the visible
task kept its old status — which presents as "I completed that and it came
back".

## Why it is not fixed yet

Three reasons, all of which need a decision rather than more analysis.

**It changes identifiers.** Fixing it means keying those objects by the derived
identifier, after which ~589 task UUIDs change. Anything holding one — a saved
skill, a note, an agent's memory of a task — breaks once.

**It casts doubt on the UUID validation relaxation.** Write validation was
relaxed so the legacy hyphenated identifiers this database holds could be
operated on at all (see the README's note on `IsValidItemUUID`). But if the
*current* identifier for those objects is the derived one, a write keyed by the
hyphenated id may be addressing an object Things no longer recognises —
creating a phantom rather than editing the task. The relaxation was right that
we should not reject identifiers we hold; it may be wrong that those are the
identifiers we should be holding.

**It needs a live write test first.** Before trusting either behaviour, write
to a single known legacy task by its hyphenated id and by its derived id, and
see which one Things actually updates. That is a five-minute experiment and it
settles the question; reasoning about it further does not.

## If and when it is fixed

- `EncodeLegacyIdentifier` belongs in `uuid.go` beside `EncodeUUID`, with the
  composite recurrence handling.
- `processItem` derives `item.UUID` for legacy kinds; each per-entity handler
  derives its identifier *references* after unmarshalling — `ar`, `pr`, `agr`,
  `tg`, `rt` for tasks, `ts` for checklist items, `pn` for tags, `dloid` for
  tombstones.
- Existing databases are repaired by `cmd/rebuild`, which replays `change_log`
  through the same path, so no migration is needed beyond running it.
- The torn pairs should merge rather than leaving an orphan: after derivation
  both halves land on the same key and later events win, which is the correct
  outcome, but it is worth asserting in a test against a real pair.
