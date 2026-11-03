# Things 3 Crash Investigation

**Date:** 2026-02-17
**Crash date:** 2026-02-16 23:36:35 UTC
**App:** Things 3.22.10 (build 32210502) — Mac, also reproduced on iPad
**Account:** Test account (now deleted and replaced)

## Crash Report Summary

- **Exception:** `EXC_BREAKPOINT` / `SIGTRAP` with `brk 1` — Swift precondition failure
- **Thread:** 0 (main thread, `com.apple.main-thread`)
- **Stack trace:**
  - Frame 0–2: `Base.framework` (offsets 671248, 718104, 724996)
  - Frame 3: `LegacySCHistoryPerformSync` at symbolLocation 1088 (`Syncrony.framework`)
  - Frame 4: `_PullConnectionDidCompletePullSession` (`Syncrony.framework`)
- **Meaning:** Things crashed while processing history items during a sync pull. The crash is a Swift precondition failure (force-unwrap, bounds check, or explicit precondition) inside Base.framework's data parsing code, called from the Syncrony framework's sync engine.

## History Analysis

The Things Cloud history for the old account contained **503 commits** with **475 items written by our server** across test cycles and Claude MCP operations.

### Operations Claude performed via MCP on real tasks

1. **Deadline clearing + schedule change** (6 tasks, commits 376–381):
   ```json
   {"dd": null, "md": ..., "sr": 1771372800, "st": 2, "tir": null}
   ```
   Cleared deadlines and moved tasks to Upcoming with future dates.

2. **Checklist item creation** (18 items, commits 382–398):
   Created checklist items on existing user tasks (blood pressure tracking, painting steps, case studies, etc.)

3. **Task creation with checklists** (4 tasks + checklist items, commits 400–417):
   - "Morning routine (with coffee)" — daily task with 4 checklist items
   - "Weekly review" — weekly task with 5 checklist items
   - "Take tablet" — daily task
   - "Water house plants" — weekly task with 5 checklist items

4. **Repeat rule edits** (8 edits, commits 444–463):
   Added daily/weekly repeat rules to existing tasks, then later removed them.

### Bugs found in our server's write format

#### 1. `sp: 0` instead of `sp: null` on un-complete (FIXED)

When un-completing a task, `uncompleteTask()` set `"sp": 0` (Unix epoch = January 1, 1970) instead of `"sp": null`. Things displayed these as 1970 dates. This was confirmed by the user seeing 1970 dates in the app.

**Root cause:** `stopDate(0)` passed float64 zero instead of nil.

**Fix:** Changed to set `u.fields["sp"] = nil` directly.

**Location:** `server/write.go`, `uncompleteTask()` function.

#### 2. No past-date validation (FIXED)

The server accepted any date for `when` and `deadline` fields, including dates in the past. This could produce tasks scheduled in the past or deadlines that have already passed.

**Fix:**
- `parseWhen()`: Past dates are clamped to today
- `createTask()`, `editTask()`, `createProject()`: Past deadlines return an error

#### 3. Area modify missing `md` timestamp (NOT FIXED — low risk)

One area rename (commit 135) was sent without an `md` (modification date) field. All task modifies include `md`. This is a minor inconsistency — Things likely handles it gracefully.

#### 4. Task6 delete with empty payload (RESOLVED 2026-11-02 — not a bug)

Two commits (9 and 270) used action type 2 (DELETE) on Task6 with an empty payload `{}`, which is different from trashing (action 1 with `tr: true`).

This is now understood: **action type 2 with an empty payload is Things' own native deletion form.** A read-only fetch of history index 4694 on a real account — the batch produced by emptying the Things trash — returns 348 items of exactly that shape:

```
e=Task7      t=2 (DELETE)  empty_payload=true   count=348
e=Tombstone2 t=0 (create)  empty_payload=false  count=6
```

So the server was not sending something malformed; it was sending what the app sends. The real defect was on the *read* side — those deletions were being ignored, because routing matched a hard-coded list of entity kinds that `Task7` was not in. See [2026-11-02-sync-deletion-cascade-fix.md](2026-11-02-sync-deletion-cascade-fix.md).

#### 5. `own-history-keys` API endpoint returns 404 (OBSERVED)

The Things Cloud API endpoint `GET /version/1/account/{email}/own-history-keys` returns 404 for all accounts, not just those with `+` in the email. This endpoint appears to be deprecated or non-functional. The SDK methods `Histories()`, `CreateHistory()`, and `Delete()` that use this endpoint were also missing the `Authorization: Password` header (now fixed, but endpoint still returns 404).

## Reproduction Attempts

We tested each feature type individually on a fresh account. Things synced successfully after each test:

| Test | Operation | Result |
|------|-----------|--------|
| 1 | Simple task create | OK |
| 2 | Task with checklist items | OK |
| 3 | Task with daily repeat rule (created with repeat) | OK |
| 4 | Task edit: set deadline, then clear deadline + move to Today | OK |
| 5 | Task edit: add weekly repeat to existing task | OK |
| 6 | Tombstone: create checklist item then delete it | OK |
| 7 | Clear deadline + move to Upcoming (future date) | OK |
| 8 | Complete then un-complete (sp: 0 bug) | OK (no crash, but showed 1970 date) |

**Conclusion:** The crash could not be reproduced on a clean account. It likely required the specific combination of Things-native task data from the old account interacting with our server's modifications. The old account had ~300 Things-created items with populated device metadata (`xx.sn`), and our server modified some of those items. The crash may have been caused by:

- A field type mismatch when modifying Things-native items (e.g., Things expects a specific type for `sp` and gets integer 0 instead of null/float)
- An interaction between Things' internal state tracking and our modifications
- A cumulative effect of 500+ single-item commits (our server writes one item per commit; Things batches many items per commit)

## Infrastructure Changes

During investigation:

- **Account:** Deleted original test account, created replacement (no `+` in email)
- **Region:** Moved Fly.io machine from `iad` (US East) to `lhr` (London)
- **Machine ID:** Replaced
- **History key:** Replaced
- **Verbose logging:** `client.Debug = true` and `[WRITE]` logging in `historyWrite()` remain enabled for future diagnostics

## SDK Fixes

- Added `Authorization: Password` header to `Histories()`, `Delete()`, and `CreateHistory()` in `histories.go` (previously only `Verify()` had it)
- Added `Do()` export method to `client.go` for server code needing direct HTTP access

## Files Modified

- `server/write.go` — Fixed `sp: 0` bug, added past-date validation, added verbose write logging
- `server/main.go` — Added debug endpoints, enabled `client.Debug`, added nuke/confirm account endpoints
- `histories.go` — Added auth headers to account-level API methods
- `client.go` — Added exported `Do()` method

## Crash Report

Full crash report and old history dump were local artifacts not checked into this repo.
