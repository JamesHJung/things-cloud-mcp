# Things Cloud

A Go server and SDK for the [Things 3](https://culturedcode.com/things/) cloud API. Gives [Claude](https://claude.ai) (and other MCP-compatible AI assistants) full read/write access to your task manager.

Built on a reverse-engineered, unofficial SDK — there is no official API documentation from Cultured Code.

> **This is a maintained fork.** The upstream repository
> [`mattydsmith/things-cloud-mcp`](https://github.com/mattydsmith/things-cloud-mcp)
> was archived in March 2026 and no longer accepts pull requests, leaving two
> open fixes permanently unmergeable. This fork carries on from there: it fixes
> the sync layer's handling of deletions, container cascades and partial
> updates (see
> [Sync correctness](#sync-correctness-deletions-cascades-and-the-task7-bucket)),
> adds a repair command for existing databases, and adds optional bearer
> authentication so non-browser MCP clients can connect. Issues and pull
> requests are welcome here.
>
> The sync engine originates in
> [`arthursoares/things-cloud-sdk`](https://github.com/arthursoares/things-cloud-sdk),
> which is actively maintained and is the better home for SDK-level fixes; the
> MCP server layer is what this repository adds on top.

## What it does

The server syncs your Things Cloud data into a local SQLite database and exposes it through two interfaces:

- **MCP Endpoint** (`/mcp`) — [Model Context Protocol](https://modelcontextprotocol.io/) for AI assistants like Claude
- **REST API** (`/api/*`) — Bearer token auth, for scripts and apps

Changes you make in the Things app show up immediately. Tasks Claude creates appear in Things within seconds. No restart required.
The server performs an initial sync at startup and now exits if it cannot establish a clean local snapshot.

```
┌──────────────┐     ┌──────────────────┐     ┌──────────────┐
│  Things App  │────▶│  Things Cloud    │◀────│  API Server  │
│  (Mac/iOS)   │     │  (Cultured Code) │     │  (Fly.io)    │
└──────────────┘     └──────────────────┘     └──────┬───────┘
                                                     │
                                              ┌──────┴───────┐
                                              │              │
                                         /api/*         /mcp
                                        REST API    MCP Endpoint
                                              │              │
                                         curl/apps    Claude.ai
                                                     Connector
```

## MCP tools (36)

Once connected, Claude can do pretty much everything you'd do in the Things app:

- **Browse your views** — Today, Inbox, Anytime, Someday, Upcoming, All Tasks, by Project, by Area, Completed (with date filters)
- **Look things up** — get any task, area, or tag; search by title or note content
- **Create** — tasks (with notes, dates, deadlines, project, tags, recurrence), projects, areas, tags, headings, and checklist items
- **Edit** — title, notes, dates, deadlines, project, area, tags, and recurrence
- **Organise** — move tasks between Today, Anytime, Someday, and Inbox
- **Complete & restore** — complete or reopen tasks and checklist items; trash and untrash
- **Diagnostics** — built-in smoke test covering the full create/read/edit/complete/trash cycle

<details>
<summary>Full tool reference</summary>

### Read tools

| Tool | Description | Parameters |
|------|-------------|------------|
| `things_list_today` | Tasks scheduled for today | `limit`, `offset` |
| `things_list_inbox` | Tasks in the inbox | `limit`, `offset` |
| `things_list_anytime` | Tasks in Anytime | `limit`, `offset` |
| `things_list_someday` | Tasks in Someday | `limit`, `offset` |
| `things_list_upcoming` | Tasks in Upcoming | `limit`, `offset` |
| `things_list_all_tasks` | All open tasks | `limit`, `offset` |
| `things_list_projects` | All projects | `limit`, `offset` |
| `things_list_areas` | All areas | `limit`, `offset` |
| `things_list_tags` | All tags | `limit`, `offset` |
| `things_list_project_tasks` | Tasks in a project | `project_uuid`, `limit`, `offset` |
| `things_list_area_tasks` | Tasks in an area | `area_uuid`, `limit`, `offset` |
| `things_list_completed` | Recently completed tasks | `limit`, `completed_after`, `completed_before` |
| `things_list_checklist_items` | Checklist items for a task | `task_uuid` |
| `things_get_task` | Get a single task | `uuid` |
| `things_get_area` | Get a single area | `uuid` |
| `things_get_tag` | Get a single tag | `uuid` |

### Write tools

| Tool | Description | Parameters |
|------|-------------|------------|
| `things_create_task` | Create a task | `title` (req), `note`, `when`, `deadline`, `project`, `parent_task`, `tags`, `repeat` |
| `things_create_project` | Create a project | `title` (req), `note`, `when`, `deadline`, `area` |
| `things_create_heading` | Create a heading in a project | `title` (req), `project` |
| `things_create_area` | Create an area | `title` (req), `tags` |
| `things_create_tag` | Create a tag | `title` (req), `shorthand`, `parent` |
| `things_edit_task` | Edit a task | `uuid` (req), `title`, `note`, `when`, `deadline`, `project`, `parent_task`, `area`, `tags`, `repeat` |
| `things_complete_task` | Complete a task | `uuid` |
| `things_uncomplete_task` | Reopen a completed task | `uuid` |
| `things_trash_task` | Move to trash | `uuid` |
| `things_untrash_task` | Restore from trash | `uuid` |
| `things_move_to_today` | Schedule for today | `uuid` |
| `things_move_to_anytime` | Move to anytime | `uuid` |
| `things_move_to_someday` | Move to someday | `uuid` |
| `things_move_to_inbox` | Move to inbox | `uuid` |

### Search tools

| Tool | Description | Parameters |
|------|-------------|------------|
| `things_search_tasks` | Search tasks by title or note | `query` |

### Checklist item tools

| Tool | Description | Parameters |
|------|-------------|------------|
| `things_create_checklist_item` | Add a checklist item to a task | `title` (req), `task_uuid` (req) |
| `things_complete_checklist_item` | Complete a checklist item | `uuid` |
| `things_uncomplete_checklist_item` | Reopen a checklist item | `uuid` |
| `things_delete_checklist_item` | Delete a checklist item | `uuid` |

### Diagnostic tools

| Tool | Description | Parameters |
|------|-------------|------------|
| `things_smoke_test` | Run a smoke test: create, read, edit, complete, trash | — |

### Parameter reference

#### `when` — Task scheduling

| Value | Effect |
|-------|--------|
| `today` | Today view |
| `anytime` | Anytime view (triaged, no date) |
| `someday` | Someday view (deferred) |
| `inbox` | Inbox (default for new tasks) |
| `none` | Strip dates without moving the task (edit only) |
| `YYYY-MM-DD` | Future date → Upcoming; today/past → Today |

#### `deadline` — Hard deadlines

| Value | Effect |
|-------|--------|
| `YYYY-MM-DD` | Set a hard deadline |
| `none` | Clear an existing deadline (edit only) |

#### `repeat` — Recurring tasks

| Value | Effect |
|-------|--------|
| `daily` | Every day |
| `weekly` | Every week |
| `monthly` | Every month |
| `yearly` | Every year |
| `every N days/weeks/months/years` | Custom interval |
| `... until YYYY-MM-DD` | End recurrence on that date |
| `... after completion` | Repeat-after-completion mode |
| `none` | Clear recurrence (edit only) |

</details>

## Sync correctness: deletions, cascades, and the `Task7` bucket

The sync layer mirrors Things Cloud history into SQLite. Three classes of event
were mishandled; all three are fixed, and an existing database can be repaired
in place with the [`rebuild`](#repairing-an-existing-database) command.

### 1. Deletions were ignored (`Task7`)

Items are routed by their `e` (entity kind) field, which Things Cloud versions:
`Task3` → `Task4` → `Task6` → `Task7`. Routing matched a hard-coded list of
kind names, so when Things began sending `Task7` **every task item of the new
kind fell through to `UnknownChange` and was never applied**. The most visible
consequence: emptying the Things trash emitted 354 deletions in one batch
(server index 4694), 348 of which were `Task7` — they were logged with an empty
payload and the task rows were left in place.

Routing now classifies on the base kind name with the version suffix stripped
(`sync/kind.go`), so `Task7` — and any future `Task8` — routes correctly without
a code change. Deletions mark the row deleted and cascade to `task_tags` and
`checklist_items`.

### 2. Container changes did not cascade

Things applies container operations to everything inside the container, but
Things Cloud sends only the item for the container itself. Applying each item in
isolation left children live under a trashed or deleted parent. On the database
this was developed against, 66 to-dos (46 of them completed) sat untrashed under
a trashed project.

Cascades are now re-derived locally (`sync/cascade.go`), walking the hierarchy
transitively through both `project_uuid` and `heading_uuid`:

| Event | Effect on contents |
|---|---|
| `ProjectTrashed`, `TaskTrashed` (incl. headings) | `in_trash = 1` on all descendants |
| `ProjectRestored`, `TaskRestored` | `in_trash = 0` on all descendants |
| `ProjectDeleted`, `HeadingDeleted`, `TaskDeleted` | descendants marked deleted |
| `AreaDeleted` | contents kept, their area reference cleared — see below |
| `ProjectCompleted` | **no cascade** (see below) |

Cascades are applied to the materialized tables but are deliberately **not**
written to `change_log`: the log stays a faithful record of what Things Cloud
actually sent, so replaying it re-derives the cascade instead of double-applying
a stale one.

Two judgement calls worth knowing about:

- **`ProjectCompleted` does not cascade status.** Completing a project in Things
  logs the project but leaves its unfinished to-dos alone, so neither `status`
  nor `in_trash` is propagated. (Trashing that project later *will* cascade, and
  completed children are trashed along with everything else.)
- **`AreaDeleted` keeps the area's contents** and clears their area reference.
  This was initially implemented the other way round, on the assumption that
  Things deletes an area's contents along with it. The history says otherwise:
  across the `AreaDeleted` events in a real account, **11 of the 13 items filed
  under a deleted area received no further item of any kind** — no delete, no
  trash, no update. Things pushes its own actions to the cloud, so had it
  deleted those items it would have sent deletions for them. It did not. An
  area's contents outlive the area, holding a reference to a container that no
  longer exists; clearing that reference keeps the rows and leaves no dangling
  pointer.

  The other behaviour is still available if a Things version is found to
  propagate area deletions:

  ```go
  syncer.SetAreaDeleteMode(sync.AreaDeleteRemovesContents)
  ```

  or `rebuild -delete-area-contents`.

### 3. `UnknownChange` was too broad

Things Cloud sends partial updates, and an update that produced no *named*
semantic change used to vanish silently. Payload fields are now classified
explicitly (`sync/fields.go`):

- **applied** — written to the row. If no named change results, the update is
  recorded as `TaskFieldsUpdated` with the field list, so it is still visible.
  `{"ti":N}` (Today reorder) and `{"icsd":...}` (a repeating task's
  next-instance date) are the common cases; `icsd` was previously parsed but
  never written, making those updates no-ops. It now has its own column.
- **recognised no-ops** — part of the wire format, deliberately not
  materialized (`xx`, `rr`, `lt`, `icp`, `sb`, …). No change is emitted.
- **genuinely unknown** — reported as `UnknownChange` *naming the fields*, so
  the parser can be extended. An unparsable payload is likewise reported rather
  than aborting the whole sync batch.

`Settings*` and `Command*` items (the mail-to-inbox envelopes, whose effect also
arrives as an ordinary `Task` item) are now recognised and skipped rather than
counted as unknown. **`UnknownChange` now means genuinely unknown.**

### Repairing an existing database

`change_log` already holds every payload Things Cloud sent, so a database
written by the old sync layer can be repaired without re-pulling the history.
The `rebuild` command drops the materialized tables and replays `change_log`
from index 0. It needs no network access and no Things credentials — only the
database file.

```bash
go run ./cmd/rebuild -diagnose            # report problems, change nothing
go run ./cmd/rebuild                      # repair ~/.things-workflow/sync.db
go run ./cmd/rebuild -db /path/to/sync.db
go run ./cmd/rebuild -delete-area-contents # delete contents of deleted areas
```

`change_log` and `sync_state` are left untouched, so the sync cursor survives
and the next `Sync()` picks up where it left off. The replay reuses the same
`processItem` path as a live sync, so a rebuild and a fresh sync cannot drift
apart, and it is idempotent.

Two things a replay cannot do:

- **It does not relabel historic `change_log` rows.** Rows recorded as
  `UnknownChange` by the old parser stay that way — the log is immutable. The
  `-diagnose` output separates these ("legacy ... now parseable") from rows this
  parser still cannot classify, which is the number that should be zero.
- **It cannot resolve a reference to an entity that has no create event in the
  retained history.** Things Cloud does not keep history forever. Where an old
  payload references an area created before the retained window, the replay
  keeps the reference faithfully and `-diagnose` counts it as unresolved — on
  the account this was developed against, 2 such tasks reference areas with
  zero events in the log. This is distinct from a deleted area, whose contents
  do get their reference cleared.

### Schema

`schema_version` is **5**. Migration 5 is additive and, unlike migration 4, does
**not** clear the cache — repair is via `rebuild` instead:

- `change_log.item_kind`, `change_log.item_action` — the raw Things Cloud
  envelope (`e` and `t`), so the log can be replayed verbatim. Rows written
  before v5 have these as `NULL` and their envelope is inferred on replay from
  `change_type` plus the payload. That inference needs `change_type`, because
  the payload alone is ambiguous: an empty `{}` payload means a deletion on an
  `UnknownChange` row but *not* on a `TaskCreated` row, which also occurs.
- `tasks.instance_creation_start_date` — the `icsd` field.

Column additions are idempotent (`ALTER TABLE` guarded by `pragma_table_info`),
so a partially-applied migration no longer hard-fails on reopen.

The migration is **backward compatible**: a v5 database still works under the
pre-fix code, verified by running the previous `sync` package against a migrated
database — open, every read query, and the write path all succeed with matching
row counts. Older code sees `version >= 4` and skips migrating, and it selects
explicit column lists, so the added columns are inert to it.

### UUID validation accepts legacy hyphenated IDs

Write validation rejected anything that was not a Things Base58 UUID. Things
Cloud still holds **uppercase hyphenated UUIDs** (8-4-4-4-12, e.g.
`A1B2C3D4-E5F6-4789-8ABC-DEF012345678`) for items imported around 2020, and the
hyphens — along with characters Base58 excludes, such as `0`, `O` and `I` —
failed the check. Those items were unaddressable through the MCP tools even
though the server accepts operations on them. On the database this was developed
against that was 589 tasks, 13 areas, 7 tags and 41 checklist items.

Validation now accepts either form (`IsValidItemUUID`, in `uuid.go`, shared by
the MCP server and `things-cli`). Lowercase hyphenated IDs are accepted too,
since they appear in mail-to-inbox payloads. Malformed IDs are still rejected.

## Connecting MCP clients

The server speaks **Streamable HTTP** MCP at `POST /mcp`. Any MCP-capable
client can connect — the transport is not Claude-specific.

### Authenticating: `MCP_TOKEN`

`/mcp` is **unauthenticated by default**, on the assumption that the endpoint is
gated somewhere else: an identity-aware proxy, a private network, or a
local-only bind. Set `MCP_TOKEN` to require a bearer token instead:

```bash
MCP_TOKEN=$(openssl rand -hex 32)
```

With it set, every request to `/mcp` must carry
`Authorization: Bearer $MCP_TOKEN`; without it, behaviour is unchanged.

`MCP_TOKEN` is deliberately **separate from `API_KEY`**. `API_KEY` guards the
REST surface, and making it an MCP requirement too would silently break any
already-working connector that relies on an edge gate rather than a header.

This is a single shared secret, which suits the single-user deployment this
server is built for. **It identifies nobody.** Do not hand one token to several
people and treat it as per-user access — a multi-tenant deployment needs real
per-user identity (OAuth 2.1 with authorization code + PKCE, as the MCP
specification describes), not a shared token.

### Client examples

Every non-browser client authenticates with a bearer header, which is what
`MCP_TOKEN` exists for.

**Claude Code**

```bash
claude mcp add --transport http things https://your-host/mcp \
  --header "Authorization: Bearer $THINGS_MCP_TOKEN"
```

Or in `.mcp.json`, so the token stays out of git:

```json
{ "mcpServers": { "things": { "type": "http", "url": "https://your-host/mcp",
    "headers": { "Authorization": "Bearer ${THINGS_MCP_TOKEN}" } } } }
```

**Codex, Cursor, VS Code (Copilot), Windsurf** — all support a remote HTTP MCP
server with a bearer token read from an environment variable. Codex uses a
`bearer_token_env_var` setting; the others take a `headers` map with `${env:…}`
interpolation. Check your client's current docs for the exact key, as these
have been changing.

**Hosted connectors** (Claude.ai, ChatGPT, Gemini Enterprise) call from the
vendor's cloud, so the URL must be reachable from the public internet. They
generally expect the MCP OAuth flow rather than a static header; this server is
not an OAuth authorization server, so pair it with an identity-aware proxy and
leave `MCP_TOKEN` unset.

**Running it locally** sidesteps all of the above: start the server on
`127.0.0.1` and point your client at `http://localhost:8080/mcp`. Your Things
credentials never leave the machine, which is the lowest-risk way to use this.

### One server, several clients

Pointing several clients at a single instance is fine — writes and syncs are
serialised internally. Do **not** run two server processes against the same
`things.db`: that is cross-process SQLite contention and you will get
`SQLITE_BUSY`. Separate instances need separate database files.

## Operational Notes

- **Startup is fail-fast.** The server now requires the initial sync to succeed before it starts serving REST or MCP traffic.
- **Shutdown is graceful.** `SIGINT` and `SIGTERM` trigger an HTTP shutdown with a timeout instead of dropping in-flight requests immediately.
- **Read surfaces support pagination.** REST list endpoints and the corresponding MCP list tools accept optional `limit` and `offset` values.
- **Old `tir`/`sr` cache corruption is repaired automatically.** Opening a pre-fix sync database triggers a one-time local cache reset and full resync from Things Cloud history.
- **Soft-deleted rows are purged from the local cache after sync.** This keeps the SQLite mirror from growing forever while preserving normal sync semantics.

## Getting started

See **[docs/installation.md](docs/installation.md)** for full setup instructions. The short version:

1. Clone the repo and deploy to Fly.io
2. Set your Things Cloud credentials as Fly secrets
3. Add the MCP URL as a custom connector in Claude.ai
4. Create a Skill file for your workflow

### Privacy and cost

Your credentials are stored as encrypted secrets on your own Fly.io account — you're not giving them to anyone. Fly.io doesn't bill you if your monthly usage is under $5. After 2+ months of active development and daily use, the author has not been billed.

### Test with a separate account

Since this uses the Things Cloud sync protocol directly, consider creating a separate Things Cloud account to experiment with before pointing it at your main account.

## Skills

The MCP server gives Claude the ability to interact with Things, but to get the most out of it you need a **Claude Skill file** — a small instruction file that teaches Claude your specific projects, tags, and workflows.

See **[docs/skills.md](docs/skills.md)** for a step-by-step guide to creating your own.

## REST API

All `/api/*` endpoints require `Authorization: Bearer <API_KEY>` when `API_KEY` is set.

See **[docs/endpoints-and-things-cloud.md](docs/endpoints-and-things-cloud.md)** for how the REST and MCP surfaces sync from Things Cloud history, answer reads from the local SQLite mirror, and commit writes back to the cloud.

| Endpoint | Description |
|----------|-------------|
| `GET /` | Health check |
| `GET /api/verify` | Verify Things Cloud credentials |
| `GET /api/sync` | Trigger sync, returns change count |
| `GET /api/tasks/today` | Tasks scheduled for today. Optional query params: `limit`, `offset` |
| `GET /api/tasks/inbox` | Tasks in the inbox. Optional query params: `limit`, `offset` |
| `GET /api/tasks/anytime` | Tasks in Anytime. Optional query params: `limit`, `offset` |
| `GET /api/tasks/someday` | Tasks in Someday. Optional query params: `limit`, `offset` |
| `GET /api/tasks/upcoming` | Tasks in Upcoming. Optional query params: `limit`, `offset` |
| `GET /api/projects` | All projects. Optional query params: `limit`, `offset` |
| `GET /api/areas` | All areas. Optional query params: `limit`, `offset` |
| `GET /api/tags` | All tags. Optional query params: `limit`, `offset` |
| `POST /api/tasks/create` | Create a task |
| `POST /api/tasks/edit` | Edit a task |
| `POST /api/tasks/complete` | Complete a task |
| `POST /api/tasks/trash` | Trash a task |

## SDK

The underlying Go SDK can be used directly as a library. See **[docs/sdk.md](docs/sdk.md)** for documentation including:

- Getting started and quick start guide
- CLI tool (`things-cli`) with create, edit, complete, trash, batch commands
- Working with histories and items
- Persistent sync engine with 40+ semantic change types
- Wire format notes from reverse engineering

## Testing

113 integration tests across 5 test suites:

```bash
./tests/test-smoke.sh          # Core read/write workflow (11 checks)
./tests/test-mcp.sh 010        # All MCP write tools (44 checks)
./tests/test-mcp-read.sh       # All MCP read tools (29 checks)
./tests/test-mcp-protocol.sh   # JSON-RPC handshake and error handling (11 checks)
API_KEY=your-key ./tests/test-api.sh  # All REST endpoints (18 checks)
```

## Local development

```bash
go build -v -o things-server ./server/
export THINGS_USERNAME='...' THINGS_PASSWORD='...'
mkdir -p /data
./things-server

go test -v ./...
```

## Credits

Built on top of [arthursoares/things-cloud-sdk](https://github.com/arthursoares/things-cloud-sdk), which reverse-engineered the Things Cloud sync protocol.
