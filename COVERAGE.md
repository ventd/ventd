# Coverage Snapshot

Last measured: 2026-04-16 (Go 1.25.0, CGO_ENABLED=1) — full re-measure
after the controller safety suite (#118), allow_stop fix (#124), the
setup orchestration invariant suite, the `autoload.go` parser +
driver-need heuristic coverage added alongside this snapshot, and the
setup-Manager sysfs/procfs root extraction that closed #131 (#163).

> **Diagnostic suite added 2026-04-17** — see `docs/TESTING.md` and
> `scripts/diagnose-tests.sh`. New test files push watchdog from
> 23.2 % → 85.5 %, add fuzz seeds for `config.Parse` and the
> `sensors-detect` parser, and fill the `handleDetectRPM` /
> `handleCalibrateAbort` / `handleSetupStatus` handler gap. The
> coverage numbers below predate those additions; rerun the command
> above to refresh.

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
| `internal/hwmon`              |   41.1 % | `DiagnoseHwmon` (7 cases), `RecoverAllPWM` (5 cases), udev-rule behaviour (8 cases), and `autoload.go` parser/enumerator surface (8 functions, 7 at 100 %, `moduleFromPath` at 94.1 %). Install-time exec paths (`AutoloadModules`, `tryModuleCandidates`, `runSensorsDetect`, `enumerateHwmonCandidates`, `installLmSensors`) remain untested — they shell out to `modprobe`/`sensors-detect` and require root plus real hardware. |
| `internal/nvidia`             |   30.7 % | NVML bindings via purego; init + fan-query paths covered. |
| `internal/sdnotify`           |   95.3 % | systemd notify protocol implementation; full suite covering env-absent, env-present, ping cadence, stop semantics. |
| `internal/setup`              |   79.5 % | Manager lifecycle, buildConfig (20 cases), validateGeneratedConfig, diag emitters, fixture helpers, orchestration invariant suite, and fixture-rooted coverage for the six hardware-discovery methods (#163 → `manager_roots_test.go`). Remaining 20.5 % locked behind concrete calibrate.Manager (#132) and wizard HTTP handlers (#133). |
| `internal/watchdog`           |   23.2 % | Restore-on-exit plumbing; per-entry panic recovery covered. |
| `internal/web`                |   53.9 % | Auth, session, cert generation well-covered; wizard HTTP handlers (`handleSetup*`, `handleCalibrateAbort`, `handleDetectRPM`, `handleSystemReboot`) remain the gap (#133). |
| `internal/monitor`            |   97.2 % | Hwmon scan path fully exercised via a fake sysfs tree in `t.TempDir()`; a package-level `scanRoot` string makes the root overridable from tests. `scanNVML` is covered incidentally on the dev-box (real RTX 4090); on CI (no NVML) the package floor is ≈77 % — the ≈20 statements behind `nvidia.Init` success are a v0.4 item (needs a GPU mock in `internal/nvidia`). |

## Highest-value gaps

Ranked by `(100 − coverage) × estimated package size`:

1. `internal/hwmon` — autoload.go parsers and driver-need heuristics
   are now covered (41.1 % package total). The remaining gap is the
   install-time module-probe loop that shells out to `modprobe` and
   `sensors-detect`; those require root plus real hardware and are
   exercised only via `ventd --probe-modules`.
2. `internal/web` — 53.9 % is solid for production use; remaining
   wizard HTTP handlers tracked by #133.
3. `internal/setup` — 79.5 % covered after #163 threaded the
   sysfs/procfs root paths through the Manager (`hwmonRoot`,
   `procRoot`, `powercapRoot`) and closed #131. Remaining 20.5 % is
   locked behind a concrete `calibrate.Manager` (#132) and the wizard
   HTTP handlers that live in `internal/web` (#133). Extracting the
   calibrate interface would unlock 9 skipped subtests.
4. `internal/watchdog` — 23.2 %. Small package, but the restore-on-
   exit guarantees are safety-critical.
5. `internal/monitor` — landed at 97.2 % on the dev-box (≈77 % CI
   floor, scanNVML excluded pending a GPU mock).

(`internal/controller` was on this list in the previous snapshot; the
`TestSafety_Invariants` suite now binds every hwmon-safety rule to a
named subtest and takes the package from 12.0 % → 85.9 %.)

## Packages with no test files

None. `internal/monitor` landed its first test suite (hwmon scan
via injected `scanRoot`) and is out of this list.

(`cmd/list-fans-probe` and `cmd/preflight-check` no longer exist as
standalone packages; they were folded into `cmd/ventd` as
`--list-fans-probe` and `--preflight-check` subcommands and are
exercised through the same code paths the validation matrix uses.)

## What changed since the previous snapshot

| Package              | Then    | Now     | Reason                                          |
|----------------------|--------:|--------:|-------------------------------------------------|
| `internal/controller`| 12.0 %  | 85.9 %  | Safety-invariant suite (#118) + allow_stop/ctx-cancel fix (#124). |
| `internal/config`    | 61.8 %  | 67.2 %  | Additional integration coverage from resolver tests. |
| `internal/setup`     |  5.9 %  | 79.5 %  | Manager lifecycle, buildConfig (20 cases), diag emitters, orchestration invariant suite, and (after #163) fixture-rooted tests for the six hardware-discovery methods via injected `hwmonRoot`/`procRoot`/`powercapRoot`. Previous jump 5.9 → 67.3 was from invariant-binding tests against existing paths; 67.3 → 79.5 is the #163 root-extraction unlocking the four skipped discovery/profile subtests. |
| `internal/nvidia`    |  5.0 %  | 30.7 %  | Fan-query and init paths expanded. |
| `internal/web`       | 48.9 %  | 53.9 %  | Additional handler coverage. |
| `internal/monitor`   |   — %   | 97.2 %  | First test suite: `scanRoot` override + fake sysfs in `t.TempDir()`. Hwmon path 100 %; scanNVML covered incidentally on dev-box (RTX 4090), v0.4 gets a GPU mock. |
| `internal/hwmon`     | 31.4 %  | 41.1 %  | Table-driven tests for `autoload.go` parsers + driver-need heuristics (8 functions). Install-time `modprobe`/`sensors-detect` exec paths remain untested (require root + real hardware). |
