You are Claude Code, working on the ventd repository.

## Task
ID: P1-HOT-01
Track: HOT
Goal: Eliminate per-tick allocations in the controller hot loop. Six specific optimisations; measurable by `BenchmarkTick_5Fans_10Sensors` (see T-HOT-01 spec).

## Model
Sonnet 4.6 (performance optimization, well-scoped, no new safety invariants).

## Context you should read first

- `internal/controller/controller.go` — the current hot loop.
- `internal/controller/safety_test.go` — the hwmon-safety invariants this must preserve.
- `.claude/rules/hwmon-safety.md` — invariants that cannot regress.
- `internal/curve/*.go` — the curves evaluated per tick (you may touch Mix.Evaluate specifically).
- `internal/config/config.go` — where cfg is loaded; you'll want a snapshot pattern.

## What to do

Apply these six optimisations in order. Each is independent; if one fights a safety invariant, STOP and report blocker (don't ship a partial).

1. **Preallocate sensors/smoothed maps.** Today each tick reallocates the `map[string]float64` for sensors and the smoothed-sensor map. Lift them to `*Controller` fields, `clear()` at tick start, reuse.

2. **Cache compiled curve graph.** Curve compilation currently happens per-tick. Cache the compiled graph on `*Controller`, invalidate via a SIGHUP-triggered reload.

3. **SIGHUP-refreshed config snapshot.** Replace any per-tick `cfg.Load()` calls with a `sync/atomic.Pointer[Config]` that SIGHUP swaps. Tick reads the pointer once at the top and uses that snapshot for the whole tick (torn-read-safe).

4. **Cache maxRPM per RPM-target fan at startup.** Today each tick walks hwmon to look up `fan_max`. Read once at controller startup, cache in the per-fan runtime struct.

5. **Binary-search Points curve.** `CurvePoints.Evaluate` probably does linear search today. Replace with `sort.Search` (binary), since points are already sorted-ascending by temp.

6. **sync.Pool the vals slice in Mix.Evaluate.** `Mix.Evaluate` allocates a `[]float64` every call. Put it behind a `sync.Pool` with a reset on Put.

After the six optimisations:

7. Run `go test -race -count=1 ./internal/controller/... ./internal/curve/...` — all tests must pass, including the full hwmon-safety invariant suite.

8. Run `go vet ./...` and `golangci-lint run ./internal/controller/... ./internal/curve/...` — both clean.

9. Verify `pwm_enable` save/restore semantics in controller are unchanged. If you accidentally broke the restore path, STOP and report blocker.

## Definition of done

- All six optimisations visible in the diff.
- `pwm_enable` save/restore unchanged.
- All existing tests pass under `-race`.
- `go vet` + `golangci-lint` clean.
- No new package dependencies.
- CHANGELOG.md `## Unreleased` / `### Changed` entry: one line referencing P1-HOT-01.

## Out of scope for this task

- Tests outside the scope this task targets per the testplan catalogue. T-HOT-01 will add benchmarks + alloc assertions separately — don't add those here. P-task PRs add tests only as documented in testplan §18 row R19.
- Changing curve mathematics, control algorithm, or hysteresis.
- Touching the HAL layer (P1-HAL-01 is done; this is purely a controller hot-loop task).
- Changing `internal/config/config.go` struct shape (only the load-pattern wrapper).
- Modifying any rule file.

## Branch and PR

- Work on branch: `claude/P1-HOT-01-hot-loop-alloc-elim`
- Commit style: conventional commits (one commit per optimisation is fine, or one squash-worthy commit with a full body).
- Open a draft PR on completion with title: `perf(controller): eliminate per-tick allocations (P1-HOT-01)`
- PR description must include: the goal verbatim, a numbered list of the six optimisations with file:line anchors, "How I verified" section with test output, link back to task ID: P1-HOT-01.

## Constraints

- Do not touch files outside: `internal/controller/controller.go`, `internal/curve/*.go`, `internal/config/config.go` (only for the snapshot pattern), `CHANGELOG.md`.
- Do not add new dependencies.
- `CGO_ENABLED=0` compatible.
- Preserve all safety guarantees.
- If blocked, push WIP with `[BLOCKED]` prefix.

## Reporting

- STATUS: done | partial | blocked
- PR: <url>
- SUMMARY: <= 200 words
- CONCERNS: any second-guessing
- FOLLOWUPS: work you noticed out of scope
