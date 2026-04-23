# Spec format — section reference

Every spec in `specs/` follows this structure. Sections must appear in
this order. Omitting a section is an error; use explicit placeholders if
content is genuinely unknown.

---

## Header block (front matter, not a heading)

```
# Spec NN — <short title>

**Masterplan IDs this covers:** <comma-separated IDs from ventdmasterplan.md>
**Target release:** v<major>.<minor>.<patch> (<one-line rationale>)
**Estimated session cost:** <Model>, ~<N>–<M> focused sessions, $<X>–<Y> each. [One Opus consult (~$3) for <reason>.] [No Opus required.]
**Dependencies already green:** <comma-separated IDs> [or "none"]
```

All four bold fields are required. The cost field must name the model
(Sonnet / Opus) and give a dollar range so Phoenix can decide whether to
start the work now or defer it.

---

## Why this ships [first|next|after X]

One to three paragraphs. Explain the *product* or *technical* reason this
spec is prioritised now. Reference the relevant market hook, release note,
or technical dependency. If this spec unblocks another, name it.

Pattern from spec-01: "The code is in `main`; what's missing is the
belt-and-braces polish that keeps a first-time /r/homelab user from filing
an issue in the first hour."

---

## Scope — what this session produces

Intro sentence: "A PR series that closes the gap between X and Y. N small
PRs, not one big one. Each independently mergeable."

### PR N — <title> (<Masterplan-ID>)

Repeat for each PR. Each sub-section has:

**Files:**
- `path/to/file.go` (new | extend | verify exists)
- ...

**Coverage required:** / **Tests:** / **Behaviour:**
Numbered list. Each item is a specific, testable behaviour. If a test
must be hardware-gated, append `[HARDWARE-REQUIRED]`.

**Invariant file contents (`.claude/rules/<name>.md`):** (only if the PR
creates a new rule file)
> Follow the exact format established in `.claude/rules/hwmon-safety.md`.
> Write N `RULE-<PREFIX>-<N>:` entries corresponding to the N tests above.
> Each rule's `Bound:` line points to a specific subtest name.

---

## Definition of done

Checkbox list. Every item must be independently verifiable without hardware
unless explicitly marked `[HARDWARE-REQUIRED]`.

```markdown
- [ ] `go test -race ./internal/<pkg>/...` passes locally and in CI.
- [ ] `go test -run TestXxx_Invariants ./internal/<pkg>/...` passes; every subtest maps 1:1 to a RULE-* entry.
- [ ] rulelint still green — no orphan rules, no orphan subtests.
- [ ] `CHANGELOG.md` updated.
- [ ] No HARDWARE-REQUIRED work [unless explicitly listed here].
```

---

## Explicit non-goals

Bullet list. Name every adjacent feature that is explicitly out-of-scope
for this spec. The purpose is to prevent CC from scope-creeping mid-session.

Pattern: "No new vendors beyond Supermicro + Dell. LIQUID backend is a
separate spec."

---

## CC session prompt — copy/paste this

Fenced code block. This is the literal prompt the user pastes into a new
CC terminal to start the session. Include:

1. Path to this spec file (absolute).
2. Which other files to read first (masterplan, existing rule file for style).
3. Which PR to start with and the stop condition (do not start PR N+1 until PR N's DoD is green).
4. The test command to run after every meaningful edit.
5. Model instruction ("Use Sonnet for all of this work. Do not invoke any subagents.")
6. Commit cadence ("Commit at every green-test boundary.").

---

## Why this is cheap / costly

Brief justification of the cost estimate. Name the savings (fixtures
already exist, pattern already proven) or the costs (protocol reverse
engineering needed, HARDWARE-REQUIRED for final DoD).

---

## Hardware gates (only if HARDWARE-REQUIRED items exist)

List the exact rigs needed and what they're used for:

- `192.168.7.222` MiniPC (HIL) — for [specific test]
- `192.168.7.10` Proxmox — for [specific test]

---

## Notes on scope creep

The most common violations seen in past sessions:

- Expanding vendor coverage beyond what the spec names.
- Adding UI changes to a backend-only spec.
- Writing helper abstractions "for future specs" that aren't used in this
  PR series — YAGNI.
- Skipping the CC session prompt section (users need the copy-paste block).
