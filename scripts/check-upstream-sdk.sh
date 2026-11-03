#!/usr/bin/env bash
# List upstream SDK commits this fork has not been reviewed against.
#
# The sync engine comes from arthursoares/things-cloud-sdk. The histories are
# unrelated (the code was copied, not forked) so there is nothing to merge —
# changes are read, assessed, and ported if they apply. .github/upstream-sdk-synced
# records how far that review has reached.
#
# Usage: scripts/check-upstream-sdk.sh [--markdown]
set -euo pipefail

UPSTREAM="${UPSTREAM_SDK_REPO:-arthursoares/things-cloud-sdk}"
MARKER_FILE="$(git rev-parse --show-toplevel)/.github/upstream-sdk-synced"
MARKDOWN=0
[[ "${1:-}" == "--markdown" ]] && MARKDOWN=1

if [[ ! -f "$MARKER_FILE" ]]; then
  echo "missing $MARKER_FILE" >&2
  exit 1
fi
MARKER="$(head -1 "$MARKER_FILE" | tr -d '[:space:]')"

if ! command -v gh >/dev/null 2>&1; then
  echo "gh CLI is required" >&2
  exit 1
fi

# compare/<base>...<head> gives both the count and the commit list in one call.
RESP="$(gh api "repos/${UPSTREAM}/compare/${MARKER}...HEAD" 2>/dev/null)" || {
  echo "could not compare ${MARKER}...HEAD on ${UPSTREAM}" >&2
  echo "(has the marker SHA been force-pushed away upstream?)" >&2
  exit 1
}

AHEAD="$(printf '%s' "$RESP" | python3 -c 'import json,sys; print(json.load(sys.stdin)["ahead_by"])')"

if [[ "$AHEAD" -eq 0 ]]; then
  if [[ "$MARKDOWN" -eq 1 ]]; then
    echo "NO_NEW_COMMITS"
  else
    echo "Up to date with ${UPSTREAM} (marker ${MARKER:0:7})."
  fi
  exit 0
fi

printf '%s' "$RESP" | MARKDOWN="$MARKDOWN" UPSTREAM="$UPSTREAM" MARKER="$MARKER" python3 -c '
import json, os, re, sys

data = json.load(sys.stdin)
md = os.environ["MARKDOWN"] == "1"
upstream, marker = os.environ["UPSTREAM"], os.environ["MARKER"]
commits = data["commits"]

# Fixes first: they are the ones most likely to apply to a diverged fork.
FIX = re.compile(r"^(fix|revert|security)\b|\bbug\b|\bpanic\b|\bcrash\b", re.I)
NOISE = re.compile(r"^(ci|chore|docs|test|style|refactor|build|deps)\b", re.I)

def bucket(subject):
    if FIX.search(subject):
        return "fix"
    if NOISE.match(subject):
        return "noise"
    return "other"

head_sha = commits[-1]["sha"]

groups = {"fix": [], "other": [], "noise": []}
for c in commits:
    subject = c["commit"]["message"].splitlines()[0]
    groups[bucket(subject)].append((c["sha"][:9], c["commit"]["committer"]["date"][:10], subject))

if md:
    print(f"`{len(commits)}` new commit(s) on [{upstream}](https://github.com/{upstream}) "
          f"since `{marker[:9]}`.\n")
    print("The sync engine here is ported from that repository, not merged — the "
          "histories are unrelated and the trees have diverged. Each item below needs "
          "a *does this fork actually have this bug?* check before anything is applied.\n")
    for key, title in (("fix", "Fixes and crashes — check these first"),
                       ("other", "Features and other changes"),
                       ("noise", "CI, docs, tests, refactors")):
        rows = groups[key]
        if not rows:
            continue
        print(f"### {title} ({len(rows)})\n")
        for sha, date, subject in rows:
            print(f"- [ ] [`{sha}`](https://github.com/{upstream}/commit/{sha}) {date} — {subject}")
        print()
    print(f"[Full comparison](https://github.com/{upstream}/compare/{marker}...HEAD)\n")
    print("When this has been worked through, move the SHA in "
          "`.github/upstream-sdk-synced` forward and close this issue.\n")
    # The head this listing describes. The workflow reads it back out of an
    # existing issue to tell "nothing new since last week" from "more arrived",
    # so it can stay silent in the first case and notify in the second.
    print(f"<!-- upstream-head: {head_sha} -->")
else:
    print(f"{len(commits)} new commit(s) on {upstream} since {marker[:9]}:\n")
    for key, title in (("fix", "FIXES"), ("other", "OTHER"), ("noise", "ci/docs/tests")):
        rows = groups[key]
        if not rows:
            continue
        print(f"  {title} ({len(rows)})")
        for sha, date, subject in rows:
            print(f"    {sha}  {date}  {subject}")
        print()
'
