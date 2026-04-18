# fix-277-rebase

You are Claude Code. PR #277 (P2-PWMSYS-01) has mergeable_state "dirty"
— its base is commit 04660cf, main is now far ahead. Rebase onto
current main.

## Setup

```
cd /home/cc-runner/ventd
git fetch origin
git checkout main
git pull origin main
```

## Steps

1. Check out the PR branch:
   ```
   gh pr checkout 277
   ```

2. Rebase onto current main:
   ```
   git fetch origin main
   git rebase origin/main
   ```

3. Expected conflict: `cmd/ventd/main.go`. Main now registers the
   `halasahi` backend (from #279). PR #277 adds the `halpwmsys`
   registration. Resolve by **keeping both**:

   ```go
   // imports block — keep both new imports in alphabetical order
   halasahi "github.com/ventd/ventd/internal/hal/asahi"
   halhwmon "github.com/ventd/ventd/internal/hal/hwmon"
   halnvml "github.com/ventd/ventd/internal/hal/nvml"
   halpwmsys "github.com/ventd/ventd/internal/hal/pwmsys"

   // run() block — keep both registration lines
   hal.Register(halhwmon.BackendName, halhwmon.NewBackend(logger))
   hal.Register(halnvml.BackendName, halnvml.NewBackend(logger))
   hal.Register(halasahi.BackendName, halasahi.NewBackend(logger))
   hal.Register(halpwmsys.BackendName, halpwmsys.NewBackend(logger))
   ```

   Possible additional conflict: `internal/hwmon/*.go` if #277's branch
   still has the old hwmon exports that #278 pruned. If so, delete the
   conflicting block on the "theirs" side (the main side; #278 already
   removed them).

   CHANGELOG.md may conflict. Keep both sets of entries, ordered by
   PR landing in main-first / pwmsys-last.

4. Stage resolved files + continue:
   ```
   git add <files>
   git rebase --continue
   ```

5. Verify build + test locally:
   ```
   go build ./...
   CGO_ENABLED=0 go build ./...
   go test -race -count=1 ./...
   gofmt -l .
   ```

6. Force-push:
   ```
   git push --force-with-lease origin claude/P2-PWMSYS-01-sbc-backend
   ```

7. Wait 3 minutes for CI, then:
   ```
   gh pr checks 277
   ```

## Reporting

STATUS: done | partial | blocked
REBASE_CLEAN: yes | resolved-with-N-conflicts
FILES_CONFLICTED: <list>
BUILD_LOCAL: pass | fail + details
TEST_LOCAL: pass | fail + details
CI_AFTER: green | failed + which checks

## Out of scope

- Do NOT fix pre-existing test failures.
- Do NOT add new tests.
- Do NOT bump dependencies.
- Do NOT edit .cowork/ or anything outside the original PR's allowlist.

## Time budget

30 minutes wall-clock.
