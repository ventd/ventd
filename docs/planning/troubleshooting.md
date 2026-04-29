# Troubleshooting workflow — fast diagnosis for CI and local failures

This guide replaces the "screenshot terminal output → paste into chat
→ get told to run a different command → repeat" loop that wasted ~5
round-trips during the v0.4.1 release-pipeline debug.

## TL;DR

Two scripts, two skills, one rule.

| Symptom | Run | One paste of output goes to |
|---|---|---|
| CI run failed (PR check, tag push, branch CI) | `./scripts/triage-run.sh` | Claude.ai chat |
| About to push a branch, want to verify | `./scripts/verify-local.sh` | Claude.ai chat (only if anything red) |

**The rule:** never iterate `gh run view` commands. Run the triage
script once, paste the output once.

## Why the old loop was expensive

Real example from the v0.4.1 release-pipeline debug:

1. User: "release failed" (no detail)
2. Claude: "send `gh run view <id> --log-failed | tail -150`"
3. User: pastes 150 lines of irrelevant SLSA wrapper output
4. Claude: "wrong job — send `gh run view <id> --json jobs`"
5. User: pastes job list
6. Claude: "ok, the failing job is `provenance / final` — send
   `gh run view --job=<id> --log-failed`"
7. User: pastes more log
8. Claude: "this is the SLSA aggregator inheriting upstream failure —
   send `gh run view <id> --json jobs --jq '.jobs[] | {name,
   conclusion}'`"
9. User: pastes
10. Claude: "all upstream succeeded, this is cosmetic, run
    `gh release view v0.4.1 --json assets --jq '.assets | length'`"
11. User: pastes "34"
12. Claude: "release shipped, ignore the red"

Twelve turns. Should have been three.

## The triage script — `./scripts/triage-run.sh`

One command produces the entire diagnostic:

- Run metadata (workflow, branch, event, conclusion, URL)
- Job-state matrix (every job + its conclusion in a tab-aligned table)
- Per-failed-job error-line greps + last 40 lines of failed-step log
- Release state if it's a tag-push event (asset count + names)
- Hint block — pattern-matches known failure shapes and pre-flags them

Output is bounded to ~100 lines. Fits one paste.

### Usage

```bash
cd ~/ventd
./scripts/triage-run.sh                  # current branch's latest failed run
./scripts/triage-run.sh 24929981579      # specific run id
./scripts/triage-run.sh --pr 627         # PR's head branch
./scripts/triage-run.sh --tag v0.4.1     # release pipeline for a tag
```

### What the hints catch automatically

The script flags these failure classes before you even paste:

- **Aggregator-only red** — only a `final` / `outcome` job failed
  while everything upstream succeeded. Hint: check release state,
  likely cosmetic. (This was the v0.4.1 SLSA `provenance / final`
  case.)
- **Stale path after `git mv`** — log shows "not found, skipping".
  Hint: `grep -rn '<old-path>'` to find every reference. (This was
  the v0.4.1 spec-06 PR 2 case where the workflow still pointed at
  the old `usr.local.bin.ventd` filename.)
- **SBOM/SLSA spec drift** — log shows `specVersion='1.X'`. Hint:
  generator auto-bumped, update the validator. (This was the v0.4.1
  CycloneDX 1.5 to 1.6 case.)
- **Permissions error** — log shows 403 or "Resource not accessible".
  Hint: workflow YAML missing `permissions:` block.
- **Setup-time failure** — job conclusion `failure` but log empty.
  Hint: re-run, possibly runner-pool issue.

## The verification script — `./scripts/verify-local.sh`

Replaces the multi-line copy-paste verification block that gets pasted
after every CC session.

Two of the most common mistakes in this workflow:

1. **Ran from `~` instead of `~/ventd`.** Every command failed with
   "not a git repository". Round-trip wasted on directory mismatch.
2. **Pager swallowed queued commands.** A `git log` or `git show` in
   the middle of a paste-block hits `less`, the rest of the queued
   commands vanish into the terminal buffer.

`verify-local.sh` eliminates both: always cds to repo root, always
uses `--no-pager` for git.

### Usage

```bash
cd ~/ventd     # or anywhere inside the repo
./scripts/verify-local.sh                                    # default
./scripts/verify-local.sh --against develop                  # different base branch
./scripts/verify-local.sh --skip-tests                       # faster, just structure
./scripts/verify-local.sh --paths 'TESTING.md|deploy/apparmor.d/ventd'
                                                             # extra existence checks
```

### What it covers

- Tree state (clean / dirty)
- Commit count + log since base branch
- `go test -race ./...`
- `tools/rulelint`
- Path existence checks (optional, for spec verification)
- **Drift detection** — finds stale references to files renamed in the
  last 10 commits

The drift detection alone would have caught the v0.4.1 post-rename CI
failure before push.

## Rule of thumb — when to fire CC vs hand-edit

The v0.4.1 ship surfaced a useful calibration point.

**Hand-edit on mobile is correct when:**
- Single-line change, single file, exact text known
- Editing config you've edited dozens of times before

**Fire CC ($0.30 to $2) is correct when:**
- More than 2 files
- More than 5 lines per file
- Any indentation-sensitive content (YAML, Python, deeply-nested JSON)
- Any chance of typo cascades (renaming a symbol that appears in many
  test files, for example)

The CycloneDX 1.5 to 1.6 fix was 4 lines in 1 file. It would have been
30 seconds hand-edited on a desktop. On mobile via Termius, the cost
of one wrong-indent disaster outweighed the $0.30 of CC. Fire CC.

## When to escalate to Claude.ai chat

After running `triage-run.sh`:

- **If the HINTS section pre-flagged the issue:** apply the suggested
  fix yourself. Don't bother chatting.
- **If the HINTS section is empty and the failure is opaque:** paste
  the full triage output to Claude.ai. The bounded ~100 lines fit
  one message and contain everything needed.
- **If the hint matched but you don't trust the diagnosis:** paste
  output, ask for second opinion. (Cheap — same one paste.)

After running `verify-local.sh`:

- **If SUMMARY is all green:** push. Don't chat.
- **If anything red:** paste the SUMMARY block + the relevant section
  to Claude.ai. Usually 10 lines.

## Maintaining the scripts

Both scripts are kept under `scripts/` in the repo. They're plain bash
with `set -euo pipefail`. To extend:

- **Adding a hint to triage-run.sh:** append a new `if` block to the
  HINTS section. Each hint is independent, can match the failure log
  via grep, and prints a one-line suggestion. Adding hints over time
  is exactly how the script gets smarter without becoming brittle.
- **Adding a check to verify-local.sh:** the existing structure
  (numbered sections, summary at the bottom) is easy to extend.
  Anything that's a yes/no answer about the local state belongs here.

When a new failure class burns more than 3 round-trips, add a hint for
it and commit. The script accretes pattern-recognition the same way
the `.claude/rules/*.md` files accrete invariants.

## File inventory shipped by this bundle

```
scripts/
├── triage-run.sh         # CI failure diagnosis
└── verify-local.sh       # pre-push verification

.claude/skills/
├── ci-triage/
│   └── SKILL.md          # CC skill mapping to triage-run.sh
└── ci-verify-local/
    └── SKILL.md          # CC skill mapping to verify-local.sh

docs/
└── troubleshooting.md    # this file
```

Drop the `scripts/` files into `~/ventd/scripts/`, drop the
`.claude/skills/` directories into `~/ventd/.claude/skills/`, drop
this doc into `~/ventd/docs/troubleshooting.md`. Commit them under a
single `chore(scripts)` PR.
