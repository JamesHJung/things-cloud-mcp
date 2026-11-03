# Backlog

Open items only. Completed entries are not kept here — the git history and the
dated notes in this directory record what was done.

For anything substantial, prefer a GitHub issue; this file is for protocol
questions that have not been answered yet.

## Legacy identifier derivation (known data defect)

Objects created before Things Cloud's identifier migration are stored under two
different keys, so 53 tasks on the account this was measured against exist as
torn pairs — title on one row, current status on the other. Diagnosed, measured
and **not fixed**: the fix changes ~589 task identifiers and calls the UUID
validation relaxation into question, so it needs a decision and a live write
test first.

Full analysis, including the derivation algorithm and what fixing it involves:
[2026-11-03-legacy-identifier-derivation.md](2026-11-03-legacy-identifier-derivation.md).

## Investigate tag/area deletion via `Tombstone2`

Areas and tags cannot currently be deleted through the MCP tools. The SDK has a
`Tombstone2` entity for explicit deletion — test whether writing one for a tag
or area UUID actually deletes it server-side.

Worth noting what is already known: `Tombstone2` creates *are* what Things
sends alongside task deletions (6 of them in the trash-emptying batch analysed
in [crash-investigation.md](crash-investigation.md#4-task6-delete-with-empty-payload-resolved-2026-11-02--not-a-bug)),
and the sync engine already applies them via `processTombstone`. So the read
side is handled; what is untested is writing one.
