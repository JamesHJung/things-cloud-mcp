# Sync: deletions, container cascades, and the `Task7` bucket

**Date:** 2026-11-02
**Branch:** `fix/sync-deletions-and-cascades`
**Schema:** 4 → 5 (additive migration, no cache wipe)

Diagnosed and verified against a real 2.6 MB sync database at `schema_version`
4 (4,916 history items, 5,239 `change_log` rows, history indices 0–4694).

---

## Summary

| | before | after |
|---|---|---|
| rows surviving a logged deletion | 204 | **0** |
| untrashed children of trashed containers | 66 (46 completed) | **0** |
| `change_log` rows genuinely unclassifiable | 577 | **0** |
| real to-dos missing from local state | 5 | **0** (recovered) |
| unresolved area references | 0 | 2 (pre-existing, unrecoverable — see §6) |

Three defects, one shared root cause. All three are fixed, and an existing
install repairs itself in place with `cmd/rebuild` — no re-pull from Things
Cloud, no credentials, ~3 seconds.

---

## 1. Root cause: entity-kind version drift

Items were routed on the `e` (entity kind) field by matching a hard-coded list:

```go
case things.ItemKindTask, things.ItemKindTask4, things.ItemKindTask3, things.ItemKindTaskPlain:
```

Things Cloud *versions* that name and bumps the suffix when the app's storage
schema changes: `Task3` → `Task4` → `Task6` → **`Task7`**. When Things started
sending `Task7`, every task item of the new kind missed every case arm and fell
through to the `default` branch as `UnknownChange`. Nothing was applied.

Measured in the live database:

```
entity_type   rows      (change_type = 'UnknownChange')
Task7          545
Command         14
Command3        14
Settings5        3
Settings4        1
```

`Task7` payload shapes behind those 545 rows:

```
348  {}                        <- deletions, silently dropped
172  icsd                      <- repeating-task next-instance date
  7  md,sp,ss                  <- completions
  5  acrd,agr,ar,...,tp,tr,tt  <- full-object upserts
  3  md,tt                     <- renames
  3  md,sp,ss,st
  2  icc,icsd,tir
  2  ti                        <- Today reorder
  ...
```

**Fix:** route on the entity *family*, with the version suffix stripped
(`sync/kind.go`). `Task7` — and a future `Task8` — routes correctly with no code
change. This is the kind of bug that must not be able to recur, so the
classification is table-tested across `Task3`…`Task12`, `Area`/`Area2`/`Area3`,
`Tag`/`Tag3`/`Tag4`, `ChecklistItem`/`ChecklistItem3`, `Settings3/4/5`,
`Command`/`Command3`, and an unknown kind.

---

## 2. Deletions were never applied

Emptying the Things trash emitted **354 deletions in one batch** at server index
4694. Six carried the older `Task` kind and were applied correctly; the other
**348 were `Task7`** and were logged with an empty payload while the rows stayed
live. 204 of those rows were still present in the database.

**Fix:** deletions mark the row deleted and cascade to `task_tags` and
`checklist_items` immediately (not just at the end-of-sync purge), so a later
item in the same batch cannot see the dependents of a deleted task. A deletion
for a UUID that was never materialized is now still written to `change_log`
rather than dropped, so the log stays a complete account of the history.

### Why an empty payload is not sufficient to detect a deletion

The obvious heuristic — "empty payload means delete" — is wrong, and the live
data proves it:

```
change_type           entity_type      rows   (payload = '{}')
UnknownChange         Task7             348   <- deletions
TaskDeleted           Task               13   <- deletions
AreaDeleted           Area                8   <- deletions
TaskCreated           Task                6   <- NOT deletions
HeadingDeleted        Heading             3
ChecklistItemDeleted  ChecklistItem       2
ProjectDeleted        Project             1
UnknownChange         Command3           14   <- NOT deletions
```

Six genuine `TaskCreated` items also carry `{}`. The authoritative signal is the
envelope's `t` field, which is now persisted (§5).

---

## 3. Container changes did not cascade

Things applies a container operation to everything inside the container. Things
Cloud emits an item **only for the container itself** — never one per affected
child. Applying each item in isolation therefore left children live under a
trashed or deleted parent:

```sql
-- 66 rows, 46 of them status = 3
SELECT count(*) FROM tasks c JOIN tasks p ON c.project_uuid = p.uuid
WHERE p.in_trash = 1 AND c.in_trash = 0;
```

