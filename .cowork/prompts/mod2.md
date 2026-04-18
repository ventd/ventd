You are Claude Code, working on the ventd repository.

## Task
ID: P1-MOD-02
Track: MOD
Goal: Fix `persistModule` in `internal/hwmon/autoload.go` so running `--probe-modules` twice on a dual-chip board keeps both detected modules in `/etc/modules-load.d/ventd.conf` instead of the second run overwriting the first run's module list.

## Care level
Low-to-medium risk. File is install-time config, not runtime-critical. Main concern is atomicity: the rewrite must be atomic (tmp + rename) and must not drop modules that are ventd's own previous entries. Idempotency is required — running probe N times with the same hardware must produce the same file content (N=1 case).

## Context you should read first

- `internal/hwmon/autoload.go` — existing `persistModule` / `writeModuleLoadFile` code. Understand the current overwrite path.
- `internal/hwmon/modulesalias.go` — landed in #259 / P1-MOD-01. Informational only (module discovery is already subprocess-free).
- Any existing test file for the autoload path — pattern-match the fixture style; do NOT rewrite existing tests.

## What to do

1. Locate the current `persistModule` / file-writing logic in `internal/hwmon/autoload.go` that writes to `/etc/modules-load.d/ventd.conf`.

2. Change the behaviour from overwrite to append-with-dedup:
   - Read the existing file if present; parse its non-comment, non-empty lines as already-present module names (one per line, trim whitespace).
   - Union the existing set with the newly-detected module set.
   - Sort the union deterministically (lexical) so repeated runs produce identical output.
   - Write the union back via the existing atomic write path (tmp + rename). If the file does not exist, create it.

3. Preserve all existing semantics:
   - Same file path, same permissions (0644), same owner (whatever the current code sets — do not touch).
   - Same header comment if one is emitted today. If no header exists, do not introduce one.
   - Same error propagation contract (return the same error types the current function returns).

4. Add tests proving idempotency + append:
   - First probe on fixture-chip-A produces file with `{module-A}`.
   - Second probe on fixture-chip-B (different hardware fixture) produces file with `{module-A, module-B}`.
   - Third probe (same as second) produces identical file content (idempotency).
   - Empty-probe (no modules detected) does NOT truncate an existing populated file.

5. Verify:
   - `CGO_ENABLED=0 go build ./...` — clean
   - `go test -race -count=1 ./internal/hwmon/...` — pass
   - `go vet ./...` — clean
   - `gofmt -l internal/hwmon/autoload.go` — empty

## Definition of done

- Running `--probe-modules` twice on a simulated dual-chip board keeps both modules in the output file.
- No module is duplicated in the output file regardless of how many times probe runs.
- Empty probe does not wipe an existing populated file.
- File write remains atomic (tmp + rename).
- All prior `internal/hwmon` tests still pass.
- New test(s) prove the four bullet points under step 4.

## Out of scope for this task

- Tests outside `internal/hwmon/` scope.
- Changing the module-discovery logic (P1-MOD-01 / #259 owns that).
- Changing the file path, permissions, or ownership.
- Adding new dependencies.
- Moving module loading to the runtime daemon (that's P3-MODPROBE-01).
- Touching `scripts/install.sh` or `deploy/`.

## Branch and PR

- Work on branch: `claude/P1-MOD-02-persist-append`
- Commit style: conventional commits (`fix(hwmon):` or `refactor(hwmon):`)
- Open a draft PR on completion with title: `fix(hwmon): append-not-overwrite in persistModule (P1-MOD-02)`
- PR description must include: the goal verbatim, bulleted files-touched list, "How I verified" section showing test output, link back to task ID: P1-MOD-02.
- CHANGELOG.md `## Unreleased` entry under `### Fixed`: one line referencing P1-MOD-02.

## Constraints

- Do not touch files outside: `internal/hwmon/autoload.go`, a new or existing test file under `internal/hwmon/` for the new tests, and `CHANGELOG.md`.
- Do not add new direct dependencies.
- Keep the main binary `CGO_ENABLED=0` compatible.
- Preserve atomic write semantics (tmp + rename). Do NOT write directly to the final path.
- If blocked, push WIP, open draft PR with `[BLOCKED]` prefix, write a `Blocker` section in the description.

## Reporting

On completion:
- STATUS: done | partial | blocked
- PR: <url>
- SUMMARY: <= 200 words
- CONCERNS: second-guessing you had while working
- FOLLOWUPS: work you noticed that isn't in scope
