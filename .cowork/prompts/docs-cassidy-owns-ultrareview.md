# docs-cassidy-owns-ultrareview

**Care level: HIGH.** This edits `.cowork/roles/cassidy/SYSTEM.md`,
which is the authoritative spec for the Cassidy role. Role-spec
drift has cascading effects because each role is a separately-booted
claude.ai Project whose custom system prompt mirrors the file on
`cowork/state`. Misaligned SYSTEM.md means the next Cassidy session
doesn't know her own lane.

Human-commanded change, explicitly requested in orchestrator turn.
Opens a PR against `cowork/state` for human review before merge.

## Goal

Move ownership of ultrareview audits from Atlas (who currently
dispatches them via `spawn_cc("ultrareview")`) to Cassidy (who
performs them as part of her standing audit lane). Codify the
trigger, the protocol, and the handoff.

## Context

Atlas historically ran ultrareviews by dispatching a CC session
against `.cowork/prompts/ultrareview.md`. That prompt describes a
12-check repo-wide audit (HAL contract, safety posture, rule file
integrity, dead code, duplication, coverage, API hygiene, binary
size, CHANGELOG, deps, config schema, docs drift). The work is
audit-only — no code changes — and the output is a markdown report
at `.cowork/reviews/ultrareview-<N>.md`.

That shape matches Cassidy's job description ("You read diffs after
they merge to main. You are skeptical by temperament. You are the
owner of quality.") better than Atlas's ("You do not write code. You
dispatch and merge."). The CC dispatch indirection was a leftover
from when Cassidy didn't exist as a role.

User turn (orchestrator session, 2026-04-18): "Cassidy is posting
ultrareview.2 we should make this Cassidys role." Filing this PR
encodes that instruction.

## What to do

### 1. Move the spec file

Rename `.cowork/prompts/ultrareview.md` to
`.cowork/roles/cassidy/ULTRAREVIEW.md`. The new path makes it a
Cassidy-owned spec, not a CC-dispatchable prompt.

Update the file header and framing to reflect the new audience
(Cassidy reading her own spec, not CC executing a one-shot prompt).
Specifically:

- Change "You are Claude Code running an ULTRAREVIEW" to
  "You are Cassidy, running a scheduled ULTRAREVIEW audit."
- Remove the "Model: Opus 4.7" line (Cassidy runs in her own
  claude.ai project; model selection is the project's concern, not
  a prompt directive).
- Remove the "spawn_cc alias convention" implications — keep the
  12 checks, keep the report template, keep the "audit-only, no
  code changes, no PRs" contract.
- Add a line at the top: "Triggered when: (a) >=10 PRs merged to
  `main` since the last ultrareview; (b) a phase boundary is
  crossed per masterplan \u00a77; or (c) Atlas or the human filed a
  `role:cassidy` issue titled `ultrareview-N trigger`."
- Keep the pushing-the-report section, but point the report at
  `.cowork/reviews/ultrareview-<N>.md` (unchanged) and mention
  that Cassidy pushes to `cowork/state` as part of her normal
  session-end commit, not as a separate CC task.

### 2. Update Cassidy's SYSTEM.md

In `.cowork/roles/cassidy/SYSTEM.md`, under "Your job" (which
currently lists 5 numbered items), add a sixth:

```
6. **When triggered, run an ultrareview.** Read `.cowork/roles/cassidy/ULTRAREVIEW.md`
   for the 12-check protocol. Produce `.cowork/reviews/ultrareview-<N>.md`.
   Trigger conditions: \u226510 merged PRs since the last ultrareview,
   a phase boundary crossed per masterplan \u00a77, or an open issue
   labelled `role:cassidy` with title `ultrareview-N trigger`.
   This is audit-only work \u2014 no code changes, no PRs opened. On
   completion, file findings as separate `role:atlas` issues using
   the normal handoff protocol.
```

Under "Authoritative documents," add an 8th entry:

```
8. `.cowork/roles/cassidy/ULTRAREVIEW.md` \u2014 the 12-check audit
   protocol for scheduled ultrareviews.
```

Under "Metrics you track," add a fourth bullet:

```
- **Ultrareview cadence**: number of ultrareviews completed per
  release cycle. Target: one per 10 merged PRs or phase boundary.
```

### 3. Update DIRECTORY.md

In `.cowork/roles/DIRECTORY.md`, find Cassidy's row in the at-a-glance
table and in her detailed subsection:

- In the at-a-glance table's "Owns" column for Cassidy, append
  "ultrareview audits (scheduled)".
- In her detailed subsection under "Owns":
  - Add bullet: "Ultrareview audits: 12-check repo-wide audits at
    10-PR gates and phase boundaries, producing
    `.cowork/reviews/ultrareview-<N>.md`."
- Under her "Does not":
  - (No change \u2014 the audit-only, no-PRs-opened posture is
    consistent with existing "does not merge, does not dispatch"
    rules.)
- Under "Metrics":
  - Add: "Ultrareview cadence (one per 10 PRs / phase boundary)."

No changes needed to Atlas's or Mia's sections.

### 4. Add a LESSONS.md entry

Append one entry to `.cowork/LESSONS.md` (read the file first via
`get_file_contents` so your push-files write preserves existing
entries verbatim \u2014 this file is append-only, and a stale-content
rewrite is a memory-loss incident):

