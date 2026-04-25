# CC Prompt — Implementation PR

<!-- Template for CC prompts that build/modify Go code, tests, or daemon behavior.
     Fill every slot. Empty slots = ambiguous spec = CC drift. -->

## Spec
- Spec file: `specs/<spec-id>.md`
- PR scope: PR <N> of <total> — <one-line scope>
- Branch: `<feat|fix>/<spec-id>-pr<N>-<slug>`
- Base: `main`

## Model & cost
- Model: Sonnet (implementation) | Haiku (mechanical only — tests/lint/commit-msg)
- Estimated cost: $<low>–$<high>
- Calibration basis: <similar PR from docs/claude/spec-cost-calibration.md, or "no prior — pad 50%">

## Target files (explicit, no globs)
**Create:**
- `<path>`
- `<path>`

**Modify:**
- `<path>` — <one-line reason>
- `<path>` — <one-line reason>

**Do not touch:**
- <path or pattern> — <reason>

## Invariants (rule IDs from .claude/rules/)
This PR must preserve:
- `RULE-<AREA>-NN` — <one-line restatement>
- `RULE-<AREA>-NN` — <one-line restatement>

This PR introduces (new bindings):
- `RULE-<AREA>-NN` → subtest `<TestName>/<subtest>` in `<test_file>`
- (each new rule = 1:1 subtest, enforced by tools/rulelint)

## Success condition
PR is done when:
1. All listed target files exist with the specified shape
2. `go test ./...` passes
3. `golangci-lint run` clean
4. `tools/rulelint` reports zero orphans, zero unbound rules, zero allow-orphan markers on resolved bindings
5. `go list -deps ./cmd/ventd | grep <new-pkg>` confirms registration (skip if pure library)
6. <spec-specific assertion, e.g. "ventd --probe shows new device" or "smoke VM 220 boots clean">

## Verification commands (Phoenix runs these, not CC)
```bash
git fetch origin
git checkout <branch>
go test ./... -count=1
golangci-lint run
tools/rulelint
gh pr view --json state,statusCheckRollup
```

## Out of scope (do not add to this PR)
- <list — protects against scope creep>
- <list>

## Failure modes to flag, not fix
If CC encounters any of these, stop and surface to Phoenix:
- Pre-existing test failures on `main`
- Rule binding ambiguity (rule cites multiple possible subtests)
- Spec contradiction (two sections of spec disagree)
- Missing fixture/hardware that test requires

## Commit & PR style
- Conventional commits (`feat(scope):`, `fix(scope):`, `test(scope):`, `chore(scope):`)
- Squash on merge — body becomes the squash message
- PR body: link to spec, list rule bindings added, paste verification command output
- Linear history on main — no merge commits

## Pre-flight (run before starting)
```bash
.claude/scripts/preflight.sh <spec-id>
```
Must pass before CC writes a single line.
