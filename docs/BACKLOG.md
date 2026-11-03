# Backlog

Open items only. Completed entries are not kept here — the git history and the
dated notes in this directory record what was done.

For anything substantial, prefer a GitHub issue; this file is for protocol
questions that have not been answered yet.

## Investigate tag/area deletion via `Tombstone2`

Areas and tags cannot currently be deleted through the MCP tools. The SDK has a
`Tombstone2` entity for explicit deletion — test whether writing one for a tag
or area UUID actually deletes it server-side.

Worth noting what is already known: `Tombstone2` creates *are* what Things
sends alongside task deletions (6 of them in the trash-emptying batch analysed
in [crash-investigation.md](crash-investigation.md#4-task6-delete-with-empty-payload-resolved-2026-11-02--not-a-bug)),
and the sync engine already applies them via `processTombstone`. So the read
side is handled; what is untested is writing one.
