# Things Cloud

A Go server and SDK for the [Things 3](https://culturedcode.com/things/) cloud API. Gives [Claude](https://claude.ai) (and other MCP-compatible AI assistants) full read/write access to your task manager.

Built on a reverse-engineered, unofficial SDK — there is no official API documentation from Cultured Code.

> **Where this came from.** This started as a copy of
> [`mattydsmith/things-cloud-mcp`](https://github.com/mattydsmith/things-cloud-mcp),
> which was archived in March 2026 — it accepts no pull requests, and two
> submitted fixes are stranded there permanently. It has since diverged enough
> to stand on its own: the sync layer's handling of deletions, container
> cascades and partial updates is fixed (see
> [Correctness notes](#correctness-notes)),
> the Today view no longer breaks outside UTC, there is a repair command for
> existing databases, and `/mcp` can require a bearer token so non-browser MCP
> clients can connect. It is maintained — issues and pull requests welcome.
>
> The sync engine is tracked against
> [`arthursoares/things-cloud-sdk`](https://github.com/arthursoares/things-cloud-sdk),
> which is still active; see
> [Staying in sync with the upstream SDK](#staying-in-sync-with-the-upstream-sdk).
> Full lineage and licensing in [Credits](#credits).

## What it does

The server syncs your Things Cloud data into a local SQLite database and exposes it through two interfaces:

- **MCP Endpoint** (`/mcp`) — [Model Context Protocol](https://modelcontextprotocol.io/) for AI assistants like Claude
- **REST API** (`/api/*`) — Bearer token auth, for scripts and apps

Changes you make in the Things app show up immediately. Tasks Claude creates appear in Things within seconds. No restart required.
The server performs an initial sync at startup and now exits if it cannot establish a clean local snapshot.

```
┌──────────────┐     ┌──────────────────┐     ┌──────────────┐
│  Things App  │────▶│  Things Cloud    │◀────│  API Server  │
│  (Mac/iOS)   │     │  (Cultured Code) │     │ (self-hosted)│
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

## Getting started

See **[docs/installation.md](docs/installation.md)** for full setup instructions. The short version:

1. Clone the repo
2. Set `THINGS_USERNAME` and `THINGS_PASSWORD`, and mount a writable `/data`
3. Run it — locally with `go run ./server/`, or as a container on a NAS or VM
4. Point your MCP client at `/mcp` (see [Connecting MCP clients](#connecting-mcp-clients))

It is a single static binary with an embedded SQLite database, so it runs
anywhere with a writable directory. There is nothing to provision and no
managed service involved.

### Privacy

You run this yourself, so your Things credentials stay on hardware you control.
Keep them in environment variables or an uncommitted `.env`, never in a compose
file.

Worth knowing what that credential is: Things Cloud has no tokens or scopes, so
the SDK sends your **account password** on every request, and the same
credential can change your password or delete your account. Running locally,
where it never leaves your machine, is the lowest-risk option.

### Test with a separate account

Since this uses the Things Cloud sync protocol directly, consider creating a separate Things Cloud account to experiment with before pointing it at your main account.

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

### Local, over stdio

The lowest-risk way to use this. The client launches the server as a child
process and talks to it over a pipe — no port, no network, no token, and your
Things credentials never leave the machine.

```bash
go build -o things-mcp ./server/
```

**Claude Code**

```bash
claude mcp add things -- /path/to/things-mcp -stdio
```

Any MCP client that launches local servers takes the same shape — a command and
its arguments, with the credentials in the environment:

```json
{ "mcpServers": { "things": {
    "command": "/path/to/things-mcp",
    "args": ["-stdio"],
    "env": { "THINGS_USERNAME": "...", "THINGS_PASSWORD": "..." } } } }
```

The database defaults to `~/.things-cloud-mcp/things.db` outside a container;
`THINGS_DB_PATH` overrides it. Startup syncs before serving, so the first launch
takes a few seconds and later ones are immediate.

In this mode stdout carries the protocol and nothing else — logs go to stderr,
where your client surfaces them.

**Running the HTTP server locally** also works if you prefer it: start it and
point your client at `http://localhost:8080/mcp`.

### One server, several clients

Pointing several clients at a single instance is fine — writes and syncs are
serialised internally. Do **not** run two server processes against the same
`things.db`: that is cross-process SQLite contention and you will get
`SQLITE_BUSY`. Separate instances need separate database files.

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

## Correctness notes

This started from an archived upstream whose sync layer mishandled three classes
of event. All three are fixed, and an existing database is repaired in place by
`cmd/rebuild` — it replays `change_log`, so it needs no network access and no
Things credentials.

- **Deletions were ignored.** Items are routed by their `e` field, which Things
  Cloud versions (`Task3` → `Task4` → `Task6` → **`Task7`**). Routing matched a
  hard-coded list, so when Things began sending `Task7`, every task item of the
  new kind fell through unapplied — including an entire trash-emptying batch of
  348 deletions. Routing now classifies on the base kind name with the version
  suffix stripped, so a future `Task8` works without a code change.
- **Container changes did not cascade.** Things applies a container operation to
  its contents, but Things Cloud sends an item only for the container. Trash,
  restore and delete now walk the hierarchy transitively through both
  `project_uuid` and `heading_uuid`.
- **`UnknownChange` was too broad.** Partial updates that produced no named
  semantic change used to vanish. Payload keys are now classified as applied,
  recognised-no-op, or genuinely unknown — and unknown ones are reported *by
  name*, so the label is actionable.
- **The Today view was wrong outside UTC.** Things stores date fields as
  date-only values at midnight UTC, so the day window must come from the local
  calendar date expressed at UTC midnight. Using the UTC calendar date put it a
  day ahead for eight hours a day at UTC-8.
- **Generated identifiers were not canonical Base58.** Leading zero bytes were
  dropped, producing an identifier Things cannot decode to 16 bytes for roughly
  1 UUID in 256 — which corrupts the sync history.

Full analysis, with the measured payload distributions and the SQL that
demonstrates each defect:
[docs/2026-11-02-sync-deletion-cascade-fix.md](docs/2026-11-02-sync-deletion-cascade-fix.md).

**One known, unfixed defect:** objects predating Things Cloud's identifier
migration are stored under two different keys, so some tasks exist as torn
pairs — title on one row, current status on the other. Diagnosed and measured,
deliberately not yet fixed:
[docs/2026-11-03-legacy-identifier-derivation.md](docs/2026-11-03-legacy-identifier-derivation.md).

## Operational Notes

- **Startup is fail-fast.** The server now requires the initial sync to succeed before it starts serving REST or MCP traffic.
- **Shutdown is graceful.** `SIGINT` and `SIGTERM` trigger an HTTP shutdown with a timeout instead of dropping in-flight requests immediately.
- **Read surfaces support pagination.** REST list endpoints and the corresponding MCP list tools accept optional `limit` and `offset` values.
- **Old `tir`/`sr` cache corruption is repaired automatically.** Opening a pre-fix sync database triggers a one-time local cache reset and full resync from Things Cloud history.
- **Soft-deleted rows are purged from the local cache after sync.** This keeps the SQLite mirror from growing forever while preserving normal sync semantics.

## Local development

```bash
go build -v -o things-server ./server/
export THINGS_USERNAME='...' THINGS_PASSWORD='...'
mkdir -p /data
./things-server

go test -v ./...
```

## Testing

113 integration tests across 5 test suites:

```bash
./tests/test-smoke.sh          # Core read/write workflow (11 checks)
./tests/test-mcp.sh 010        # All MCP write tools (44 checks)
./tests/test-mcp-read.sh       # All MCP read tools (29 checks)
./tests/test-mcp-protocol.sh   # JSON-RPC handshake and error handling (11 checks)
API_KEY=your-key ./tests/test-api.sh  # All REST endpoints (18 checks)
```

## Staying in sync with the upstream SDK

The sync engine originates in
[`arthursoares/things-cloud-sdk`](https://github.com/arthursoares/things-cloud-sdk),
which is actively maintained. The two histories are **unrelated** — the code was
copied here rather than forked — and the trees have since diverged (different
schema versions, different file sets, an MCP server layer that exists only
here). So upstream changes are **read, assessed and ported**, never merged.

`.github/upstream-sdk-synced` records the upstream commit this fork has been
reviewed against. To see what is new since then:

```bash
scripts/check-upstream-sdk.sh            # grouped, fixes first
scripts/check-upstream-sdk.sh --markdown # issue-ready checklist
```

`.github/workflows/upstream-sdk-check.yml` runs that weekly and keeps a single
tracking issue up to date, labelled `upstream-sdk`. When the listed commits have
been worked through, move the SHA in the marker file forward and say in the
commit message what was applied and what was rejected.

Each upstream commit needs a *does this fork actually have this bug?* check
before anything is applied. That is not a formality — upstream's own fix for the
Today-view date handling is correct for `isToday` and **wrong** for the day-bounds
query at negative UTC offsets, so porting it wholesale would have introduced a
worse bug than the one it fixed. See
[Correctness notes](#correctness-notes).

## Credits

Four layers of other people's work sit under this, each copied forward rather
than forked, which is why none of the histories connect:

| | |
|---|---|
| [**nicolai86/things-cloud-sdk**](https://github.com/nicolai86/things-cloud-sdk) | Raphael Randschau, 2017. Reverse-engineered the Things Cloud sync protocol and wrote the original Go SDK. The `LICENSE` in this repository is his. |
| [**arthursoares/things-cloud-sdk**](https://github.com/arthursoares/things-cloud-sdk) | Carried the SDK forward and added the persistent SQLite sync engine. Still actively maintained. |
| [**mattydsmith/things-cloud-mcp**](https://github.com/mattydsmith/things-cloud-mcp) | Added the MCP server and REST layer on top of the SDK. Archived March 2026. |
| **this repository** | Sync correctness fixes, the database repair command, MCP authentication, and ongoing maintenance. |

Licensed MIT throughout. The original copyright notice is preserved in
[`LICENSE`](LICENSE) as that licence requires.

Things 3 and Things Cloud are products of [Cultured Code](https://culturedcode.com/).
This project is unofficial, unaffiliated, and built on a reverse-engineered API
with no vendor support or guarantees.
