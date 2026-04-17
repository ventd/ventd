# Test Suite Summary

Snapshot: 2026-04-17. Regenerate with:

```sh
go test -count=1 -cover ./...
scripts/diagnose-tests.sh
```

---

## At a glance

- **70 `*_test.go` files** across 14 packages
- **491 named test functions** (`Test*`) + **2 fuzz targets** (`Fuzz*`)
- **One diagnostic entry point**: `scripts/diagnose-tests.sh` (9 groups)
- **One workflow doc**: `docs/TESTING.md`
- **Full suite wall clock**: ~60 s with `-race`, dominated by `internal/web` (rod browser smoke) and `internal/calibrate` (2-s sysfs settle windows)

---

## Coverage by package

| Package | Tests | Coverage | Notes |
|---------|------:|---------:|-------|
| `cmd/ventd` | 11 | 22.5 % | Daemon entrypoint; `main()` is thin, logic lives in `internal/*`. Subcommands `--list-fans-probe` / `--probe-modules` not unit-tested (shell out to real tools). |
| `internal/calibrate` | 26 | 76.6 % | Sweep logic + `DetectRPMSensor` + abort/persist. Blocked coverage tracked by #132 (concrete manager interface). |
| `internal/config` | 81 | 76.3 % | YAML parse, validate, migrate, resolve, startup retry. `FuzzParseConfig` seeded with testdata + pathological YAML. |
| `internal/controller` | 40 | 86.4 % | Every hwmon-safety rule has a 1:1 subtest in `TestSafety_Invariants`. |
| `internal/curve` | 10 | 96.6 % | Linear / Fixed / Mix table-driven. |
| `internal/hwdiag` | 6 | 87.2 % | Diagnostic store + filter. |
| `internal/hwmon` | 56 | 41.1 % | Read/write + diagnose + autoload parsers + recover. `FuzzParseSensorsDetect` guards the sensors-detect stdout parser. Install-time exec paths (modprobe, sensors-detect) intentionally unmocked. |
| `internal/monitor` | 16 | 76.4 % | Hwmon scan via `scanRoot` override + fake sysfs; `scanNVML` CI floor, dev-box 97 %. |
| `internal/nvidia` | 12 | 22.8 % | `ErrNotAvailable` paths + `goStringFromC` + refcount concurrency. Real NVML calls exercised only on the dev-box via `nvidia_smoke_test.go`. |
| `internal/packaging` | 1 | — | Build tag marker only. |
| `internal/sdnotify` | 11 | 95.3 % | systemd `NOTIFY_SOCKET` protocol. |
| `internal/setup` | 111 | 71.9 % | Manager lifecycle + `buildConfig` 20-case table + fixture-rooted discovery (#163). Remaining gap locked behind #132 / #133. |
| `internal/watchdog` | 12 | 85.5 % | Every `restoreOne` branch covered (restore_matrix_test.go, this session). Up from 23.2 %. |
| `internal/web` | 98 | 66.2 % | Auth, session, cert gen, setup wizard state machine, detect/abort handlers, SSE, trust-proxy, selfsigned, rod-driven e2e smoke. |

---

## Test categories

### Safety (`scripts/diagnose-tests.sh safety`)

The three groups whose failure implies a user-visible safety regression:

| Group | File(s) | What it pins |
|-------|---------|--------------|
| `safety_watchdog` | `internal/watchdog/{watchdog_restore,restore_matrix}_test.go` | Every daemon-exit restore path. Panic-in-entry-N does not abort entries N+1..end. Deregister is LIFO. |
| `safety_controller` | `internal/controller/safety_test.go` (`TestSafety_Invariants`) | 12 named subtests, one per rule in `.claude/rules/hwmon-safety.md`. |
| `safety_calibrate` | `internal/calibrate/{calibrate,detect,safety}_test.go` | Abort restores PWM to pre-calibration value. Detect refuses nvidia fans, handles missing fan*_input, rejects concurrent calls. |

### Handler contracts (`internal/web`)

Every POST handler has three pinned properties: method enforcement (→ 405), missing-param rejection (→ 400), and state-machine exit codes (→ 409 on invalid transitions).

- `setup_handlers_test.go` — wizard state machine (#133 closed).
- `handlers_detect_abort_test.go` — DetectRPM / CalibrateAbort / SetupCalibrateAbort / SetupStatus (this session).
- `security_test.go` — auth middleware, session timing, rate limiting.
- `selfsigned_test.go` — TLS cert generation.
- `trustproxy_test.go` — X-Forwarded-For handling.

### Orchestration (`internal/setup`)

`TestBuildConfig_*` is a 20-case table over hardware profiles. `TestManager_*` + `manager_roots_test.go` drive the Manager against a `t.TempDir()`-rooted fake sysfs/procfs/powercap tree.

### Parsers

| Parser | Unit tests | Fuzz |
|--------|-----------|------|
| `config.Parse` (YAML) | `config_test.go`, `migrate_test.go`, `resolve_hwmon_test.go` | `FuzzParseConfig` |
| `hwmon.parseSensorsDetectModules` / `parseSensorsDetectChips` | `autoload_test.go` | `FuzzParseSensorsDetect` |
| `curve.ParseMixFunc` | `curve_test.go` | — (package at 96.6 %) |

Run the seed corpora:

```sh
go test -run FuzzParseConfig ./internal/config/...
go test -run FuzzParseSensorsDetect ./internal/hwmon/...
```

Run real fuzzing:

```sh
scripts/diagnose-tests.sh fuzz-long  # DIAGNOSE_FUZZTIME=30s by default
```

### Hardware-integration surrogates

Tests that stand in for real hardware by building fake sysfs trees in `t.TempDir()`:

- `internal/watchdog/restore_matrix_test.go` — `newFakeHwmon`, `newFakeRPMTarget`
- `internal/calibrate/detect_test.go` — `newRampingHwmon` (goroutine-animates a fan*_input file)
- `internal/monitor/monitor_test.go` — `scanRoot` override
- `internal/setup/fixtures_test.go` — hwmon + dmi + modprobe fixtures
- `internal/hwmon/udev_rule_test.go` — synthetic uevent streams

### Build-tag coverage

- `internal/nvidia/nonvidia_build_test.go` (`//go:build nonvidia`) — musl distro stub contract.
- `internal/hwmon/uevent_{linux,other}.go` — Linux-only netlink path plus a no-op stub; tested through `uevent_test.go`.

---

## Diagnostic runner (`scripts/diagnose-tests.sh`)

Nine groups, one parseable summary block:

```
DIAGNOSE-SUMMARY BEGIN
mode: all
race: -race
PASS     safety_watchdog        watchdog restore matrix
PASS     safety_controller      controller safety invariants
PASS     safety_calibrate       calibrate detect + abort
PASS     hwmon_parsers          hwmon autoload parsers
PASS     nvidia_unavailable     nvidia unavailable paths
PASS     web_handlers           web setup / detect / abort handlers
PASS     cmd_preflight          cmd/ventd preflight subcommand
PASS     config_fuzz_seed       config.Parse fuzz seeds
PASS     hwmon_fuzz_seed        hwmon sensors-detect fuzz seeds
overall: PASS
DIAGNOSE-SUMMARY END
```

Subcommands: `all` (default), `safety`, `web`, `cmd`, `fuzz`, `fuzz-long`.

Exit code: `0` iff every selected group passed. A single red group does NOT abort — everything broken surfaces in one pass.

---

## What the suite does NOT cover

Explicitly out of scope for `go test`:

1. **Real NVML calls** — require NVIDIA driver + GPU. Dev-box smoke only.
2. **Real sysfs writes to physical fans** — validation matrix in `validation/`.
3. **Install-time `modprobe` / `sensors-detect` exec paths** — require root + hardware.
4. **Full browser journeys** — `internal/web/e2e_test.go` has one rod smoke; full setup wizard E2E would need real backend hardware.
5. **PID-1 reboot refusal** — tracked by #177; current test pins CURRENT behaviour (200 + delayed goroutine), will flip to 409 when guard lands.
6. **SIGKILL / kernel panic / power loss recovery** — documented as out-of-envelope in `internal/watchdog/watchdog.go` header.

---

## CI gates

`.github/workflows/ci.yml` runs the following on every push:

| Job | Command |
|-----|---------|
| `golangci-lint` | `gofmt -l .` + `golangci-lint run --timeout=5m` (v2.1.6) |
| `build-and-test-ubuntu` / `-arch` / `-fedora` / `-alpine` / `-ubuntu-arm64` | `go test -race -count=1 ./...` on each distro |
| `cross-compile-matrix` | `GOOS=linux GOARCH={amd64,arm64} go build ./...` |
| `govulncheck` | `govulncheck ./...` |
| `headless-chromium` | rod-driven browser tests in `internal/web` |
| `shellcheck` | `scripts/*.sh` static analysis |
| `apparmor-parse-debian13` | Policy syntax check |
| `nix-drift` | Flake/lock consistency |

Known flake: `internal/config/TestLoadForStartup_RetryEventuallySucceeds` is occasionally timing-sensitive on cold CI workers; passes on rerun.

---

## References

- `docs/TESTING.md` — six named workflows for automated diagnosis, failing-test → file-to-open cheat sheet, ground rules for adding tests.
- `COVERAGE.md` — historical coverage snapshots and gap-tracking issue references (#132, #133, #163, #177).
- `scripts/diagnose-tests.sh` — diagnostic runner source.
- `.claude/rules/hwmon-safety.md` — the 12 safety rules bound 1:1 to `TestSafety_Invariants` subtests.
