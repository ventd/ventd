# Spec NN — [Short title, e.g. "Hysteresis band for PWM curves"]

**Masterplan IDs this covers:** [e.g. P3-HYST-01, T-HYST-01]
**Target release:** v[X.Y.Z] ([one-line rationale, e.g. "closes Phase 3 control-quality gap"])
**Estimated session cost:** Sonnet, ~[N]–[M] focused sessions, $[X]–[Y] each. [No Opus required. | One Opus consult (~$3) for [reason].]
**Dependencies already green:** [comma-separated IDs, or "none"]

---

## Why this ships [first|next|after X]

[1–3 paragraphs. Why now, not later? What does it unblock? What is the
user-visible or technical motivator? Reference the roadmap phase if relevant.]

## Scope — what this session produces

[Intro sentence: "A PR series that [achieves X]. N small PRs, not one big
one. Each independently mergeable."]

### PR 1 — [Title] ([Masterplan-ID])

**Files:**
- `internal/[pkg]/[file].go` (new | extend)
- `internal/[pkg]/[file]_test.go` (new | extend)
- `.claude/rules/[name].md` (new) ← only if a new rule file is created

**Coverage required:**
1. [Specific behaviour under test — happy path]
2. [Edge case 1]
3. [Edge case 2]
4. [Error path]
5. [Hardware-gated test, if any — append `[HARDWARE-REQUIRED]`]

**Invariant file contents (`.claude/rules/[name].md`):** ← delete if no new rule file
> Follow the exact format established in `.claude/rules/hwmon-safety.md`.
> Write [N] `RULE-[PREFIX]-[KEYWORD]:` entries corresponding to the [N]
> tests above. Each rule's `Bound:` line points to a specific subtest name.

### PR 2 — [Title] ([Masterplan-ID])

**Files:**
- [list]

**Tests:**
1. [list]

### PR 3 — [Title, e.g. "CHANGELOG + release polish"] ← include only if applicable

**Files:**
- `CHANGELOG.md`
- [any other release-readiness files]

**Behaviour:**
1. `CHANGELOG.md` under v[X.Y.0]: [describe the entry, including honest
   caveats about what works and what doesn't].

## Definition of done

- [ ] `go test -race ./internal/[pkg]/...` passes locally and in CI.
- [ ] `go test -run Test[Xxx]_Invariants ./internal/[pkg]/...` passes; every subtest maps 1:1 to a `RULE-[PREFIX]-*` entry in `.claude/rules/[name].md`.
- [ ] `go run ./tools/rulelint -root .` exits 0 — no orphan rules, no orphan subtests.
- [ ] `CHANGELOG.md` updated.
- [ ] [Any hardware-gated items listed with `[HARDWARE-REQUIRED]` and the rig needed.]
- [ ] No HARDWARE-REQUIRED work [delete this line if there are hardware gates above].

## Explicit non-goals

- [Feature X is out of scope — that's spec NN.]
- [Vendor Y support is out of scope for this PR series.]
- [No UI changes.]
- [No fleet/remote management.]

## CC session prompt — copy/paste this

```
Read /home/[user]/ventd/specs/spec-NN-[short-name].md. It references
existing code in internal/[pkg]/, .claude/rules/, and the masterplan
files (ventdmasterplan.md, ventdtestmasterplan.md) for naming and style
conventions.

Start with PR 1. Do not begin PR 2 until PR 1's DoD checklist is green.
Run `go test -race ./internal/[pkg]/...` after every meaningful edit.
If a test requires hardware, mark it `//go:build [tag]_integration` and
document in TESTING.md — do not skip silently.

[If a new .claude/rules/*.md file is being created:]
The .claude/rules/[name].md file MUST follow the exact format used in
.claude/rules/hwmon-safety.md — read that file first before writing the
new rule file.

Use Sonnet for all of this work. Do not invoke any subagents. Commit at
every green-test boundary.
```

## Why this is [cheap|costly]

- [Saving 1: fixtures already exist / pattern proven with X]
- [Saving 2: pure Go, no hardware, no network]
- [Cost 1: protocol reverse-engineering required]
- [Cost 2: HARDWARE-REQUIRED for final DoD — adds +2–3 sessions]

## Hardware gates ← delete this section if no HARDWARE-REQUIRED items

- `192.168.7.222` MiniPC (HIL) — for [specific test]
- `192.168.7.10` Proxmox — for [specific test]
