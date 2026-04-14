# Coverage Snapshot

Last measured: 2026-04-15 (Go 1.25.0, CGO_ENABLED=1)

Command:

```
CGO_ENABLED=1 go test -race -cover ./...
```

Race tests pass clean across the tested packages; numbers below are statement
coverage from the same run.

## Per-package

| Package                       | Statements | Coverage | Notes                                                                 |
|-------------------------------|-----------:|---------:|-----------------------------------------------------------------------|
| `cmd/ventd`                   |        234 |   23.9 % | Daemon entrypoint. Most logic delegated to `internal/*`.              |
| `cmd/list-fans-probe`         |          — |      — % | No test files. CLI probe tool, excluded from coverage instrumentation.|
| `cmd/preflight-check`         |          — |      — % | No test files. Pre-install environment sanity checker.                |
| `internal/calibrate`          |        488 |   61.3 % | Best-covered non-leaf package. Curve fitting + RPM detection tested.  |
| `internal/config`             |        226 |   34.5 % | YAML round-trip covered; `resolveHwmonPaths` still unimplemented (#2).|
| `internal/controller`         |        142 |   12.0 % | Control-loop orchestration. Covered only via smoke paths.             |
| `internal/curve`              |          — |      — % | No test files. Pure-math curves (`linear`, `fixed`, `mix`) — see #6.  |
| `internal/hwdiag`             |         47 |   87.2 % | Small, mostly pure helpers.                                           |
| `internal/hwmon`              |       1081 |   26.1 % | Largest package; most uncovered code is sysfs I/O and installers.     |
| `internal/monitor`            |          — |      — % | No test files. NVML/temp scrape loop, hard to test without hardware.  |
| `internal/nvidia`             |        202 |   30.7 % | NVML bindings via purego; smoke test covers init path.                |
| `internal/setup`              |        610 |    4.8 % | First-boot wizard flow — mostly HTTP handlers, untested end-to-end.   |
| `internal/watchdog`           |         69 |   23.2 % | Restore-on-exit plumbing; happy-path only.                            |
| `internal/web`                |        732 |   48.9 % | Auth, session, cert generation well-covered; handlers less so.        |
| **Total (measured)**          |   **3 831** | **32.3 %** | Excludes four packages with no test files.                          |

## Highest-value gaps

Ranked by `(100 − coverage) × statements` across measured packages:

1. `internal/hwmon` — ~800 uncovered statements. Sysfs / modprobe I/O is the
   hard part; a fixture-backed test for `resolveHwmonPaths` (ITEM 8) will
   chip at this.
2. `internal/setup` — ~581 uncovered statements, 4.8 % covered. Handlers are
   thin glue over `internal/calibrate` + `internal/config`; driving them
   through `httptest` would land quickly.
3. `internal/web` — ~374 uncovered statements. The unsealed handlers are
   `handleSetup*`, `handleCalibrateAbort`, `handleDetectRPM`,
   `handleSystemReboot` — all need fake helpers.
4. `internal/controller` — 142 statements at 12.0 %. Control-loop ticks
   against a fake sensor/fan pair would be a small, high-ROI test.
5. `internal/curve` — no tests, pure-math. ITEM 6 targets this directly.

## Packages without test files

- `cmd/list-fans-probe`
- `cmd/preflight-check`
- `internal/curve` *(queued — ITEM 6)*
- `internal/monitor`

These are skipped by `go test -cover` (the Go 1.25 cover runtime emits
`no such tool "covdata"` for any zero-test package in the module graph; the
other packages' coverage numbers are unaffected).
