# docs-roles-directory — role registry rollup

**Care level:** LOW. Docs-only, no code touched. Work the task however your session is configured — no model-gated abort.

## Task

- **ID:** docs-roles-directory (issue #283)
- **Goal:** Create `.cowork/roles/DIRECTORY.md` on `cowork/state` branch — a tabular rollup of the role ensemble. This is a convenience rollup, not a new source of truth. Every claim in the new file must be traceable to an existing `SYSTEM.md` or the ensemble `README.md`.

## Context — read before editing

1. `.cowork/roles/README.md` — ensemble coordination rules. Source for the "Lane boundaries (hard rules)" section you will mirror into DIRECTORY.md.
2. `.cowork/roles/atlas/SYSTEM.md` — source for Atlas's identity/owns/does-not/handoffs/metrics rows.
3. `.cowork/roles/cassidy/SYSTEM.md` — same for Cassidy.
4. `.cowork/roles/mia/SYSTEM.md` — same for Mia.
5. Existing issue #283 body — the full requirements.

## What to do

1. Create the file `.cowork/roles/DIRECTORY.md` with this structure:

   ```markdown
   # Role directory

   Rollup view of the Cowork role ensemble. Each role is a distinct
   claude.ai conversation with its own system prompt, its own lane, and
   its own worklog. Roles coordinate through GitHub (issues labelled
   `role:<name>`) and never through direct messaging.

   **Source of truth for each role's configuration is the role's own
   `SYSTEM.md`.** This file is a rollup for convenience.

   ## At a glance

   | Role    | Purpose       | Owns                    | Does not           | Metrics                    |
   |---------|---------------|-------------------------|--------------------|----------------------------|
   | Atlas   | Orchestrator  | dispatch, merge, queue  | close issues, read diffs | PR/hr, TPM            |
   | Cassidy | Reviewer      | diff audit, regressions | merge, close, dispatch | catches/wk, FP rate   |
   | Mia     | Triage        | backlog, labels, close  | merge, dispatch, diff-read | closures/wk, stale%|

   ## Active roles

   ### Atlas — Orchestrator

   - **Identity:** <one sentence pulled from atlas/SYSTEM.md>
   - **Owns:** <bulleted list from "Your job" section>
   - **Does not:** <bulleted list from "Lane boundaries" section>
   - **Handoffs in:** Issues labelled `role:atlas` filed by Cassidy or Mia.
   - **Handoffs out:** Files issues labelled `role:cassidy` (requests for diff audit) or `role:mia` (via @mia closing comments).
   - **Metrics:** PR/hr merged, TPM per merged PR. Tracked in `.cowork/THROUGHPUT.md`.
   - **Source of truth:** `.cowork/roles/atlas/SYSTEM.md`

   ### Cassidy — Reviewer
   <same structure>

   ### Mia — Triage
   <same structure>

   ## Future roles (not yet active)

   The ensemble may expand after ~two weeks of active three-role operation if the coordination overhead is justified by the quality gain. Candidates:

   - **Felix** — Architect. Plan evolution. Status: not yet active.
   - **Nora** — Writer. User-facing content. Status: not yet active.
   - **Drew** — Security. CVE response and audit. Status: not yet active.
   - **Pax** — Releaser. Tags and release pipeline. Status: not yet active.

   ## Lane boundaries (hard rules)

   <verbatim mirror of the "Lane boundaries (hard rules)" block from
    .cowork/roles/README.md. If you edit one, edit the other in the
    same PR.>

   ## Notes for future readers

   - "Roles" are Claude sub-personas configured via distinct system
     prompts, not human employees. Framing them as employees is a
     category error — they do not draw salaries, hold rights, or
     persist outside a given conversation's context window.
   - The ensemble is explicitly a test. See the "Evolution" section
     of `.cowork/roles/README.md` for the exit criterion.
   ```

2. Fill the sections by reading the three SYSTEM.md files. Each field must be sourced verbatim or near-verbatim from the role's SYSTEM.md. Do not paraphrase in a way that changes meaning. If a field genuinely can't be sourced, leave the field blank with `TODO(owner)` marker — do not invent.

3. Add exactly one line to `.cowork/roles/README.md` in the "The ensemble (Phase 1 — three roles)" section linking to the new DIRECTORY.md. Suggested placement: immediately after the role table, as a single italicised line like `*See [DIRECTORY.md](./DIRECTORY.md) for a rollup of role responsibilities, lane boundaries, and handoff protocols.*`

4. Do NOT edit any `SYSTEM.md` file.

5. CHANGELOG entry is NOT required (this is `.cowork/` infrastructure, not product code).

## Definition of done

- `.cowork/roles/DIRECTORY.md` exists on branch.
- Every Active-role subsection has all 7 fields populated or TODO-marked.
- "Lane boundaries (hard rules)" section matches `README.md` verbatim (you can check with a `diff` if you push both files and compare the blocks).
- README.md has exactly one new line linking to DIRECTORY.md.
- No SYSTEM.md was modified.
- Branch opens a PR targeting `cowork/state` (NOT `main`).

## Out of scope

- Creating new role definitions (Felix / Nora / Drew / Pax stay as stubs).
- Metric dashboards or automation — this is a static markdown file.
- Tests — no code changed.
- Any changes outside `.cowork/roles/DIRECTORY.md` and the one-line README.md edit.

## Branch and PR

- Branch: `claude/docs-roles-directory`
- Target: `cowork/state` (not main)
- Title: `docs(roles): add DIRECTORY.md — role registry rollup (#283)`
- Open PR as ready-for-review (NOT draft).
- PR body includes: link to #283, bulleted files-touched list, verification notes (diff between the two "Lane boundaries" blocks).

## Allowlist

- `.cowork/roles/DIRECTORY.md` (new)
- `.cowork/roles/README.md` (one-line link only)

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS.
- SUMMARY should confirm: (a) no SYSTEM.md edits, (b) Lane-boundaries block is verbatim, (c) every field is either sourced or TODO-marked.
