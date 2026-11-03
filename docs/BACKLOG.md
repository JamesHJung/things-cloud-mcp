# Backlog

Open items only. Completed entries are not kept here — the git history and the
dated notes in this directory record what was done.

For anything substantial, prefer a GitHub issue; this file is for protocol
questions that have not been answered yet.

## Confirm whether a legacy identifier is still writable

Resolved for reads: objects predating Things Cloud's identifier migration are
now keyed by their derived identifier, which the Things app confirms is the
current one. See
[2026-11-03-legacy-identifier-derivation.md](2026-11-03-legacy-identifier-derivation.md).

What is left is the write side. Write validation accepts legacy hyphenated
identifiers, which was right when the database handed them out. It no longer
does, so nothing should be generating them — the relaxation is harmless but
probably pointless. One live write to a known legacy identifier would settle
whether Things updates the object, ignores it, or creates a phantom.

## Investigate tag/area deletion via `Tombstone2`

Areas and tags cannot currently be deleted through the MCP tools. The SDK has a
`Tombstone2` entity for explicit deletion — test whether writing one for a tag
or area UUID actually deletes it server-side.

Worth noting what is already known: `Tombstone2` creates *are* what Things
sends alongside task deletions (6 of them in the trash-emptying batch analysed
in [crash-investigation.md](crash-investigation.md#4-task6-delete-with-empty-payload-resolved-2026-11-02--not-a-bug)),
and the sync engine already applies them via `processTombstone`. So the read
side is handled; what is untested is writing one.