**Fix:** cascades are re-derived locally (`sync/cascade.go`), walking the
hierarchy transitively through *both* `project_uuid` and `heading_uuid` with a
recursive CTE, so `project → heading → to-do` is fully covered.

| Event | Effect on contents |
|---|---|
| `ProjectTrashed`, `TaskTrashed` (incl. headings) | `in_trash = 1` on all descendants |
| `ProjectRestored`, `TaskRestored` | `in_trash = 0` on all descendants |
| `ProjectDeleted`, `HeadingDeleted`, `TaskDeleted` | descendants marked deleted |
| `AreaDeleted` | contents kept, area reference cleared (§4) |
| `ProjectCompleted` | **no cascade** |

Cascades are applied to the materialized tables but deliberately **not** written
to `change_log`. The log stays a faithful record of what Things Cloud actually
sent, so replaying it re-derives the cascade instead of double-applying a stale
one.

`IN` clauses are chunked at 500 parameters, since a cascade's size is unbounded
and SQLite caps variables per statement.

### `ProjectCompleted` does not cascade status

Completing a project in Things logs the project and leaves its unfinished to-dos
alone, so neither `status` nor `in_trash` is propagated. Trashing that project
later *does* cascade, and completed children are trashed along with everything
else — which is the case that produced the 46 completed-but-untrashed rows.

---

## 4. `AreaDeleted`: what the history actually shows

This was initially implemented as "delete the area's contents", on the
assumption that Things propagates an area deletion. **The history disagrees.**

Across the `AreaDeleted` events in the account, of the 13 items filed under a
deleted area at the moment of deletion:

```
area 66ADED52-… deleted @ 1050:  2 members -> 0 received any later item
area 3SPPFHMD…  deleted @ 4056: 11 members -> 2 received a later item
                                              (both at 4694, the trash-empty,
                                               unrelated to the area)
```

**11 of 13 received no further item of any kind** — no delete, no trash, no
update. Things pushes its own actions to the cloud, so had it deleted those
items it would have sent deletions for them. It sent none.

So an area's contents outlive the area, holding a reference to a container that
no longer exists. The default now **keeps those rows and clears the stale
reference**, which preserves the data and leaves no dangling pointer. The other
behaviour remains available:

```go
syncer.SetAreaDeleteMode(sync.AreaDeleteRemovesContents)
```

or `rebuild -delete-area-contents`.

This changed the outcome materially: 13 rows that the first implementation
deleted are now kept.

---

## 5. `UnknownChange` was too broad

Things Cloud sends partial updates. An update that produced no *named* semantic
change used to vanish entirely. Payload keys are now classified explicitly
(`sync/fields.go`) into three sets:

- **applied** — written to the row. If no named change results, the update is
  recorded as `TaskFieldsUpdated` with the field list, so the write stays
  visible. `{"ti":N}` (Today reorder) and `{"icsd":…}` (repeating-task
  next-instance date) are the common cases. `icsd` was previously *parsed but
  never written*, making all 172 of those updates silent no-ops; it now has its
  own column and a field on `things.Task`.
- **recognised no-ops** — part of the wire format, deliberately not
  materialized: `rr`, `lt`, `icp`, `icc`, `sb`, `lai`, `rmd`, `acrd`, `dds`,
  `xx`, `rp`. No change is emitted.
- **genuinely unknown** — reported as `UnknownChange` **naming the fields**, so
  the parser can be extended. An unparsable payload is likewise reported instead
  of aborting the whole sync batch, so one bad item can no longer strand a sync.

`Settings*` and `Command*` items are now recognised and skipped. `Command`
envelopes are the mail-to-inbox / share-extension path; their effect also
arrives as an ordinary `Task` item, so applying them would duplicate the task.

**`UnknownChange` now means genuinely unknown.**

---

## 6. `cmd/rebuild` — repairing an existing install

`change_log` already holds every payload Things Cloud sent, so a database
written by the old sync layer can be repaired without re-pulling the history.

```bash
go run ./cmd/rebuild -diagnose             # report problems, change nothing
go run ./cmd/rebuild                       # repair ~/.things-workflow/sync.db
go run ./cmd/rebuild -db /path/to/sync.db
go run ./cmd/rebuild -delete-area-contents # non-default area behaviour
```

Design points:

