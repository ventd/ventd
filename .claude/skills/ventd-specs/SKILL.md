---
name: ventd-specs
description: >
  Guides spec authoring, feature design, and commit message drafting for
  the ventd codebase. ALWAYS invoke before drafting any spec file, writing
  a commit message, or proposing a design decision about new features or
  behaviour changes. Invoke whenever the user mentions: specs, new feature,
  feature design, design, behaviour change, commit, commit message, PR
  series, masterplan IDs, roadmap, CHANGELOG, or adding a new RULE-*
  binding. This skill routes correctly between spec authoring (read
  references/spec-format.md + use assets/spec-template.md), commit drafting
  (read references/commit-conventions.md), and rule-binding work (read
  references/subtest-patterns.md and invoke ventd-rulelint after).
  Even if the user only says "I want to add X" or "commit this", invoke
  this skill first — it shapes how the work gets structured.
---

# ventd-specs

Three modes. Pick the one that fits and follow it.

## Mode A — Writing or reviewing a spec

**Trigger:** user asks to design a feature, draft a spec, plan a PR series,
or review whether a feature is scoped correctly.

1. Read `references/spec-format.md` — it defines every section that every
   spec in `specs/` uses. A spec with a missing section is an incomplete spec.

2. Use `assets/spec-template.md` as your starting skeleton. Fill every
   field. Leave `[TBD]` only for items genuinely unknowable today.

3. Model / cost routing — fill the **Estimated session cost** field using
   this table:

   | Work type                              | Model  | Sessions | Cost est. |
   |----------------------------------------|--------|----------|-----------|
   | Pure Go, fixtures, no hardware         | Sonnet | 3–5      | $5–15     |
   | New backend (USB HID, IPMI extension)  | Sonnet | 8–12     | $10–25    |
   | Protocol design review                 | Opus¹  | 1        | ~$3       |
   | PI/MPC math review                     | Opus¹  | 1–2      | ~$3–6     |
   | HARDWARE-REQUIRED final DoD            | Sonnet | +2–3     | +$5–10    |

   ¹ Opus only for consult sessions in claude.ai chat — never in CC terminals.
   Never invoke subagents inside a spec session; the session cap is 3/session.

4. Gate HARDWARE-REQUIRED items explicitly. If any DoD bullet requires a
   real device (HIL, Proxmox VM, USB hardware), mark the DoD bullet with
   `[HARDWARE-REQUIRED]` and add a `## Hardware gates` section listing the
   exact rigs (`192.168.7.222 MiniPC`, `192.168.7.10 Proxmox`).

5. Sanity-check the spec against the roadmap in `CLAUDE.md §Current roadmap`
   before presenting it. Phase order matters: SLEEP → PI → HYST → LATCH+STEP
   → PI-autotune → HWCURVE → INTERFERENCE → DITHER → MPC.

6. Cross-reference `specs/README.md` read-order gating. If the spec you're
   writing depends on another spec's output, name the dependency explicitly
   in the **Dependencies already green** header field.

## Mode B — Writing a commit message

**Trigger:** user says "commit", "write a commit message", "what should the
commit say", or is about to run `git commit`.

1. Read `references/commit-conventions.md` — it lists every valid type,
   every ventd-specific scope, the CHANGELOG mapping, and forbidden trailers.

2. Draft the message in your head, then check:
   - Subject ≤ 72 chars, imperative, lowercase first letter after colon, no
     trailing period. If over 72, cut redundant qualifiers first ("for is_pump
     fans" → "for pump fans"), then shorten nouns ("pump_minimum" → "pump
     floor"), never abbreviate scope or type.
   - Scope matches a known package or is omitted for cross-cutting changes.
   - Body wraps at 72 cols and explains *why*, not *what*.
   - `Fixes #N` or `Refs #N` in footer if an issue is relevant.
   - No `Co-Authored-By: Claude` — attribution rules in
     `.claude/rules/attribution.md` forbid it.

3. Always propose the CHANGELOG edit alongside the commit message.
   `feat` → **Added**, `fix` → **Fixed**, `refactor`/`perf` → **Changed**.
   `test`, `docs`, `build`, `ci`, `chore` → no CHANGELOG entry.

4. If the diff touches a `.claude/rules/*.md` file, flag it: the user must
   run `go run ./tools/rulelint -root .` before the commit. Point them at
   the ventd-rulelint skill if needed.

## Mode C — Adding a RULE-* binding

**Trigger:** user is adding or editing a `## RULE-*` section in any
`.claude/rules/*.md` file, or adding a bound subtest.

1. Read `references/subtest-patterns.md` — it defines the exact three-part
   rule entry format and the Go `t.Run` counterpart.

2. After writing the rule and its subtest, invoke the **ventd-rulelint**
   skill to validate. Do not claim the task complete until rulelint exits 0.

3. The new rule and the new subtest must ship in the same PR. Orphan rules
   (rule without a subtest) and orphan subtests (subtest without a rule)
   both block CI.

## Quick-reference

| I need to…                         | Read                              | Then do                            |
|------------------------------------|-----------------------------------|------------------------------------|
| Draft a new spec                   | references/spec-format.md         | Fill assets/spec-template.md       |
| Write a commit message             | references/commit-conventions.md  | Check attribution.md guardrails    |
| Add a RULE-* entry                 | references/subtest-patterns.md    | Run ventd-rulelint after           |
| Check spec read-order deps         | specs/README.md                   | Verify deps green before starting  |
| Understand existing rule catalog   | ventd-rulelint → rules-catalog.md | —                                  |
