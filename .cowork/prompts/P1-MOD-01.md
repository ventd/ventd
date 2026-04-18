You are Claude Code, working on the ventd repository.

## Task
ID: P1-MOD-01
Track: MOD
Goal: Eliminate the `modinfo` subprocess-shell-out from `internal/hwmon/autoload.go`. Parse `/lib/modules/$(uname -r)/modules.alias` and `modules.builtin-modinfo` directly via file reads. Result: zero `modinfo` invocations during normal Linux probe flow.

## Care level
Medium. Autoload runs once at daemon startup (and once per `--probe-modules`); the shell-out adds ~50ms per unknown module and fails on minimal/Alpine systems where `modinfo` isn't installed. Direct parsing is faster and portable. Low safety surface (this is module-detection advisory only — modules actually loaded via the privileged modprobe oneshot).

## Context you should read first

- `internal/hwmon/autoload.go` — the current autoload implementation; find every `exec.Command("modinfo", ...)` call site.
- `internal/hwmon/autoload_test.go` (if present) — the test fixtures and any existing parser tests.
- `internal/testfixture/fakehwmon/` (if present post-T0-INFRA-02) — the fixture library.
- The format of `modules.alias`:
  ```
  alias pci:v00008086d00001234sv*sd*bc*sc*i* coretemp
  alias of:N*T*Cnvidia,tegra-pwm* pwm_tegra
  ```
  Format: `alias <pattern> <module>` — one line each.
- The format of `modules.builtin-modinfo`:
  ```
  coretemp.filename=(builtin)
  coretemp.description=Intel CPU temperature
  ```
  NUL-separated records of `module.key=value`.

## What to do

1. Audit `internal/hwmon/autoload.go`:
   - Find every call that invokes `modinfo`, `modprobe`, or similar.
   - List them in a comment block at the top of your main edit as your working checklist.

2. Implement two pure-Go helpers:
   - `parseModulesAlias(reader io.Reader) (map[string][]string, error)` — returns `map[module][]patterns`. Handles comments (#), blank lines, multi-space.
   - `parseModulesBuiltinModinfo(reader io.Reader) (map[string]map[string]string, error)` — returns `map[module]map[key]value`. NUL-record parser.

3. Replace each `modinfo` subprocess call with a lookup from the in-memory parsed data.
   - Read both files **once** at autoload startup, cache in package-level or Autoloader struct.
   - Path: `/lib/modules/$(uname -r)/modules.alias` and `.../modules.builtin-modinfo`. Use `os.ReadFile`; wrap in a `ModulesRoot` override for testability (default `/lib/modules/$(uname -r)`).

4. Unit test coverage for both parsers:
   - Known-good input → expected parsed output.
   - Malformed lines → skipped with slog.Warn, not a fatal error.
   - Empty input → empty map, no error.
   - UTF-8 and edge chars → no panic.
   - A corpus of real kernel samples (Ubuntu 24.04, Fedora 41, Arch) placed in `internal/hwmon/testdata/modules-alias/` — if those aren't already present, include 2-3 real `modules.alias` snippets (~50 lines each) captured from running systems.

5. Verify no regressions: run `go test -race -count=1 ./internal/hwmon/...` — all existing tests pass plus the new ones.

6. Verify no subprocess:
   - Grep `internal/hwmon/autoload.go` for `exec.Command`, `exec.CommandContext`, `exec.LookPath`. The modinfo/modprobe invocations must be gone.
   - `strace -e execve` on a local autoload run should show no modinfo calls. You don't need to actually run strace — just verify by grep that no subprocess wrapper remains.

7. `go vet ./...` and `golangci-lint run ./internal/hwmon/...` — both clean.

## Definition of done

- `internal/hwmon/autoload.go` contains zero `exec.Command`/`exec.CommandContext` calls for `modinfo` or `modprobe`.
- Two new parsers with unit tests, coverage ≥ 85% for the parser funcs.
- `go test -race -count=1 ./internal/hwmon/...` green.
- `go vet` + `golangci-lint` clean.
- `CGO_ENABLED=0 go build ./cmd/ventd/` succeeds.
- Binary size delta ≤ +20 KB.
- CHANGELOG.md `## Unreleased` / `### Changed` entry: one line referencing P1-MOD-01.

## Out of scope for this task

- Tests outside the scope this task targets per the testplan catalogue. P-task PRs add tests only as documented in testplan §18 row R19. The parser tests here are in scope because they cover the new code added in this PR.
- Modifying the privileged `ventd-modprobe` oneshot (P3-MODPROBE-01).
- Changing module-load policy or allowlists.
- Touching `/etc/modules-load.d` persistence logic (that's P1-MOD-02, next task).
- New dependencies. Stdlib only.

## Branch and PR

- Work on branch: `claude/P1-MOD-01-drop-modinfo-shellout`
- Commit style: conventional commits.
- Open a draft PR on completion with title: `perf(hwmon): drop modinfo shellouts, parse modules.alias directly (P1-MOD-01)`
- PR description: goal verbatim, bulleted files-touched list, "How I verified" with test output, task ID: P1-MOD-01.

## Constraints

- Do not touch files outside: `internal/hwmon/**`, `CHANGELOG.md`.
- No new dependencies.
- `CGO_ENABLED=0` compatible.
- Preserve all safety guarantees.
- If blocked, push WIP with `[BLOCKED]` prefix, write a `Blocker` section.

## Reporting

- STATUS: done | partial | blocked
- PR: <url>
- SUMMARY: <= 200 words
- CONCERNS: any second-guessing
- FOLLOWUPS: work you noticed out of scope
