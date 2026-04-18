# Ultrareview 1

- **Triggered:** manual (first ultrareview, phase-boundary after P1-HOT-02)
- **Date:** 2026-04-18
- **Last ultrareview SHA:** none (first)
- **Current main SHA:** b175fa7104bf2ed8a753270459a0b33871df3652
- **PRs audited since last ultrareview:** all (first run; covers ~#240–#263 visible in git log)
- **Lines changed since last:** 164 files changed, 18664 insertions(+), 462 deletions(-) (last 20 commits)

## Verdict summary

| Check | Verdict | Blockers | Warnings | Advisories |
|---|---|---|---|---|
| ULTRA-01 HAL contract | WARN | 0 | 1 | 0 |
| ULTRA-02 Safety posture | PASS | 0 | 0 | 0 |
| ULTRA-03 Rule files | PASS | 0 | 0 | 0 |
| ULTRA-04 Dead code | WARN | 0 | 2 | 4 |
| ULTRA-05 Duplication | WARN | 0 | 1 | 2 |
| ULTRA-06 Test coverage | WARN | 0 | 3 | 1 |
| ULTRA-07 Public API hygiene | WARN | 0 | 2 | 3 |
| ULTRA-08 Binary size | PASS | 0 | 0 | 1 |
| ULTRA-09 CHANGELOG hygiene | WARN | 0 | 1 | 0 |
| ULTRA-10 Dependency tree | FAIL | 1 | 0 | 1 |
| ULTRA-11 Config schema | PASS | 0 | 0 | 2 |
| ULTRA-12 Docs drift | WARN | 0 | 1 | 0 |

**Total: 1 blocker, 12 warnings, 14 advisories**

---

## Findings

### ULTRA-10 finding 1 (severity: blocker)

**17 Go standard library CVEs, all fixed by bumping Go toolchain from 1.25.0 to 1.25.9.**

The `go.mod` declares `go 1.25.0`. `govulncheck` reports 17 vulnerabilities reachable from production code paths, spanning `crypto/tls`, `crypto/x509`, `encoding/pem`, `encoding/asn1`, `net/http`, `net/url`, and `os`. The highest-severity ones are in 2026 advisories. All are patched in go1.25.9 (the highest fixed version across all 17).

**Evidence:**
```
Vulnerability #1: GO-2026-4947 (crypto/x509, fixed go1.25.9)
Vulnerability #2: GO-2026-4946 (crypto/x509, fixed go1.25.9)
Vulnerability #3: GO-2026-4870 (crypto/tls, fixed go1.25.9)
Vulnerability #4: GO-2026-4602 (os, fixed go1.25.8)
Vulnerability #5: GO-2026-4601 (net/url, fixed go1.25.8)
Vulnerability #6: GO-2026-4341 (net/url, fixed go1.25.6)
Vulnerability #7: GO-2026-4340 (crypto/tls, fixed go1.25.6)
Vulnerability #8: GO-2026-4337 (crypto/tls, fixed go1.25.7)
Vulnerability #9:  GO-2025-4175 (crypto/x509, fixed go1.25.5)
Vulnerability #10: GO-2025-4155 (crypto/x509, fixed go1.25.5)
Vulnerability #11: GO-2025-4013 (crypto/x509, fixed go1.25.2)
Vulnerability #12: GO-2025-4012 (net/http, fixed go1.25.2)
Vulnerability #13: GO-2025-4011 (encoding/asn1, fixed go1.25.2)
Vulnerability #14: GO-2025-4010 (net/url, fixed go1.25.2)
Vulnerability #15: GO-2025-4009 (encoding/pem, fixed go1.25.2)
Vulnerability #16: GO-2025-4008 (crypto/tls, fixed go1.25.2)
Vulnerability #17: GO-2025-4007 (crypto/x509, fixed go1.25.3)

go.mod line 3: go 1.25.0
```

Example trace (GO-2025-4009, pem.Decode):
```
#1: internal/web/selfsigned.go:103:24: web.fingerprintCert calls pem.Decode
```
Example trace (GO-2025-4008, crypto/tls ALPN):
```
#1: internal/web/server.go:719:31: web.Server.ListenAndServe calls http.Server.ServeTLS
    → tls.Conn.HandshakeContext
```

**Recommended follow-up:** File GitHub issue `chore: bump Go toolchain to go1.25.9`. Fix is one line in `go.mod`, plus `go mod tidy` and a rebuild. Must land before next production release.

---

### ULTRA-01 finding 1 (severity: warning)

**`contract_test.go:read_no_mutation` panics on GPU hosts — type assertion not gated by `fileBacked`.**

The `read_no_mutation` subtest iterates over `[hwmonBackendCase, nvmlBackendCase]`. The hwmon branch falls through to `st := ch.Opaque.(halHwmon.State)` — safe. The NVML branch skips when `!nvidia.Available()`, which is what CI does. But when `nvidia.Available()` returns `true` (a real GPU host), the code falls through to the same `halHwmon.State` type assertion even though `ch.Opaque` is a `halNVML.State`, causing a panic. The fix is to add `if !bc.fileBacked { return }` before the assertion, mirroring the pattern used correctly in `write_idempotent_open`.

**Evidence:**
```
internal/hal/contract_test.go:161 — test "read_no_mutation"

161: t.Run("read_no_mutation", func(t *testing.T) {
162:   for _, bc := range cases {
163:     bc := bc
164:     t.Run(bc.name, func(t *testing.T) {
165:       if !bc.fileBacked {
166:         if !nvidia.Available() {
167:           t.Skipf("backend %s: NVML not available...", bc.name)
168:         }
169:       }
170:       ch := bc.mkCh(t)
171:       st := ch.Opaque.(halHwmon.State)  // ← PANICS when bc is nvml + NVML available
```

Compare to the safe pattern in `write_idempotent_open`:
```
internal/hal/contract_test.go:330:
  if !bc.fileBacked {
      return  // ← guards the type assertion correctly
  }
```

**Recommended follow-up:** File GitHub issue `fix(hal/contract_test): guard read_no_mutation type assertion with fileBacked check`. One-line fix: replace the skip-if-no-NVML block with `if !bc.fileBacked { return }`.

---

### ULTRA-04 finding 1 (severity: warning)

**`internal/hwmon/watcher.go`: five exported `With*` builder functions are dead (never called).**

The watcher builder API (`WithEnumerator`, `WithUeventSubscriber`, `WithRescanPeriod`, `WithDebounce`, `WithRebindMinInterval`) is exported but `deadcode` reports all five as unreachable. The watcher itself is used, but always constructed with its default configuration — the functional options are never invoked. These are either scaffolding for a future extensibility story that never arrived, or options the caller no longer needs.

**Evidence:**
```
internal/hwmon/watcher.go:168:6: unreachable func: WithEnumerator
internal/hwmon/watcher.go:174:6: unreachable func: WithUeventSubscriber
internal/hwmon/watcher.go:180:6: unreachable func: WithRescanPeriod
internal/hwmon/watcher.go:186:6: unreachable func: WithDebounce
internal/hwmon/watcher.go:207:6: unreachable func: WithRebindMinInterval
```

**Recommended follow-up:** File issue `dead(hwmon): prune unreachable With* builder functions`. If none of these are needed by an active roadmap item, unexport or delete them.

---

### ULTRA-04 finding 2 (severity: warning)

**`internal/hwmon/hwmon.go:ReadTemp`, `WritePWMSafe`, `ReadFanMinRPM` are dead exported functions.**

Three exported functions in the core hwmon package are never called from production code. `ReadFanMinRPM` is particularly suspicious: `ReadFanMaxRPM` at line 203 is actively used by the calibrate and controller packages, but `ReadFanMinRPM` at line 222 is a near-identical clone that `deadcode` identifies as unreachable.

**Evidence:**
```
internal/hwmon/hwmon.go:19:6:  unreachable func: ReadTemp
internal/hwmon/hwmon.go:90:6:  unreachable func: WritePWMSafe
internal/hwmon/hwmon.go:222:6: unreachable func: ReadFanMinRPM
```

Additionally:
```
internal/hwmon/autoload.go:731:6:    unreachable func: FindPWMPaths
internal/hwmon/modulesalias.go:52:6: unreachable func: parseModulesBuiltinModinfo
```

**Recommended follow-up:** File issue `dead(hwmon): remove/unexport ReadTemp, WritePWMSafe, ReadFanMinRPM, FindPWMPaths, parseModulesBuiltinModinfo`. Verify none are referenced in a pending branch before deleting.

---

### ULTRA-04 finding 3 (severity: advisory)

**`internal/hal/registry.go:Reset()` is dead.**

The `hal.Reset()` function is exported and appears in the registry, but `deadcode` reports it unreachable from the production entry point. It is only referenced in `hal/registry_test.go`. This is a test seam that has leaked into the public API.

**Evidence:**
```
internal/hal/registry.go:49:6: unreachable func: Reset
```

**Recommended follow-up:** Advisory — unexport to `reset()` and adjust the test. No immediate action required.

---

### ULTRA-04 finding 4 (severity: advisory)

**`testutil/calls.go` and all `internal/testfixture/fake*` packages show as "unreachable" by deadcode.**

This is expected: `deadcode` analyzes from `cmd/ventd` entry point and correctly identifies that test-only code is never called from production. All `testfixture` and `testutil` packages are used exclusively by `*_test.go` files. No action needed, but noted for completeness.

**Evidence:**
```
testutil/calls.go:18:6: unreachable func: NewCallRecorder
internal/testfixture/fakecfg/fakecfg.go:19:6: unreachable func: New
... (12 fake* packages, all test-only)
```

**Recommended follow-up:** None — test infrastructure, not dead production code.

---

### ULTRA-05 finding 1 (severity: warning)

**12-clone duplication across all `internal/testfixture/fake*` packages — identical boilerplate structure.**

Every `fake*` package (fakecfg, fakecrosec, fakedbus, fakedmi, fakeipmi, fakeliquid, fakemic, fakenvml, fakepwmsys, fakesmc, fakeuevent, fakewmi) has an identical file structure: `New(t *testing.T) *Fake`, `t.Helper()`, `t.Cleanup(...)`, a `rec *testutil.CallRecorder` field, and method stubs. `dupl` reports the top-level clone block spanning all 12 files.

**Evidence:**
```
found 12 clones:
  internal/testfixture/fakecfg/fakecfg.go:2,34
  internal/testfixture/fakecrosec/fakecrosec.go:2,34
  internal/testfixture/fakedbus/fakedbus.go:2,34
  internal/testfixture/fakedmi/fakedmi.go:2,34
  internal/testfixture/fakeipmi/fakeipmi.go:2,34
  internal/testfixture/fakeliquid/fakeliquid.go:2,34
  internal/testfixture/fakemic/fakemic.go:2,34
  internal/testfixture/fakenvml/fakenvml.go:2,34
  internal/testfixture/fakepwmsys/fakepwmsys.go:2,34
  internal/testfixture/fakesmc/fakesmc.go:2,34
  internal/testfixture/fakeuevent/fakeuevent.go:2,34
  internal/testfixture/fakewmi/fakewmi.go:2,34
```

**Recommended follow-up:** File issue `refactor(testfixture): code-gen fake* scaffolding`. A `go generate` script could stamp out the boilerplate. Low urgency but worth doing before adding more fakes.

---

### ULTRA-05 finding 2 (severity: advisory)

**`internal/setup/setup.go` has two clusters of repeated hwdiag Entry construction blocks.**

Lines 1496–1537 (three consecutive `case` blocks in a switch that builds `hwdiag.Entry` structs) are nearly identical in shape. Lines 1336 and 1691 are also a matched pair. This is purely cosmetic within production code.

**Evidence:**
```
found 3 clones:
  internal/setup/setup.go:1496,1509
  internal/setup/setup.go:1510,1523
  internal/setup/setup.go:1524,1537
found 2 clones:
  internal/setup/setup.go:1336,1349
  internal/setup/setup.go:1691,1704
```

**Recommended follow-up:** Advisory — extract a `makeHwdiagEntry(reason, nd)` helper if this switch grows further. Not urgent.

---

### ULTRA-05 finding 3 (severity: advisory)

**`internal/calibrate/calibrate.go:346` duplicated in two test helper files.**

A small block (lines 346–352) is cloned in `hwmon/diagnose_test.go:30` and `hwmon/recover_test.go:30`. Cross-package test helper duplication; no production impact.

**Evidence:**
```
found 3 clones:
  internal/calibrate/calibrate.go:346,352
  internal/hwmon/diagnose_test.go:30,36
  internal/hwmon/recover_test.go:30,36
```

**Recommended follow-up:** Advisory — consolidate into a shared test helper if both test files grow.

---

### ULTRA-06 finding 1 (severity: warning)

**`internal/hal` package shows 0.0% coverage.**

The `internal/hal` package contains `registry.go` (the global backend registry: `Register`, `Backend`, `Reset`, `Enumerate`, `Resolve`) and `backend.go` (interface + types). The coverage profile shows 0.0% because the contract tests live in `internal/hal/hwmon` and `internal/hal/nvml` packages, not in `hal` itself. The registry functions `Register`, `Backend`, `Enumerate`, `Resolve` are called in production (cmd/ventd) but have no unit tests of their own.

**Evidence:**
```
ok  github.com/ventd/ventd/internal/hal  0.010s  coverage: 0.0% of statements

internal/hal/registry.go:30:6:  unreachable func: Register   (deadcode)
internal/hal/registry.go:40:6:  unreachable func: Backend    (deadcode — from test entry)
internal/hal/registry.go:49:6:  unreachable func: Reset      (deadcode)
internal/hal/registry.go:61:6:  unreachable func: Enumerate  (deadcode — from test entry)
internal/hal/registry.go:92:6:  unreachable func: Resolve    (deadcode — from test entry)
```

Note: `deadcode` flags these as unreachable from the cmd/ventd binary, but they ARE used in production; deadcode's analysis doesn't reach through the registry pattern. The 0.0% coverage is real — the registry itself has no dedicated test.

**Recommended follow-up:** File issue `test(hal): add registry unit tests`. A simple `TestRegistry_RegisterAndEnumerate` covering register+enumerate+resolve would bring coverage above 0% and verify the multi-backend composition used in main.

---

### ULTRA-06 finding 2 (severity: warning)

**`internal/nvidia` at 30.2% coverage — hardware-gated code.**

The nvidia package is almost entirely skip-gated behind `nvidia.Available()`. In CI (no GPU), ~70% of the code is never exercised. This is expected given the hardware constraint, but notable because the NVML Restore path (the critical fan-handback path) is in that uncovered 70%.

**Evidence:**
```
ok  github.com/ventd/ventd/internal/nvidia  0.032s  coverage: 30.2% of statements
```

**Recommended follow-up:** Advisory for now. When a hardware-in-the-loop (HIL) runner is available, add it to CI for GPU coverage. Track in an existing issue.

---

### ULTRA-06 finding 3 (severity: warning)

**`internal/hwmon` at 43.8% — below the 50% blocker threshold for this package.**

The hwmon package is a safety-critical layer (all sysfs reads/writes go through it). 43.8% is below the 50% threshold declared in the ultrareview spec for packages that include calibrate/controller/hal/watchdog. Several functions are dead (ReadTemp, WritePWMSafe, ReadFanMinRPM per ULTRA-04) which inflates the uncovered count; removing them would improve the ratio.

**Evidence:**
```
ok  github.com/ventd/ventd/internal/hwmon  0.027s  coverage: 43.8% of statements
```

Key uncovered areas include `watcher.go` With* functions (dead, see ULTRA-04), `WritePWMSafe`, `ReadTemp`, and parts of `autoload.go`.

**Recommended follow-up:** File issue `test(hwmon): coverage below 50% threshold`. Priority: remove dead code first (ULTRA-04), then add tests for the remaining gaps.

---

### ULTRA-06 finding 4 (severity: advisory)

**`internal/config/config.go` has several 0% functions: `UseSecureCookies`, `UnmarshalJSON`, `SavePasswordHash`, `detectCycle`.**

`detectCycle` at 0% is interesting — it validates that fan→curve→sensor dependency graphs are acyclic, but the test suite never exercises a cycle. If a cycle were introduced in a real config, this code path has never been validated.

**Evidence:**
```
internal/config/config.go:141:  UseSecureCookies    0.0%
internal/config/config.go:369:  UnmarshalJSON       0.0%
internal/config/config.go:627:  SavePasswordHash    0.0%
internal/config/config.go:729:  detectCycle         0.0%
```

**Recommended follow-up:** Advisory — add a test for `detectCycle` with a crafted cyclic config. `SavePasswordHash` and `UnmarshalJSON` are also worth a quick test to confirm they don't silently fail.

---

### ULTRA-07 finding 1 (severity: warning)

**`internal/hwmon`: several exported functions have zero external references outside the package.**

`Diagnose()`, `DiagnoseHwmon()`, `DiagnoseHwmonAt()`, `WritePWMEnablePath()`, `RPMTargetEnablePath()`, `ErrPWMModeUnsafe` are exported but never referenced outside `internal/hwmon`. These may be stale from an earlier diagnostic API that was superseded by `internal/hwdiag`.

**Evidence:**
```
deadcode:
internal/hwmon/hwmon.go:19:6:   unreachable func: ReadTemp           (0 external refs)
internal/hwmon/hwmon.go:90:6:   unreachable func: WritePWMSafe       (0 external refs)
internal/hwmon/hwmon.go:222:6:  unreachable func: ReadFanMinRPM      (0 external refs)
```

`Diagnose` group: not returned by deadcode but grepped to 0 external refs outside the package.

**Recommended follow-up:** Consolidate with ULTRA-04 finding 2. One clean-up PR covers both.

---

### ULTRA-07 finding 2 (severity: warning)

**`internal/config.Default()` and `calibrate.SchemaVersion` are exported constants/functions with 0 external references.**

`Default()` is exported in `config.go` but never called from outside the config package. `SchemaVersion` in calibrate is a public constant with no external consumers. Both are dead API surface.

**Evidence:**
```
go tool analysis: config.Default() — 0 references outside internal/config
go tool analysis: calibrate.SchemaVersion — 0 references outside internal/calibrate
```

**Recommended follow-up:** Advisory — unexport both or delete if not part of a planned external API.

---

### ULTRA-07 finding 3 (severity: advisory)

**`internal/controller`: `WithSensorReadHook` and `WithPanicChecker` option funcs have 0 external references.**

These functional options are exported for test injection (they're constructor options for `controller.New`) but deadcode identifies them as unreachable from production. They are test seams — appropriate, but should probably be unexported or moved to a `_test.go` file if only test code uses them.

**Evidence:**
```
internal/controller/controller.go:112:6: unreachable func: WithSensorReadHook
internal/controller/controller.go:121:6: unreachable func: WithPanicChecker
```

**Recommended follow-up:** Advisory — if these are only used in controller tests, unexport them using the `export_test.go` pattern.

---

### ULTRA-08 finding 1 (severity: advisory)

**Binary size: 12.5 MB (13,141,271 bytes). Baseline established.**

No previous ultrareview to compare. The binary is statically linked (`CGO_ENABLED=0`) and includes embedded UI assets. 12.5 MB is reasonable for this scope. Future ultrareviews should flag if size exceeds ~16 MB without a corresponding major feature addition.

**Evidence:**
```
-rwxr-xr-x 1 cc-runner cc-runner 13141271 Apr 18 14:08 /tmp/ventd
```

**Recommended follow-up:** None at this time. Record as baseline.

---

### ULTRA-09 finding 1 (severity: warning)

**CHANGELOG `[Unreleased]` section has 324 lines — well above the 150-line flag threshold.**

Lines 7–330 cover everything from initial fixture infrastructure (T0-INFRA-01 through T0-INFRA-03), the FanBackend interface (P1-HAL-01), calibration refactor (P1-HAL-02), hwdb fingerprinting (P1-FP-01, P1-FP-02), controller perf (P1-HOT-01, P1-HOT-02), safety tests, web security headers, packaging, hwmon resilience, and more. The last release was v0.2.0 on 2026-04-16 (2 days ago). No duplicate PR references found.

**Evidence:**
```
CHANGELOG.md line 7:  ## [Unreleased]
CHANGELOG.md line 331: ## [v0.2.0] — 2026-04-16
→ 324 lines of unreleased content
```

**Recommended follow-up:** Consider cutting v0.3.0 once the current hot-fix sprint (P1-HOT series) stabilizes. The volume of unreleased changes makes it hard to reason about what's in production.

---

### ULTRA-10 finding 2 (severity: advisory)

**`go mod tidy` downloads packages during audit — govulncheck tooling dependency.**

Running `go mod tidy -v` printed download activity but left `go.mod` and `go.sum` unchanged (confirmed via `git diff`). The downloads were artifact of govulncheck's own toolchain requirement (needing go1.25.9). No actual module drift.

**Evidence:**
```
go mod tidy -v 2>&1 → downloaded govulncheck transitive deps (rod, testify, etc.)
git diff go.mod → empty
git diff go.sum → empty
```

**Recommended follow-up:** None — clean.

---

### ULTRA-11 finding 1 (severity: advisory)

**Several advanced config fields are absent from `config.example.yaml`: HWDB block, Profiles, TLS settings, Hysteresis, Smoothing, Points curve, `allow_stop`.**

All fields have yaml tags and pass validation. The example omits advanced/optional fields intentionally, but `allow_stop`, `hysteresis`, and `smoothing` are features that users will want to know about when they hit the limits of the basic config.

**Evidence:**
```
Config.HWDB.AllowRemote — in code, not in example
Config.Profiles / ActiveProfile — in code, not in example
Fan.AllowStop — in code, not in example
CurveConfig.Hysteresis — in code, not in example
CurveConfig.Smoothing — in code, not in example
CurveConfig.Points — in code, not in example
```

**Recommended follow-up:** Advisory — add a commented-out advanced section to `config.example.yaml` or extend `docs/config.md` with examples for each feature. The existing `docs/config.md` is comprehensive but misses Hysteresis/Smoothing/Points.

---

### ULTRA-11 finding 2 (severity: advisory)

**`internal/config/config.go:writeFileSync` at 39.1% coverage — atomic write path under-tested.**

`writeFileSync` is the function that writes config to disk atomically (write-then-rename). It's called by `Save` and `SavePasswordHash`. Its error paths (rename failure, fsync failure) are not tested. A bug here could silently corrupt the config file.

**Evidence:**
```
internal/config/config.go:642: writeFileSync  39.1%
```

**Recommended follow-up:** Advisory — add a test that injects an fsync/rename error to confirm the function returns the error and doesn't leave a partial file.

---

### ULTRA-12 finding 1 (severity: warning)

**`docs/api.md` does not exist. 54 HTTP endpoints have no external specification.**

The web server registers 54 distinct route paths (27 base paths, each duplicated under `/api/v1/` plus standalone routes). None have external documentation. As the API surface grows, this gap makes integration harder and creates risk that route removals or signature changes go unnoticed.

**Evidence:**
```
grep -rn "HandleFunc\|Handle(" internal/web/ → 54 distinct routes
docs/api.md → does not exist

Sample routes without docs:
  GET  /api/status
  GET  /api/events
  PUT  /api/config
  POST /api/config/dryrun
  GET  /api/hardware
  POST /api/hardware/rescan
  POST /api/panic
  GET  /api/panic/state
  POST /api/panic/cancel
  GET  /api/profile
  POST /api/profile/active
  GET  /api/history
  PUT  /api/profile/schedule
  GET  /api/schedule/status
  POST /api/calibrate/start
  GET  /api/calibrate/status
  GET  /api/calibrate/results
  POST /api/calibrate/abort
  POST /api/detect-rpm
  GET  /api/setup/status
  POST /api/setup/start
  POST /api/setup/apply
  POST /api/setup/reset
  POST /api/setup/calibrate/abort
  POST /api/setup/load-module
  POST /api/system/reboot
  POST /api/set-password
  GET  /api/hwdiag
  POST /api/hwdiag/install-kernel-headers
  POST /api/hwdiag/install-dkms
  POST /api/hwdiag/mok-enroll
  GET  /api/system/watchdog
  GET  /api/system/recovery
  GET  /api/system/security
  GET  /api/system/diagnostics
  (plus /api/v1/* mirrors and unauthenticated routes)
```

**Recommended follow-up:** File issue `docs: create docs/api.md — route inventory with request/response shapes`. Even a minimal inventory (method, path, auth required, brief description) is useful. Not urgent, but should be done before any external integration story.

---

## Raw data

### Full coverage by package

```
ok  github.com/ventd/ventd/internal/controller     0.559s  coverage: 88.8%
ok  github.com/ventd/ventd/internal/curve          0.003s  coverage: 98.5%
ok  github.com/ventd/ventd/internal/hal            0.010s  coverage: 0.0%
ok  github.com/ventd/ventd/internal/hwdb           0.007s  coverage: 82.8%
ok  github.com/ventd/ventd/internal/hwdiag         0.003s  coverage: 87.2%
ok  github.com/ventd/ventd/internal/hwmon          0.027s  coverage: 43.8%
ok  github.com/ventd/ventd/internal/monitor        0.015s  coverage: 76.4%
ok  github.com/ventd/ventd/internal/nvidia         0.032s  coverage: 30.2%
ok  github.com/ventd/ventd/internal/packaging      0.002s  coverage: [no statements]
ok  github.com/ventd/ventd/internal/sdnotify       0.457s  coverage: 95.3%
ok  github.com/ventd/ventd/internal/setup          0.633s  coverage: 72.3%
ok  github.com/ventd/ventd/internal/watchdog       0.009s  coverage: 94.9%
ok  github.com/ventd/ventd/internal/web            4.296s  coverage: 68.7%
ok  github.com/ventd/ventd/internal/calibrate      ~       coverage: ~87.4% (func avg)
ok  github.com/ventd/ventd/internal/config         ~       coverage: ~70% (func avg)
ok  github.com/ventd/ventd/tools/coworkstatus      0.002s  coverage: 67.3%
ok  github.com/ventd/ventd/tools/regresslint       0.002s  coverage: 59.3%
ok  github.com/ventd/ventd/tools/rulelint          0.002s  coverage: 84.0%
```

### govulncheck full output (summary)

```
Your code is affected by 17 vulnerabilities from the Go standard library.
This scan also found 3 vulnerabilities in packages you import and 6
vulnerabilities in modules you require, but your code doesn't appear to call
these vulnerabilities.

All 17 exploitable vulns fixed by: go 1.25.9
Packages affected: crypto/tls, crypto/x509, encoding/pem, encoding/asn1,
                   net/http, net/url, os
```

### deadcode full output (production code only, excluding testfixture)

```
internal/config/config.go:31:6:       SetHwmonRootFS
internal/config/migrate.go:37:6:      SetTLSMigrationFS
internal/config/resolve_hwmon.go:49:6: SetHwmonDevicePathResolver
internal/hal/registry.go:49:6:        Reset
internal/hwmon/autoload.go:731:6:     FindPWMPaths
internal/hwmon/hwmon.go:19:6:         ReadTemp
internal/hwmon/hwmon.go:90:6:         WritePWMSafe
internal/hwmon/hwmon.go:222:6:        ReadFanMinRPM
internal/hwmon/modulesalias.go:52:6:  parseModulesBuiltinModinfo
internal/hwmon/watcher.go:168:6:      WithEnumerator
internal/hwmon/watcher.go:174:6:      WithUeventSubscriber
internal/hwmon/watcher.go:180:6:      WithRescanPeriod
internal/hwmon/watcher.go:186:6:      WithDebounce
internal/hwmon/watcher.go:207:6:      WithRebindMinInterval
internal/setup/modprobe.go:158:6:     SetModprobeCmd
internal/setup/modprobe.go:170:6:     SetModulesLoadWrite
internal/setup/modprobe.go:182:6:     SetModulesLoadDir
testutil/calls.go:18:6:               NewCallRecorder (test-only)
```

### dupl notable production clones

```
found 2 clones (internal hwmon.go):
  internal/hwmon/hwmon.go:203,216  (ReadFanMaxRPM)
  internal/hwmon/hwmon.go:222,235  (ReadFanMinRPM — also dead code)

found 3 clones (setup.go hwdiag blocks):
  internal/setup/setup.go:1496,1509
  internal/setup/setup.go:1510,1523
  internal/setup/setup.go:1524,1537

found 2 clones (setup.go repeated blocks):
  internal/setup/setup.go:1336,1349
  internal/setup/setup.go:1691,1704
```

### dependency tree

```
go list -m all | wc -l → 24 modules total (4 direct, 5 indirect)

Direct deps:
  github.com/ebitengine/purego v0.10.0
  github.com/go-rod/rod v0.116.2
  golang.org/x/crypto v0.50.0
  gopkg.in/yaml.v3 v3.0.1
```

### binary size

```
-rwxr-xr-x 1 cc-runner cc-runner 13141271 Apr 18 14:08 /tmp/ventd
= 12.54 MB (CGO_ENABLED=0)
```

---

## Recommended next actions

1. **[blocker] Bump Go toolchain** — change `go 1.25.0` to `go 1.25.9` in `go.mod`, run `go mod tidy`, rebuild. Fixes all 17 stdlib CVEs. File as `chore: bump Go toolchain to go1.25.9`.

2. **[warning] Fix `contract_test.go:read_no_mutation` type assertion** — add `if !bc.fileBacked { return }` guard before line 171. File as `fix(hal/contract_test): guard read_no_mutation with fileBacked check`.

3. **[warning] Add registry unit tests** — `internal/hal` at 0.0% coverage. File as `test(hal): add registry unit tests`.

4. **[warning] Prune dead hwmon exports** — `ReadTemp`, `WritePWMSafe`, `ReadFanMinRPM`, `FindPWMPaths`, `parseModulesBuiltinModinfo`, all 5 `With*` watcher builder functions. File as `dead(hwmon): remove unreachable exported functions`.

5. **[warning] hwmon coverage below 50%** — after dead code removal, close the remaining gap. File as `test(hwmon): coverage below 50%`.

6. **[warning] Create `docs/api.md`** — 54 endpoints with no external spec. File as `docs: create API endpoint inventory`.

7. **[warning] CHANGELOG [Unreleased] at 324 lines** — consider cutting v0.3.0 after P1-HOT sprint stabilizes.

8. **[warning] Code-gen testfixture scaffolding** — 12 identical fake* packages. File as `refactor(testfixture): code-gen fake* boilerplate`.

9. **[advisory] Unexport `hal.Reset()`, `config.Default()`, `calibrate.SchemaVersion`** — dead public API.

10. **[advisory] Add `detectCycle` test** — config cycle detection at 0% coverage; a cycle in a real config would silently miss the validator.

11. **[advisory] `writeFileSync` error paths untested** — atomic config write could silently corrupt. Add error injection test.

12. **[advisory] Extend `config.example.yaml`** — add commented-out sections for Hysteresis, Smoothing, Points curves, allow_stop.

---

## Summary

The tree is architecturally sound and operationally safe. The HAL contract is fully implemented across both backends, all goroutine exit paths are correct, watchdog restore coverage is complete, and the rule/test binding system is working. The single blocker is a Go toolchain version that hasn't tracked stdlib security patches — 17 CVEs fixed by a one-line `go.mod` bump to 1.25.9. The most structurally significant warning is `internal/hal` at 0.0% coverage (the registry layer, which is the multi-backend composition point, has no dedicated tests). Secondary concerns are dead code accumulation in `internal/hwmon` (5 watcher builders + 3 hwmon functions never called), `internal/hwmon` coverage below 50%, and the absence of `docs/api.md` for a now-substantial 54-endpoint API surface. The codebase is ready for continued development once the Go toolchain blocker is addressed.
