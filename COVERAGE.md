# Coverage Snapshot

Last measured: 2026-04-15 (Go 1.25.0, CGO_ENABLED=1) — refreshed after
the chip-agnostic udev / Load+ChipName / install-time module probe / 2s
watchdog promise overhauls.

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
| `internal/controller`         |   12.0 % | Control-loop orchestration. Smoke paths only — biggest untested code path on the safety-critical fan-write side. |
| `internal/curve`              |  100.0 % | Linear / Fixed / Mix all table-driven. `MixFunc` parser exhausted. |
| `internal/hwdiag`             |   87.2 % | Small, mostly pure helpers. |
| `internal/hwmon`              |   31.4 % | Now includes `DiagnoseHwmon` (7 cases), `RecoverAllPWM` (5 cases), and the udev-rule behaviour suite (8 cases). Largest remaining untested surface is `autoload.go` (sensors-detect parsing + module probing). |
| `internal/nvidia`             |    5.0 % | NVML bindings via purego; smoke covers init path. Fan-side path needs a real NVIDIA device or a mock harness. |
| `internal/sdnotify`           |   95.2 % | New package. systemd notify protocol implementation; full suite for `Notify`, `WatchdogInterval`, `StartHeartbeat` covering env-absent, env-present, ping cadence, stop semantics. |
| `internal/setup`              |    5.9 % | Largest gap by far. Handlers are thin glue over `internal/calibrate` + `internal/config`; covered helpers include `chipNameOf` (6 cases) plus the existing dmi/preflight diag suites. |
| `internal/watchdog`           |   23.2 % | Restore-on-exit plumbing; per-entry panic recovery covered. |
| `internal/web`                |   48.9 % | Auth, session, cert generation well-covered; the wizard-driving handlers (`handleSetup*`, `handleCalibrateAbort`, `handleDetectRPM`, `handleSystemReboot`) remain the gap. |
| `internal/monitor`            |       — | No test files. NVML/temp scrape loop, hard to test without hardware. Add `fs.FS` overrideable tests next. |

## Highest-value gaps

Ranked by `(100 − coverage) × estimated package size`:

1. `internal/setup` — 5.9 % covered. **The hot path the README's
   "zero terminal after install" promise lives in.** Driving the
   wizard handlers through `httptest` against fake hwmon fixtures is
   the highest-leverage single test investment in the tree.
2. `internal/hwmon` autoload.go — sensors-detect parsing branches and
   module-probe loop are still untested. Lower priority now that the
   probing has moved to install time and is fired explicitly via
   `ventd --probe-modules`.
3. `internal/web` — 48.9 % is solid for production use; remaining
   handlers are mostly UI glue.
4. `internal/controller` — small package (~142 statements) but
   safety-critical. A control-loop tick driven against a fake
   sensor/fan pair would land quickly and lock in invariants.
5. `internal/monitor` — still no tests. Lowest urgency (read-only).

## Packages with no test files

- `internal/monitor` — only remaining production package without tests.

(`cmd/list-fans-probe` and `cmd/preflight-check` no longer exist as
standalone packages; they were folded into `cmd/ventd` as
`--list-fans-probe` and `--preflight-check` subcommands and are
exercised through the same code paths the validation matrix uses.)

## What changed since the previous snapshot

| Package              | Then    | Now     | Reason                                          |
|----------------------|--------:|--------:|-------------------------------------------------|
| `cmd/ventd`          | 23.9 %  | 15.1 %  | Folded in two helper-binary code paths; their existing zero-coverage statements are now counted here. |
| `internal/calibrate` | 61.3 %  | 65.5 %  | `ZeroPWMSentinel` 8-case suite added.           |
| `internal/config`    | 34.5 %  | 61.8 %  | `EnrichChipName` (9 cases) + `Load`+`Resolve` integration (6 cases). |
| `internal/curve`     |     —   | 100.0 % | New 7-case suite for Linear/Fixed/Mix.          |
| `internal/hwmon`     | 26.1 %  | 31.4 %  | `DiagnoseHwmon` (7), `RecoverAllPWM` (5), udev rule behaviour (8). |
| `internal/sdnotify`  |     —   | 95.2 %  | New package; 8-case suite.                       |
| `internal/setup`     |  4.8 %  |  5.9 %  | `chipNameOf` 6-case suite (small bump; bigger gap remains). |
