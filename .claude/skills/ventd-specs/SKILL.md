---
name: ventd-specs
description: |
  Use when authoring a spec, designing a feature, drafting a commit
  message for a feature change, or proposing a behaviour change in the
  ventd codebase. Triggers on: "draft a spec", "design X", "new
  feature", "PR series", "roadmap", "add a RULE-* binding". Routes to
  one of three modes: spec authoring (Mode A), commit drafting (Mode
  B), or rule binding (Mode C). Even for casual asks like "I want to
  add X" or "let's plan Y", invoke this skill — it shapes how the work
  gets structured. Do NOT use for: pure code edits with no design
  surface, rulelint runs (use ventd-rulelint), or commits unrelated to
  features (use conventional-commit directly).
---

# ventd-specs

Three modes. Pick one and follow it.

## Current state

<!-- VERIFY CC SUPPORTS !`...` INJECTION; remove if not -->
Existing specs: !`ls specs/ | grep -E '^spec-' | head -20`

Current roadmap phase (from CLAUDE.md): !`grep -A1 'Current roadmap' CLAUDE.md | head -3`

## Mode A — Spec authoring or review

**Trigger:** drafting a new spec, reviewing scope, planning a PR series.

Read `references/spec-format.md` for the section schema. Use
`assets/spec-template.md` as the starting skeleton. Cost-routing table
and HARDWARE-REQUIRED conventions live in `references/cost-routing.md`.

Constraints (not steps):

- Every spec has invariant bindings before features. RULE-* IDs are
  defined in the spec; the bound subtests ship in the same PR that
  ships the rules.
- Out-of-scope section is non-empty. If a spec has nothing it's
  refusing to do, it's underspecified.
- Failure modes are enumerated, not gestured at.
- HARDWARE-REQUIRED DoD bullets are tagged explicitly. Phoenix's
  hardware fleet is in CLAUDE.md; reference rigs by their actual
  identifiers (`192.168.7.222 MiniPC`, `192.168.7.10 Proxmox`).
- Spec depends on other specs → name the dependency in the
  **Dependencies already green** header. The reader needs to know
  what to read first.
- Roadmap phase order is real: SLEEP → PI → HYST → LATCH+STEP →
  PI-autotune → HWCURVE → INTERFERENCE → DITHER → MPC. A spec that
  jumps the order without justification is a flag.

## Mode B — Commit message for a feature change

**Trigger:** about to commit work that lands a spec or part of a spec.

This skill drafts the message and the CHANGELOG edit; the
conventional-commit skill executes the commit. They compose; don't
duplicate work.

Constraints:

- Subject ≤72 chars. Cut qualifiers, shorten nouns, never abbreviate
  type or scope.
- Body explains *why*, not *what*.
- `Refs #N` or `Fixes #N` in footer when relevant.
- No `Co-Authored-By: Claude` — `.claude/rules/attribution.md` forbids
  it.
- CHANGELOG mapping: feat→Added, fix→Fixed, refactor/perf→Changed,
  BREAKING→Changed (prefixed). test/docs/build/ci/chore → no entry.
- Touching `.claude/rules/*.md` → run rulelint before commit.

Hand off to conventional-commit skill once the message is drafted.

## Mode C — Adding a RULE-* binding

**Trigger:** adding or editing a `## RULE-*` section in
`.claude/rules/*.md`, or adding a bound subtest.

Read `references/subtest-patterns.md` for the three-part rule entry
format and Go `t.Run` counterpart.

Constraints:

- Rule and subtest ship in the same PR. Orphan rules and orphan
  subtests both block CI.
- Subtest name in rule file matches `t.Run("...")` exactly
  (case-sensitive, slashes for nested).
- Run ventd-rulelint after writing the binding. Do not declare done
  until rulelint exits 0.
- Docs-first PRs use `<!-- rulelint:allow-orphan -->` markers; impl
  PRs MUST strip them. rulelint errors on present-but-resolved
  markers — this is a real failure mode that has shipped to CI.

## Gotchas (real failure modes)

- **README never promises what isn't shipped.** A spec proposing a
  feature does not warrant a README edit. README changes when a
  tagged release ships the feature. Repeat: tagged release.
- **Specs drift from rule bindings.** When a rule changes, the spec
  that introduced it usually needs an amendment file
  (`spec-NN-amendment-*.md`), not a rewrite of the original spec.
  Original specs are immutable history.
- **DEFERRED specs need renaming when un-deferred.** `spec-16-persistent-state-DEFERRED.md`
  → `spec-16-persistent-state.md`. There's no automation; manual `git mv`.
- **CC subagents inside spec sessions are forbidden.** Session cap is
  3/session. Any subagent invocation breaks the budget model.
- **Cost estimates are pad-50%-only-when-touching-existing-scaffolding.**
  sysusers, units, install scripts, docker workflows — these have
  hidden coupling. Pure new code does not get the pad.
- **Ground-truth probe before CC prompt.** Q1-Q5 Haiku pre-flight
  probe (4 greps, STOP-and-report, ~$0.20) collapses CC cost ~10×.
  Skipping it is the single biggest cost regression.

## Quick-reference

| Need | Read | Then |
|---|---|---|
| Draft a spec | references/spec-format.md | Fill assets/spec-template.md |
| Cost-route a session | references/cost-routing.md | Fill the spec's cost field |
| Write a feature commit | references/commit-conventions.md | Hand off to conventional-commit |
| Add a RULE-* | references/subtest-patterns.md | Run ventd-rulelint after |
| Check spec read-order | specs/README.md | Verify deps green before starting |
| Existing rule catalog | ventd-rulelint → references/rules-catalog.md | — |

## Out of scope

- Running rulelint (ventd-rulelint owns that)
- Executing the commit (conventional-commit owns that)
- Pushing or tagging
- Editing past spec files (immutable history; use amendment files)