- **Reuses the live `processItem` path**, so a rebuild and a fresh sync cannot
  drift apart.
- **Preserves `change_log` and `sync_state`**, so the cursor survives and the
  next `Sync()` resumes normally.
- **Collapses rows on `(server_index, entity_uuid)`.** One item can produce
  several `change_log` rows (one per detected change); the replay must treat
  them as one item. A history slot holds at most one entry per UUID, so that
  pair uniquely identifies a source item.
- **Idempotent** — verified by running it twice and comparing state checksums.
- **~3 seconds** for 4,916 items.

`Diagnose()` reports the invariants, and the CLI prints them before and after so
the repair is verified rather than assumed.

### What a replay cannot do

- **It does not relabel historic `change_log` rows.** Rows recorded as
  `UnknownChange` by the old parser stay that way; the log is immutable.
  `-diagnose` separates these ("legacy … now parseable") from rows this parser
  still cannot classify — the latter is the number that should be zero.
- **It cannot resolve a reference to an entity with no create event in the
  retained history.** Things Cloud does not keep history forever. Two tasks
  reference areas with *zero* events in the log, so no replay can materialize
  those areas. The reference is kept faithfully and counted as unresolved. This
  is distinct from a deleted area, whose contents do get their reference
  cleared.

---

## 7. Schema 5

Additive, and unlike migration 4 it does **not** clear the cache — repair is via
`rebuild` instead.

- `change_log.item_kind`, `change_log.item_action` — the raw Things Cloud
  envelope (`e` and `t`), so the log can be replayed verbatim.
- `tasks.instance_creation_start_date` — the `icsd` field.

Rows written before v5 have `NULL` for the envelope and have it inferred on
replay from `change_type` plus the payload. That inference *needs*
`change_type`, for the reason in §2: an empty payload means a deletion on an
`UnknownChange` row but not on a `TaskCreated` row.

Column additions are now **idempotent** (`ALTER TABLE` guarded by
`pragma_table_info`), so a partially-applied migration no longer hard-fails on
reopen. Migration 3 had the same latent hazard and was given the same treatment.

---

## 8. UUID validation accepts legacy hyphenated IDs

Write validation rejected anything that was not a Things Base58 UUID. Things
Cloud still holds **hyphenated UUIDs** (8-4-4-4-12) for items imported around
2020, and the hyphens — plus characters Base58 excludes, such as `0`, `O`, `I`
and `l` — failed the check. Those items were unaddressable through the MCP tools
even though the server accepts operations on them.

In the live database: **589 tasks, 13 areas, 7 tags, 41 checklist items**.

Validation now accepts either form via `IsValidItemUUID` (`uuid.go`), which also
de-duplicates two byte-identical copies of the old validator in `server/write.go`
and `cmd/things-cli/main.go`. Lowercase hyphenated IDs are accepted too, since
they appear in mail-to-inbox payloads. Malformed IDs are still rejected.

---

## 9. Verification

- `go build ./...`, `go vet ./...` clean; `go test ./...` and `go test -race
  ./...` green.
- **38 new tests**, built from the real payload shapes above. Each fix was
  checked to actually *fail* without it: reverting the kind classification
  reproduces the reported symptom exactly (`expected ProjectTrashed, got
  [UnknownChange]`), and stubbing the cascade fails four tests.
- **Full audit of the rebuild against the live database.** Of 219 rows removed,
  **204 had their own deletion event** and the rest had a deleted container —
  **zero unexplained removals**. Separately, **5 real to-dos were recovered**
  that the old parser had dropped entirely: they were created by `Task7` items
  and never materialized.
- Both `AreaDeleteMode` values exercised against the live database.

## 10. Out of scope

Untouched, as agreed: the Cloudflare/auth layer and `docker-compose`. Nine
pre-existing `gofmt`-unclean files under `cmd/` and `example/` were left alone;
every file in this change is `gofmt`-clean.

### Follow-ups worth considering

- `rp` (a newer repeater encoding) is accepted as a recognised no-op but is not
  modelled. It appears in full upserts with a `null` value only, so there is
  nothing to model yet.
- `.github/Workflows/` is capitalized, and contains a nested
  `.github/Workflows/.github/Workflows/deploy.yml`. GitHub only reads
  `.github/workflows/` (lowercase), so these almost certainly never run. Not
  touched here as it is unrelated to the sync layer.
