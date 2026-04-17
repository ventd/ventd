You are Claude Code, working on the ventd repository.

## Task
ID: T0-INFRA-03
Track: INFRA
Goal: implement the faketime fixture (monotonic clock, goroutine-safe, advance/wait semantics) and wire it into at least one calibrate test.

## Model
Claude Sonnet 4.6.

## Context you should read first (≤ 15 files)
- `internal/testfixture/faketime/faketime.go` — the skeleton (T0-INFRA-01); extend, don't rewrite the package surface
- `internal/testfixture/faketime/faketime_test.go` — skeleton's own tests (if any)
- `internal/calibrate/*_test.go` — scan for a test using `time.Sleep`, `time.Now`, `time.After`, `time.NewTimer`, or similar; pick one to migrate
- `internal/testfixture/fakehwmon/fakehwmon.go` — newly merged in T0-INFRA-02; the shape of its constructor/cleanup is the template for faketime to follow
- go.mod — confirm no new deps

## What to do
1. **Replace the skeleton content of `internal/testfixture/faketime/faketime.go`** with a real implementation:
   - Type `Clock`, public methods all goroutine-safe (mutex-protected):
     - `Now() time.Time`
     - `Advance(d time.Duration)` — moves time forward, fires any pending timers/tickers whose deadline is reached
     - `After(d time.Duration) <-chan time.Time`
     - `NewTimer(d time.Duration) *Timer` with `Stop() bool` and `C <-chan time.Time` to mirror stdlib shape
     - `NewTicker(d time.Duration) *Ticker` similarly
     - `WaitUntil(t *testing.T, condition func() bool, timeout time.Duration)` — polls in small real-time increments; fails the test on timeout
   - Constructor `New(t *testing.T, initial time.Time) *Clock`, registers `t.Cleanup()` to report orphan timers.
2. **Tests in `internal/testfixture/faketime/faketime_test.go`** covering:
   - Advance firing a single timer
   - Advance firing multiple timers in correct order
   - Ticker firing across multiple Advance calls
   - After channel behaviour
   - Concurrent `Advance` + `NewTimer` without races
   - `WaitUntil` happy path + timeout path
3. **Migrate exactly one calibrate test** to faketime. Pick a test that currently uses real time (sleep or wall-clock assertion). Keep its asserted behaviour bit-identical; just replace the time source.
4. **CHANGELOG.md** under `## [Unreleased]` / `### Added`:
   `- test: faketime fixture for deterministic timer tests (#T0-INFRA-03)`

## Definition of done
- `go build ./...` clean.
- `go test -race ./internal/testfixture/faketime` clean.
- `go test -race ./internal/calibrate` clean.
- Migrated calibrate test runs in ≪ its original wall-clock duration.

## Out of scope
- Other calibrate tests (only one migration).
- controller / watchdog / acoustic migrations (those are later tasks).
- Tests beyond what this task adds.
- Renaming/restructuring the faketime package.

## Branch and PR
- Branch: `claude/INFRA-faketime-2e8b4`
- Commit prefix: `test:`
- Draft PR: `test: implement faketime fixture (T0-INFRA-03)`
- PR body must include:
  - Goal (verbatim)
  - Files-touched bullet list
  - Which calibrate test migrated + why (what was the time-dependency?)
  - "How I verified" with literal outputs
  - Task ID

## Constraints
- Allowlist: `internal/testfixture/faketime/**`, exactly one `*_test.go` file in `internal/calibrate/`, `CHANGELOG.md`. Nothing else unless justified inline.
- stdlib only.
- CGO_ENABLED=0 preserved.
- Goroutine-safety is load-bearing — `-race` must pass.

## Reporting
On completion, PR body must carry:
- STATUS: done | partial | blocked
- BEFORE-SHA / AFTER-SHA
- POST-PUSH GIT LOG: `git log --oneline main..HEAD`
- BUILD: `go build ./...`
- TEST: `go test -race ./internal/testfixture/faketime ./internal/calibrate`
- MIGRATED: name of the calibrate test, before/after wall-clock timing if measurable
- GOFMT: `gofmt -l internal/testfixture/faketime internal/calibrate`
- CONCERNS / FOLLOWUPS
