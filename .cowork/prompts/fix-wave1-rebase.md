# fix-wave1-rebase

You are Claude Code. Four Phase 2 Wave 1 PRs need to be rebased onto
current main after three adjacent Wave 1 PRs merged (#278, #279, #284).
Two of them (#279 ASAHI and another PR) both registered their backends
in `cmd/ventd/main.go` — your rebase must resolve that conflict by
preserving both registrations.

## Scope

Rebase these four PRs onto main, push the rebased branches, verify CI
passes on each. Do NOT fix non-rebase-induced test failures — if a test
fails on a PR's own code after a clean rebase, leave the PR failing and
report back.

| PR | Branch | Likely conflict |
|----|--------|-----------------|
| #277 | claude/P2-PWMSYS-01-sbc-backend | cmd/ventd/main.go (ASAHI + PWMSYS both register) |
| #281 | (check `gh pr view 281 --json headRefName`) | go.mod + go.sum (go-hid dep), cmd/ventd/main.go |
| #282 | (check `gh pr view 282 --json headRefName`) | cmd/ventd/main.go (CROSEC register) |
| #285 | (check `gh pr view 285 --json headRefName`) | cmd/ventd/main.go (IPMI register) |

## Setup

```
cd /home/cc-runner/ventd
git fetch origin main
git checkout main
git pull origin main
```

## Per-PR procedure

For each PR number N in the table:

1. Fetch the branch:
   ```
   gh pr checkout N
   git fetch origin main
   git rebase origin/main
   ```

2. If there's a conflict:
   - Open the conflicted file (expect `cmd/ventd/main.go` every time).
   - Resolve by **keeping both** backend registrations. The pattern is:
     ```go
     hal.Register(halhwmon.BackendName, halhwmon.NewBackend(logger))
     hal.Register(halnvml.BackendName, halnvml.NewBackend(logger))
     hal.Register(halasahi.BackendName, halasahi.NewBackend(logger))
     hal.Register(halpwmsys.BackendName, halpwmsys.NewBackend(logger))  // or whichever backend this PR adds
     ```
     Same for imports — keep all.
   - For go.mod / go.sum conflicts (#281 only), run `go mod tidy` with
     appropriate tags and let Go resolve.
   - Stage + continue: `git add <files> && git rebase --continue`.

3. After rebase completes:
   ```
   go build ./...
   CGO_ENABLED=0 go build ./...
   go test -race -count=1 ./...
   gofmt -l .
   ```
   Record the output of each in a per-PR notes buffer.

4. Force-push the rebased branch (solo-dev on a feature branch, force-push OK):
   ```
   git push --force-with-lease origin <branch>
   ```

5. Wait ~3 minutes for CI, then:
   ```
   gh pr checks N
   ```
   Record: did the rebase fix CI, or did it fail in the same way as before?

6. Move to next PR.

## Out-of-order safety

Rebase in this order: #277, #282, #285, #281. The go-hid dep PR (#281)
last because its go.mod conflict is the riskiest.

Never rebase two PRs simultaneously — sequential only. The worktree
state matters.

## Reporting

For each PR:

- PR_N_STATUS: clean-rebase | conflict-resolved | rebase-failed
- PR_N_FILES_TOUCHED_IN_REBASE: <list>
- PR_N_CI_STATE_AFTER: green | same-failure | new-failure
- PR_N_NEW_FAILURE_DETAIL: <if CI state is "new-failure", one paragraph>

Final summary:

- PRS_REBASED_CLEAN: <list>
- PRS_NEED_FOLLOWUP: <list + why>
- TIME_SPENT: <hours>

## Out of scope

- Do NOT add new tests.
- Do NOT fix pre-existing test failures (only rebase-induced ones).
- Do NOT bump dependencies.
- Do NOT edit .cowork/ files.
- Do NOT open new PRs — you're updating the existing PRs in place.

## Time budget

60 minutes wall-clock. If a single PR rebase is taking >20 minutes,
report that PR as rebase-failed and move on.
