---
name: pr-body-from-spec
description: |
  Use when about to open a PR for a smart-mode patch (v0.5.x) and the spec
  document already exists at `specs/spec-v0_5_X-foo.md`. Generates PR-A
  (rules + tests) and PR-B (implementation) body skeletons by reading the
  spec's RULE-* bindings, success criteria, and validation table. Do NOT
  use for: bug-fix PRs (no spec to read); release PRs; non-smart-mode work.
disable-model-invocation: true
argument-hint: <spec-file-or-id> [pr-letter: A|B|both]
allowed-tools: Read Bash(grep *) Bash(ls *)
---

# pr-body-from-spec

User-invoked. Reads a smart-mode spec file and emits PR-A / PR-B body
markdown ready to paste into `gh pr create --body-file`. Will scale across
v0.5.6 → v0.5.10 since the patch shape repeats.

## Inputs

- `<spec-file-or-id>`: path (`specs/spec-v0_5_6-opportunistic-probing.md`)
  or shorthand id (`v0_5_6`). The shorthand resolves to the matching file
  in `specs/`.
- `<pr-letter>`: `A` (rules + tests scaffold), `B` (implementation), or
  `both` (default). Smart-mode patches typically split docs/test-skeleton
  from impl.

## What it produces

For each requested PR letter, a markdown body with these sections:

```
## Summary
<one paragraph from the spec abstract>

## What's in the box
<bulleted list, derived from spec section headings + RULE-* bindings>

## Rules introduced (PR-A only)
<table: RULE-ID | invariant summary | bound subtest>

## Test plan
<bulleted: each RULE has an entry; HIL items called out per §12 of
spec-smart-mode.md>

## Validation
- [ ] rulelint clean (`go run ./tools/rulelint`)
- [ ] go test ./... green under -race
- [ ] HIL: <fleet member from spec §12.1, or "synthetic only" with reason>
- [ ] Time-bound metric: <from spec §12 if present, else "n/a — explicit">

## Cost
- Estimated CC tokens: <from spec-smart-mode.md §13 if listed>
- Notes: <anything Phoenix should know before the spend>
```

## Procedure

1. Resolve spec path:
   - If arg is a path that exists, use it.
   - Else `ls specs/spec-<arg>*.md` and pick the unambiguous match.
   - Ambiguous → fail and list candidates.
2. Parse the spec for:
   - Title (level-1 heading).
   - First "Summary"/"Abstract"/"§1" paragraph.
   - All `RULE-*` heading occurrences and `Bound:` lines.
   - "§12" or "Validation" section if present.
3. Cross-reference `spec-smart-mode.md` §12 (per-patch validation) and
   §13 (cost projection) for the patch.
4. Emit the markdown body to stdout. Do NOT write to disk; the user pipes
   into `gh pr create --body-file -` or copies into the GitHub UI.

## Smart-mode-specific defaults

- **PR-A** (rules + scaffold) always includes:
  - `.claude/rules/<topic>.md` additions
  - `tools/rulelint` re-run instructions
  - `<!-- rulelint:allow-orphan -->` markers (if scaffolding tests in
    PR-B)
- **PR-B** (implementation) always includes:
  - removal of any allow-orphan markers added in PR-A
  - the bound subtests (one per RULE)
  - HIL line per §12.1 of spec-smart-mode.md

## Failure modes this catches

- **Forgetting the validation table** — every PR body has the same four
  bullets, copy-paste-ready.
- **Forgetting to mention the HIL fleet member** — pulled directly from
  spec §12.1 or explicitly marked "synthetic only" with a reason.
- **Forgetting to update CHANGELOG** — surfaced as a checklist item; the
  conventional-commit skill handles the actual edit.
- **Cost surprise** — the §13 line is reproduced verbatim so Phoenix sees
  the projected CC spend before approving the work.

## Out of scope

- Generating the spec itself (use `ventd-specs` for that).
- Opening the PR (`gh pr create`) — this skill only emits the body.
- Editing the spec — read-only operation.