```
## <next entry number> \u2014 ultrareview is Cassidy's lane, not a CC spawn

**Date:** <ISO-8601 of this PR's merge>
**Surfaced by:** human turn in orchestrator session 2026-04-18.
**Context:** Atlas was dispatching ultrareview via `spawn_cc("ultrareview")`
against `.cowork/prompts/ultrareview.md`. That shape predates the
three-role ensemble. Ultrareview is audit-only, read-diff-heavy, and
temperamentally skeptical \u2014 that is Cassidy's lane by design.
**Lesson:** When the 10-PR or phase-boundary gate trips, Atlas files a
`role:cassidy` issue titled `ultrareview-N trigger: <reason>`. Cassidy
picks it up next session and produces the report as part of her normal
work. No `spawn_cc("ultrareview")` \u2014 that path is deprecated.
**Corollary:** `.cowork/prompts/ultrareview.md` has moved to
`.cowork/roles/cassidy/ULTRAREVIEW.md` and is no longer a valid
spawn-mcp alias.
```

### 5. Update README.md ensemble section (minimal)

In `.cowork/roles/README.md`, in the "The ensemble (Phase 1 \u2014 three
roles)" table, update Cassidy's "Lane" cell from:

> Reads merged PRs' diffs, files follow-up issues for regressions
> missed in per-PR review. Owner of quality.

to:

> Reads merged PRs' diffs, files follow-up issues, runs scheduled
> ultrareview audits. Owner of quality.

No other changes to README.md.

### 6. Deprecate the old prompt

After moving `.cowork/prompts/ultrareview.md` to
`.cowork/roles/cassidy/ULTRAREVIEW.md`, delete the original file from
`.cowork/prompts/`. Include the deletion in the same commit as the
other changes.

## Definition of done

- `.cowork/roles/cassidy/ULTRAREVIEW.md` created, contains the 12-check
  protocol with Cassidy-voiced framing (not CC-voiced).
- `.cowork/prompts/ultrareview.md` deleted.
- `.cowork/roles/cassidy/SYSTEM.md` updated: new job bullet (#6), new
  authoritative document entry (#8), new metric bullet.
- `.cowork/roles/DIRECTORY.md` updated: Cassidy's at-a-glance and
  detailed rows reflect ultrareview ownership.
- `.cowork/roles/README.md` updated: Cassidy's one-line lane description.
- `.cowork/LESSONS.md` appended (not overwritten) with the lesson.
- PR opened against `cowork/state` branch (NOT `main`).
- PR title: `docs(roles): move ultrareview ownership Atlas \u2192 Cassidy`.
- PR body calls out:
  - The user-turn that triggered the change.
  - The re-paste requirement: after merge, the human needs to update
    the claude.ai Cassidy project's custom system prompt to match the
    new SYSTEM.md, and remove any stale `ultrareview` alias knowledge
    Atlas has in memory via a `memory_user_edits` update (this is
    Atlas's job in a future session, not something CC can do).
  - Explicit "no code changes, docs-only PR."

## Out of scope

- Editing Atlas's SYSTEM.md (no ultrareview reference there to remove;
  the trigger lives in Atlas's main operating instructions, which are
  in the orchestrator's memory and will be updated separately).
- Editing Mia's SYSTEM.md (no ultrareview reference there at all).
- Running an actual ultrareview (this PR only moves ownership; the next
  ultrareview happens when Cassidy gets triggered).
- Retroactively annotating existing ultrareview-1 report (it's fine
  as-is; new framing only applies to future audits).
- Any `memory_user_edits` calls \u2014 that's Atlas's job in a future turn.

## Branch and PR

- Branch: `claude/docs-cassidy-owns-ultrareview`
- Target: `cowork/state` (NOT `main`; all .cowork/ edits go here).
- PR title: `docs(roles): move ultrareview ownership Atlas \u2192 Cassidy`
- Open as ready-for-review (NOT draft).
- Label with: `documentation`, `role-spec-change`.

## Constraints

- Files touched (allowlist):
  - `.cowork/roles/cassidy/SYSTEM.md` (existing, edit)
  - `.cowork/roles/cassidy/ULTRAREVIEW.md` (new; content ported + reframed
    from the deleted prompts/ultrareview.md)
  - `.cowork/roles/DIRECTORY.md` (existing, edit)
  - `.cowork/roles/README.md` (existing, edit \u2014 one line only)
  - `.cowork/LESSONS.md` (existing, APPEND only \u2014 read first, preserve
    all existing entries)
  - `.cowork/prompts/ultrareview.md` (existing, DELETE)
- No production code changes. No test changes.
- No changes to `main` branch; everything lands on `cowork/state`.
- Preserve all other content in every edited file \u2014 if you use
  `create_or_update_file` for an existing file, you MUST fetch its
  current sha first and include it in the call; a stale-sha write
  silently overwrites newer content.
- Do NOT touch `.cowork/prompts/*.md` beyond the ultrareview.md deletion.

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS as standard.
- Additional section: FILES_CHANGED \u2014 list every file touched and
  the nature of the change (created / edited / deleted).
- Additional section: RE_PASTE_REMINDER \u2014 restate the human-action
  required after merge (Cassidy project system-prompt update).
- Additional section: CHARACTER_DIFF \u2014 rough character count delta
  for each edited file (for reviewer sanity check that you didn't
  accidentally delete half of SYSTEM.md).

## Time budget

30 minutes wall-clock. This is docs-only but the SYSTEM.md and
LESSONS.md reads + edits need care.

## Final note

This change is Cassidy-spec editing, which the README explicitly
restricts ("Nobody edits another role's SYSTEM.md except in a
role-bootstrap PR reviewed by the human"). The PR is opened for
human review before merge \u2014 the human is the one who decides this
SYSTEM.md change is correct. If the human is not satisfied with the
Cassidy-voice reframe of ULTRAREVIEW.md or the job-bullet wording,
iterate in PR review before merging.
