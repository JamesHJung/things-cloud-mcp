# MCP Today/Overdue Mismatch Incident (2026-03-04 to 2026-03-05)

## Status

Closed, then **reopened and closed again on 2026-11-02** — the original diagnosis was incomplete. See the postscript at the bottom.

The March 5 fixes addressed cursoring, concurrent sync, and read-path error surfacing. A follow-up on 2026-03-15 fixed stale `today_index_ref` carry-over when later updates changed `sr` or `schedule` without sending a fresh `tir`, added the schema v4 migration that invalidates old cache state and forces a full resync (corrupted `sr`/`tir` rows could not be repaired in place), preserved real source `server_index` values when flattening multi-entity history slots, hid soft-deleted tasks from direct UUID lookups, and **standardized task movement detection on UTC day boundaries**.

That last one is the part that mattered later.

## Summary

During morning reviews on **2026-03-04** and **2026-03-05** (Europe/London), MCP task lists diverged from the Things app Today view in both directions:

- **Under-reporting:** tasks visible in Things Today were missing from `things_list_today`.
- **Over-reporting:** tasks returned by MCP appeared with stale `scheduled_for` values but were no longer in Things Today.

This was initially interpreted as possible cloud sync lag only. Investigation found server-side issues that can produce partial or stale snapshots even when some updates are arriving correctly.

## User-Observed Timeline

### 2026-03-04 (morning review)

- `things_list_today` returned **1 of 7** tasks scheduled for 2026-03-04.
- 6 tasks were missing from the MCP response despite being visible in Things Today.

### 2026-03-04 07:29

- Second test: `things_list_today` returned all 12 tasks expected for that day.
- Cross-check against Things app screenshot still showed one mismatch: a task returned by MCP as `scheduled_for=2026-03-04` was not shown in Things Today.

### 2026-03-05 (morning review)

- MCP (`things_list_all_tasks` filtered by date) reported overdue tasks not in Things Today (stale `scheduled_for` values from previous days).
- Also observed inverse mismatch: a task visible in Things Today had a stale `scheduled_for` date in the MCP response.

## Impact

- Morning review reliability degraded.
- `things_list_today` and `things_list_all_tasks`-based overdue workaround both produced inaccurate planning surfaces.
- Stale scheduled dates created false overdue alerts and missed current commitments.

## Technical Findings

### 1) Partial-sync pagination/cursor bug (confirmed)

Two linked cursor issues could skip chunks of history updates:

- `items.go` set `LoadedServerIndex` cumulatively from zero rather than relative to the request `start-index`.
- `sync/sync.go` advanced paging with `LatestServerIndex` (head) instead of the next unread index.

Result: some updates could be observed while others were skipped, matching the reported “not uniformly delayed” symptom.

### 2) Concurrent sync race (confirmed)

`Syncer.Sync()` was called from multiple handlers without serialization, while mutating shared state (`history`, tx state, sync cursor).

Observed production log evidence on **2026-03-05 08:53:36 UTC**:

`[SYNC] post-write refresh failed: sql: transaction has already been committed or rolled back`

This can produce transient stale read states after writes.

### 3) Read endpoints silently served stale data on sync failure (confirmed)

Read paths called pre-read sync but ignored errors, returning whatever was in local DB.

### 4) UTC day-boundary hardening (preventive)

Today calculations now use explicit UTC midnight to avoid host-local-time truncation drift.

## Fixes Implemented

- `items.go`
  - `LoadedServerIndex = opts.StartIndex + len(v.Items)`
- `sync/sync.go`
  - Pagination now advances with `LoadedServerIndex`
  - Added mutex to serialize `Sync()` calls
- `server/main.go`
  - REST read handlers now return `503` on pre-read sync failure
- `server/mcp.go`
  - MCP read tools now return explicit pre-read sync error results
- `sync/state.go`
  - `TasksInToday()` uses UTC day boundaries
- `server/write.go`
  - `todayMidnightUTC()` now uses `time.Now().UTC()`

## Validation

- Local verification: `go test ./...` passed after changes.
- Behavioral expectation after deploy:
  - Reduced partial-update mismatches where only a subset of edits appears.
  - Explicit failures instead of silent stale snapshots when sync fails.

## Remaining Risk / Caveat

- True cloud/app eventual consistency can still exist, but should now be distinguishable from server bugs because:
  - local cursor/pagination skipping is fixed,
  - concurrent sync races are mitigated,
  - sync failures are surfaced instead of hidden.

## Recommended Post-Deploy Verification

For a single task UUID, run a controlled experiment:

1. In Things app, move task into Today.
2. Poll every 15–30s:
   - `things_get_task(uuid)`
   - `things_list_today`
   - `things_list_all_tasks` (filtered by UUID/date)
3. Move task out of Today (clear or future date), repeat polling.
4. Record first-seen timestamps in app vs MCP for each transition.

Use 3–5 repetitions to estimate whether residual lag is external (Things Cloud propagation) or local.

## Skill-Level Follow-up (separate)

`things-daily` logic should continue to:

- classify overdue by `scheduled_for < today` and `status == "open"`,
- separate deadline warnings (`deadline < today` with future/no schedule),
- caveat overdue output as approximate when cloud sync lag is suspected.

---

## Postscript: the incomplete diagnosis (2026-11-02)

Standardizing on **UTC day boundaries** in March was itself a bug, and it went unnoticed because it is invisible from the time zone this was investigated in.

Things stores `scheduled_date`, `today_index_ref` and `deadline_date` as *date-only* values encoded as **midnight UTC** of the intended day — on a real database every such value is an exact multiple of 86400. So the UTC calendar date of a stored value *is* the local day the user meant. The window has to be built from the **local** calendar date while being expressed at **UTC** midnight.

Using the UTC calendar date instead puts the window a day ahead for every local hour after UTC midnight. At UTC+0, where this incident was investigated, there is no such hour and the two forms coincide. At UTC-8 it is eight hours out of every twenty-four, during which Today silently returns tomorrow's tasks and drops today's.

Both the `isToday`/`taskLocation` comparison and the `currentUTCDayBounds` query were affected. Fixed, with the invariant pinned by clock-injected tests across fixed zones and the suite now running under four time zones in CI.

The symptoms reported here — under-reporting *and* over-reporting of Today, with stale `scheduled_for` values — are consistent with this as a contributing cause, alongside the cursoring and sync issues that were fixed at the time. Anyone re-reading this incident should treat "standardize on UTC" as the wrong lesson to draw from it.
