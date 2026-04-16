# Coverage Snapshot

Last measured: 2026-04-16 (Go 1.25.0, CGO_ENABLED=1) — full re-measure
after the controller safety suite (#118), allow_stop fix (#124), and
setup orchestration invariant suite.

Command:

```
CGO_ENABLED=1 go test -race -cover ./...
```

Race tests pass clean across the tested packages; numbers below are
statement coverage from the same run.

## Per-package

| Package                       | Coverage | Notes                                                                 |
|-------------------------------|---------:|-----------------------------------------------------------------------|
| `cmd/ventd`                   |   15.1 % | Daemon entrypoint plus the folded-in `--list-fans-probe` and `--preflight-check` subcommands. Most logic delegated to `internal/*`. |
| `internal/calibrate`          |   65.5 % | Curve fitting + RPM detection tested. Now includes `ZeroPWMSentinel` 8-case suite (PWM=0 escalation). |
| `internal/config`             |   61.8 % | `Load`+`ResolveHwmonPaths` integration covered. `EnrichChipName` 9-case suite landed alongside the writer-side ChipName population. |
| `internal/controller`         |   88.0 % | Control-loop orchestration. Every rule in `.claude/rules/hwmon-safety.md` is now bound 1:1 to a named subtest in `safety_test.go` (`TestSafety_Invariants`); all 12 subtests are live — the two previously-skipped cases for #115 (allow_stop gate) and #116 (Restore on ctx cancel) flipped green in #124. |
| `internal/curve`              |  100.0 % | Linear / Fixed / Mix all table-driven. `MixFunc` parser exhausted. |
| `internal/hwdiag`             |   87.2 % | Small, mostly pure helpers. |
| `internal/hwmon`              |   31.4 % | `DiagnoseHwmon` (7 cases), `RecoverAllPWM` (5 cases), udev-rule behaviour (8 cases). Largest remaining untested surface is `autoload.go` (sensors-detect parsing + module probing). |
| `internal/nvidia`             |   30.7 % | NVML bindings via purego; init + fan-query paths covered. |
| `internal/sdnotify`           |   95.3 % | systemd notify protocol implementation; full suite covering env-absent, env-present, ping cadence, stop semantics. |
| `internal/setup`              |   67.3 % | Manager lifecycle, buildConfig (20 cases), validateGeneratedConfig, diag emitters, fixture helpers, and orchestration invariant suite. Remaining 32.7 % locked behind hard-coded sysfs paths (#131) and concrete calibrate.Manager (#132). |
| `internal/watchdog`           |   23.2 % | Restore-on-exit plumbing; per-entry panic recovery covered. |
| `internal/web`                |   53.9 % | Auth, session, cert generation well-covered; wizard HTTP handlers (`handleSetup*`, `handleCalibrateAbort`, `handleDetectRPM`, `handleSystemReboot`) remain the gap (#133). |
| `internal/monitor`            |       — | No test files. NVML/temp scrape loop, hard to test without hardware. Add `fs.FS` overrideable tests next. |

## Highest-value gaps

Ranked by `(100 − coverage) × estimated package size`:

1. `internal/hwmon` autoload.go — sensors-detect parsing branches and
   module-probe loop are still untested. Lower priority now that the
   probing has moved to install time and is fired explicitly via
   `ventd --probe-modules`.
2. `internal/web` — 53.9 % is solid for production use; remaining
   wizard HTTP handlers tracked by #133.
3. `internal/setup` — 67.3 % covered. Remaining 32.7 % is locked
   behind hard-coded sysfs/procfs paths (#131) and a concrete
   `calibrate.Manager` (#132). Extracting interfaces would unlock
   13 skipped subtests.
4. `internal/watchdog` — 23.2 %. Small package, but the restore-on-
   exit guarantees are safety-critical.
5. `internal/monitor` — still no tests. Lowest urgency (read-only).

(`internal/controller` was on this list in the previous snapshot; the
`TestSafety_Invariants` suite now binds every hwmon-safety rule to a
named subtest and takes the package from 12.0 % → 85.9 %.)

## Packages with no test files

- `internal/monitor` — only remaining production package without tests.

(`cmd/list-fans-probe` and `cmd/preflight-check` no longer exist as
standalone packages; they were folded into `cmd/ventd` as
`--list-fans-probe` and `--preflight-check` subcommands and are
exercised through the same code paths the validation matrix uses.)

## What changed since the previous snapshot

| Package              | Then    | Now     | Reason                                          |
|----------------------|--------:|--------:|-------------------------------------------------|
| `internal/controller`| 12.0 %  | 85.9 %  | Safety-invariant suite (#118) + allow_stop/ctx-cancel fix (#124). |
| `internal/config`    | 61.8 %  | 67.2 %  | Additional integration coverage from resolver tests. |
| `internal/setup`     |  5.9 %  | 67.3 %  | Manager lifecycle, buildConfig (20 cases), diag emitters, orchestration invariant suite. Previous snapshot was stale — existing tests already covered 67.3 %; this PR adds invariant-binding tests that exercise existing paths. |
| `internal/nvidia`    |  5.0 %  | 30.7 %  | Fan-query and init paths expanded. |
| `internal/web`       | 48.9 %  | 53.9 %  | Additional handler coverage. |
