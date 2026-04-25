# CC Prompt — Docs/Spec PR

<!-- Template for CC prompts that ship spec markdown, research docs, rule files,
     or amendments. No Go code. Often docs-first PRs that precede impl PRs. -->

## Spec
- Spec file: `specs/<spec-id>.md` (this PR may be drafting/amending it)
- PR scope: PR <N> of <total> — <doc deliverable>
- Branch: `docs/<spec-id>-<slug>`
- Base: `main`

## Model & cost
- Model: Haiku (mechanical doc transcription) | Sonnet (only if doc requires synthesis from multiple sources)
- Estimated cost: $<low>–$<high>
- Calibration basis: docs PRs land $0.50–$3 typically — pad only if multi-source synthesis

## Target files
**Create:**
- `specs/<spec-id>.md` — <if new spec>
- `docs/research/<YYYY-MM>-<topic>.md` — <if research output>
- `.claude/rules/<rule-id>.md` — <if new rule, with `<!-- rulelint:allow-orphan -->` marker>

**Modify:**
- `<path>` — <reason>

**Do not touch:**
- Any `*.go` file — this is a docs PR
- Any `_test.go` file — bindings come in impl PR

## Invariants
- README never promises what isn't shipped in a tagged release — this PR must not edit README to claim un-shipped behavior
- New rule files MUST carry `<!-- rulelint:allow-orphan -->` on the line after `Bound:` until the impl PR strips it
- Spec format matches existing specs/ — invariant bindings before features, subtest mapping explicit, out-of-scope non-empty, failure modes enumerated

## Success condition
1. Files exist at specified paths with specified shape
2. `tools/rulelint` passes (allow-orphan markers tolerated)
3. Markdown lints clean if a linter is wired
4. Cross-references resolve (no broken `[link](path)` to nonexistent files)
5. Spec passes self-review checklist (see ventd-daily-rules.md if present)

## Verification
```bash
git checkout <branch>
tools/rulelint
grep -rn "allow-orphan" .claude/rules/ | wc -l   # confirm marker count matches new rules
ls -la specs/<spec-id>.md docs/research/<file>.md
```

## Out of scope
- Any Go code, even stubs
- Stripping allow-orphan markers (that's the impl PR's job)
- README updates promising the new feature

## Failure modes to flag
- Existing spec with same ID (renumber needed — surface to Phoenix, do not auto-renumber)
- Rule ID collision with existing rule
- Research source URL 404s during fetch

## Commit & PR style
- Conventional commits: `docs(<scope>):` or `chore(specs):`
- PR body: spec ID, scope summary, list of new rule IDs added with allow-orphan
