# Installation Guide

Self-host the Things Cloud MCP server to connect Claude (or any MCP-compatible AI assistant) to your Things 3 task manager.

## Prerequisites

- A [Things 3](https://culturedcode.com/things/) account with Things Cloud sync enabled
- Either [Go 1.26+](https://go.dev/dl/) to build and run directly, or Docker to
  run the container

The server is a single static binary with an embedded SQLite database, so it
runs anywhere: a NAS, a small VM, or just your own machine. There is nothing
to provision beyond a writable directory for the database.

## 1. Clone the repository

```bash
git clone https://github.com/<your-account>/things-cloud-mcp.git
cd things-cloud-mcp
```

## 2. Build locally (optional, to verify)

```bash
go build -v ./...
go test -v ./...
```

Run the server locally to confirm everything works:

```bash
export THINGS_USERNAME='your-things-email'
export THINGS_PASSWORD='your-things-password'
mkdir -p /data
go run ./server/
```

The server starts at `http://localhost:8080`. Verify with:

```bash
curl http://localhost:8080/
# {"service":"things-cloud-api","status":"ok"}
```

## 3. Run it

Pick whichever fits. All three want the same three things: your credentials in
the environment, a writable path for the database, and port 8080 reachable by
your MCP client.

### Locally, over stdio

The lowest-risk option and the one to prefer if you only use this yourself.
The client launches the server as a child process and talks to it over a pipe,
so there is no port, no network exposure and nothing to authenticate, and your
credentials never leave the machine.

```bash
go build -o things-mcp ./server/
claude mcp add things -- "$PWD/things-mcp" -stdio
```

The database defaults to `~/.things-cloud-mcp/things.db`; set `THINGS_DB_PATH`
to put it elsewhere. Logs go to stderr, because stdout carries the protocol.

### Locally, over HTTP

If you would rather run it as a server:

```bash
export THINGS_USERNAME='your-things-email'
export THINGS_PASSWORD='your-things-password'
sudo mkdir -p /data && sudo chown "$USER" /data
go run ./server/
```

Point your client at `http://localhost:8080/mcp`.

### Docker

```bash
docker build -t things-cloud-mcp .
docker run -d --name things-mcp \
  -e THINGS_USERNAME='your-things-email' \
  -e THINGS_PASSWORD='your-things-password' \
  -e MCP_TOKEN="$(openssl rand -hex 32)" \
  -v /srv/things-mcp/data:/data \
  -p 127.0.0.1:8080:8080 \
  things-cloud-mcp
```

The image is ~14 MB and also ships `things-rebuild`, the one-off database
repair tool (`docker exec things-mcp things-rebuild -diagnose`).

Binding to `127.0.0.1` keeps it off the network. Publish it more widely only
behind something that authenticates — see **Exposing it** below.

### Docker Compose (NAS, VM)

```yaml
services:
  things-mcp:
    build:
      context: https://github.com/JamesHJung/things-cloud-mcp.git#main
    container_name: things-mcp
    restart: unless-stopped
    environment:
      THINGS_USERNAME: ${THINGS_USERNAME}
      THINGS_PASSWORD: ${THINGS_PASSWORD}
      MCP_TOKEN: ${MCP_TOKEN}      # bearer token for /mcp; see Environment variables
      PORT: "8080"
    volumes:
      - ./data:/data
    ports:
      - "127.0.0.1:8080:8080"
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:8080/"]
      interval: 60s
      timeout: 5s
      retries: 3
      start_period: 60s   # the initial sync must finish or the process exits
```

Put the secrets in a `.env` beside it. On Synology Container Manager, note that
**Clean —> Build does not reliably replace a running container** — delete the
project and recreate it when you need a new image to actually take effect.

### Verify

```bash
curl http://localhost:8080/
# {"service":"things-cloud-api","status":"ok"}
```

Startup is fail-fast: the process exits if the initial sync fails, so if it is
running, it synced. A first sync of a few thousand history items takes a few
seconds.

### Exposing it

`/mcp` is unauthenticated unless you set `MCP_TOKEN`. Before reaching it from
anywhere but localhost, do one of:

- set `MCP_TOKEN` and have the client send `Authorization: Bearer <token>`, or
- put it behind an identity-aware proxy (Cloudflare Tunnel + Access, Tailscale,
  a VPN), or
- keep it bound to `127.0.0.1` and run your client on the same host.

Hosted connectors such as Claude.ai call from the vendor's cloud, so they need
a publicly reachable URL — pair that with a proxy that authenticates.

## 4. Connect to Claude

### Claude.ai (web)

1. Go to **Settings > Connectors > Add custom connector**
2. Set the URL to `https://your-host/mcp`
3. Leave authentication fields empty (the MCP endpoint has no auth)
4. Save

Then ask Claude: *"What's on my Things today?"*

### Claude Code (CLI)

Add the server to your Claude Code MCP config (`~/.claude/mcp.json` or project-level):

```json
{
  "mcpServers": {
    "things": {
      "type": "url",
      "url": "https://your-host/mcp"
    }
  }
}
```

## 5. Available tools

Once connected, Claude has access to 36 tools:

**Read** — List tasks by view (today, inbox, anytime, someday, upcoming, all), by project, by area. Get individual tasks, areas, tags. List completed tasks with date filters. List checklist items.

**Write** — Create tasks, projects, areas, tags, headings, and checklist items. Edit tasks (title, notes, dates, project, area, tags, recurrence). Complete, uncomplete, trash, and restore tasks.

**Search** — Case-insensitive substring search across task titles and notes.

**Diagnostic** — Built-in smoke test that exercises the full create/read/edit/complete/trash cycle.

## Infrastructure notes

- The database is a *cache* of Things Cloud, not a source of truth. Deleting it and restarting rebuilds it from history in a few seconds
- SQLite with WAL mode lives at `/data/things.db`; mount that path to persist it
- The server syncs incrementally from Things Cloud before every read, so changes made in the Things app are immediately visible
- Writes use event-sourced sync with automatic retry on 409 conflicts (race with the Things app)

## Privacy and credentials

The server needs your Things Cloud email and password to sync your tasks. You run it on your own hardware, so those credentials stay on infrastructure you control and are not shared with anyone — keep them in environment variables or a `.env` that is not committed, never in the compose file itself.

**Protect the MCP endpoint.** `/mcp` has no authentication unless you give it some, so by default anyone who learns your server URL can read and write your tasks. Set `MCP_TOKEN` to require `Authorization: Bearer <token>`:

```bash
MCP_TOKEN="$(openssl rand -hex 32)"
```

`API_KEY` does **not** cover `/mcp` — it only guards the REST surface. The two are separate on purpose, so that turning one on cannot silently break a working connector that authenticates at the network edge instead of with a header.

Note that a shared token identifies nobody. It is right for a single-user deployment; it is not a way to give several people their own access.

One more consideration: your Things Cloud password is sent on every API call — the protocol has no tokens or scopes — and the same credential can change your password or delete your account. Treat it accordingly, and see "Test with a separate account" in the README before pointing this at your main one.

## Resource use

Shared CPU and a few hundred MB of RAM are plenty; the binary is ~14 MB and a
database of several thousand tasks is a couple of MB. It is idle except while
serving a request.

## Environment variables

| Variable | Required | Description |
|----------|----------|-------------|
| `THINGS_USERNAME` | Yes | Your Things account email |
| `THINGS_PASSWORD` | Yes | Your Things account password |
| `API_KEY` | No | Bearer token for REST API endpoints (`/api/*`). If unset, no auth required. Does not affect `/mcp`. |
| `MCP_TOKEN` | No | Bearer token required on `/mcp`. If unset, the MCP endpoint is unauthenticated. Not used in stdio mode. |
| `THINGS_DB_PATH` | No | Where to keep the SQLite database. Defaults to `/data/things.db` in a container, else `~/.things-cloud-mcp/things.db`. |
| `THINGS_API_ENDPOINT` | No | Override the Things Cloud endpoint. For pointing at a stub in tests; leave unset in normal use. |
| `PORT` | No | Server port (default: `8080`) |
| `DEBUG` | No | Set to `true` for verbose HTTP logging |

## Updating

```bash
git pull
# then rebuild and restart however you run it, e.g.
docker compose up -d --build
```

## Troubleshooting

**Server won't start:** Check the logs (`docker logs things-mcp`). Startup is fail-fast, and incorrect Things Cloud credentials are the usual cause.

**Sync errors:** The server retries automatically. Check the logs, and trigger a manual sync with `curl http://localhost:8080/api/sync` (requires `API_KEY` if set).

**Stale or wrong local state:** `things-rebuild` replays the change log to rebuild
the materialised tables, with no network access or credentials needed:
`docker exec things-mcp things-rebuild -diagnose` to look, without `-diagnose`
to repair.
