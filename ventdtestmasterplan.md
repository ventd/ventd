# VENTD TEST MASTERPLAN

**Document purpose:** The authoritative test-suite design for ventd.
Companion to `ventdmasterplan.md` — every code task there has a matching
test task here.

**Operating model:** Solo developer + Claude. Tests land in the same PR as
the feature they cover (the regression-replay pattern), unless the test task
is pure infrastructure (fixtures, CI, fuzz corpora) that can precede any
feature.

**Last substantive revision:** 2026-04-22 (post-Cowork cleanup).

---

## 0 · Glossary

| Term | Meaning |
|---|---|
| **Invariant** | A property that must always hold. Bound 1:1 to a named subtest so a regression fails at a predictable location. |
| **Rule file** | A `.claude/rules/*.md` markdown file enumerating invariants. Each rule ID maps to one subtest. |
| **Fixture** | A reusable hardware or environment mock. Lives in `internal/testfixture/<backend>/`. |
| **Replay test** | A regression test named after a GitHub issue number (`TestRegression_Issue<N>_*`) that encodes the bug's conditions. |
| **Golden** | A committed reference output (YAML, JSON, diff) that tests compare against. Update via explicit `-update` flag. |
| **HIL** | Hardware-in-loop. Runs on real hardware via `validation/` scripts. Not gating CI; gating release. |
| **Tier** | One of five test categories from unit (Tier 2) through release validation (Tier 5). See §1. |

---

## 1 · Test architecture — the five tiers

```
Tier 5 — Release validation (HIL, cross-OS smoke)       validation/
         Manual, pre-release only.
Tier 4 — End-to-end (browser, full daemon)              internal/web/e2e_*
         go-rod headless Chromium. Gates PRs via e2e.yml.
Tier 3 — Integration (multi-package, fixtures)          internal/*/integration_*
         Fake sysfs, fake IPMI, fake HID. Gates PRs.
Tier 2 — Unit + property + fuzz                         internal/*/<pkg>_test.go
         Fast. Runs on every test invocation. Gates PRs.
Tier 1 — Static analysis                                vet, lint, staticcheck, vulncheck
         Runs before tests.
```

Rules:

- A feature cannot regress a tier it previously passed. If Tier 4 was green
  and now fails, the PR is rejected regardless of Tier 2 status.
- New tiers never appear. If a test doesn't fit a tier, stop and think.
- Each tier has a dedicated CI job; failure in any tier fails the PR.

---

## 2 · Invariant binding — the `.claude/rules/*.md` pattern

Ventd has one working invariant file (`.claude/rules/hwmon-safety.md`)
bound 1:1 to `internal/controller/safety_test.go:TestSafety_Invariants`.
This pattern extends to every safety-critical surface.

Planned rule files (extend as features land):

| File | Owned by suite | Status |
|---|---|---|
| `hwmon-safety.md` | `internal/controller/safety_test.go:TestSafety_Invariants` | LANDED |
| `calibration-safety.md` | `internal/calibrate/safety_test.go:TestCalSafety_Invariants` | planned |
| `watchdog-safety.md` | `internal/watchdog/safety_test.go:TestWDSafety_Invariants` | planned — **highest priority** (watchdog at 23% coverage) |
| `hal-contract.md` | `internal/hal/contract_test.go:TestHAL_Contract` | planned — blocks every new backend |
| `ipmi-safety.md` | `internal/hal/ipmi/safety_test.go` | spec-01 ships this |
| `liquid-safety.md` | `internal/hal/liquid/safety_test.go` | spec-02 ships this |
| `pi-stability.md` | `internal/curve/pi_safety_test.go` | spec-04 ships this |
| `mpc-stability.md` | `internal/curve/mpc_safety_test.go` | post-MPC spec |
| `acoustic-safety.md` | `internal/acoustic/safety_test.go` | Phase 7 |
| `hwdb-schema.md` | `internal/hwdb/schema_test.go:TestSchema_Invariants` | spec-03 ships this |
| `config-schema.md` | `internal/config/schema_test.go:TestSchema_Invariants` | planned |
| `web-security.md` | `internal/web/security_invariants_test.go` | planned |

**Rule file format:**

```
# <Scope> Invariants

Each rule below is bound 1:1 to a subtest in <file>:<function>.
Editing a rule requires editing the corresponding subtest in the
same PR. Adding a rule requires landing the subtest in the same PR.

## RULE-<ID>: <one-line invariant>
<paragraph explaining the invariant, why it holds, what fails if it doesn't>
Bound: <file>:<subtest_name>
```

**Enforcement:** `tools/rulelint` parses every rule file and confirms every
`Bound:` line points to a real subtest. Orphan rules or orphan subtests fail
the `meta-lint.yml` CI job.

---

## 3 · Fixture library

Every backend gets a reusable fixture under `internal/testfixture/<backend>/`.
Tests import them; they are not duplicated per-test.

| Fixture | Package | Fakes | Consumers |
|---|---|---|---|
| `fakehwmon` | `internal/testfixture/fakehwmon` | `/sys/class/hwmon/hwmonN/` trees | controller, calibrate, hwmon, monitor, setup, watchdog |
| `fakeipmi` | `internal/testfixture/fakeipmi` | `/dev/ipmi0` named-pipe + ioctl table | hal/ipmi, watchdog |
| `fakeliquid` | `internal/testfixture/fakeliquid` | USB HID mock — Corsair/NZXT/Lian Li protocols | hal/liquid |
| `fakehid` | `internal/testfixture/fakehid` | Generic USB HID base | hal/usbbase, hal/liquid |
| `fakecrosec` | `internal/testfixture/fakecrosec` | `/dev/cros_ec` stub + EC_CMD_* | hal/crosec |
| `fakepwmsys` | `internal/testfixture/fakepwmsys` | `/sys/class/pwm/` tree | hal/pwmsys |
| `fakenvml` | `internal/testfixture/fakenvml` | purego-loadable NVML shim | nvidia, monitor |
| `fakemic` | `internal/testfixture/fakemic` | Synthetic fan acoustics + bearing defects | acoustic |
| `fakedmi` | `internal/testfixture/fakedmi` | `/sys/class/dmi/id/*` trees for 20+ boards | hwdb, setup, autoload |
| `fakedbus` | `internal/testfixture/fakedbus` | Abstract-socket D-Bus with canned responses | modprobe, sleep-listener, hal/ipmi |
| `fakeuevent` | `internal/testfixture/fakeuevent` | AF_NETLINK pair writing canned messages | hwmon/watcher |
| `faketime` | `internal/testfixture/faketime` | Monotonic clock override | calibrate, controller, watchdog, acoustic |
| `fakecfg` | `internal/testfixture/fakecfg` | Config builder — known-good + known-bad | config, setup, web |

**Fixture rules:**

- Every fixture has `New*(t *testing.T, opts *Options)` with `t.Cleanup()`.
- Deterministic: no wallclock, no unseeded `rand`, no real network.
- Race-clean under `go test -race`.
- No real hardware. Unreachable paths documented in `COVERAGE.md` and
  covered in Tier 5 HIL only.

**Cross-platform fixtures removed from this plan:** `fakewmi`, `fakesmc`,
`fakeillumos`, etc. — those belong to the Windows/macOS subprojects when
they spin up. See main masterplan §16.

---

## 4 · Fuzzing & property-based tests

Fuzz targets, each via Go native fuzzing (`f.Fuzz`):

| Target | Package | Corpus seed | Invariant |
|---|---|---|---|
| `FuzzConfigParse` | internal/config | `testdata/fuzz/config_*` | Parse never panics; round-trip stable |
| `FuzzYAMLMigrate` | internal/config | Every tagged config.example.yaml | Migrate always produces valid current-schema |
| `FuzzIPMIResponse` | internal/hal/ipmi | Recorded BMC responses + bit-flips | Parse never panics; unknown fields logged not crashed |
| `FuzzHIDReport` | internal/hal/liquid | Recorded device reports + noise | Parse never panics; out-of-range clamps |
| `FuzzExprCurve` | internal/curve | Malicious expressions | Bounded eval; no `os`/`io`/`net` reachable |
| `FuzzDMIFingerprint` | internal/hwdb | UTF-8 + control-char DMI trees | Match never panics; anonymiser strips PII |
| `FuzzMpcModel` | internal/curve | Random ARX + synthetic thermals | Solver finite, feasible PWM, ≤10 ms |
| `FuzzModulesAlias` | internal/hwmon | Real + malformed modules.alias | Parser reports expected modules without panic |
| `FuzzChipName` | internal/config | Unicode chip-name strings | EnrichChipName idempotent, no crash |
| `FuzzUeventFrame` | internal/hwmon | Real AF_NETLINK captures + variants | No panic; watcher stays responsive |
| `FuzzSetupToken` | internal/web | Random byte strings | Constant-time compare; no timing oracle |
| `FuzzAnonymise` | internal/hwdb | 100-sample PII patterns | Zero leakage across corpus |

**Cadence:** 10s per target on PR (`fuzz-smoke.yml`). 10m per target nightly
(`fuzz-long.yml`). Crashers committed as corpus entries.

**Property tests** (via `testing/quick` or `gopter`):

| Property | Package | Claim |
|---|---|---|
| `PropCurveMonotonicNonDecreasing` | internal/curve | `tempA ≤ tempB ⇒ evaluate(tempA) ≤ evaluate(tempB)` |
| `PropCurveClamped` | internal/curve | Output ∈ `[0, 100]` for every input |
| `PropSmoothingConvergence` | internal/controller | EMA converges to constant input within `5·τ` |
| `PropPIStability` | internal/curve | For `(Kp, Ki)` in bounds + step disturbance, integral bounded |
| `PropHysteresisNoFlutter` | internal/controller | Within deadband, output identical across sensor oscillation |
| `PropMPCFeasibility` | internal/curve | Solver returns value within `[MinPWM, MaxPWM]` on degenerate models |
| `PropDitherMean` | internal/controller | Sum of dither offsets ≈ 0 over time |
| `PropConfigRoundtrip` | internal/config | `Parse(Serialise(c)) == c` for every valid c |
| `PropHWDBDeterministic` | internal/hwdb | `Match(fp)` is pure: same input → same output |

---

## 5 · Chaos / fault injection

Tier-3 level. Runs as `chaos.yml` CI job. Uses `faultfs.FS` wrapper on the
real or fake filesystem.

| Injection | Where | Tests |
|---|---|---|
| EIO on first N writes, then success | fakehwmon | Controller retry; watchdog fallback |
| ETIMEDOUT on read | fakehwmon, fakeipmi | Controller skip-tick; calibration settle-timeout |
| Partial write (short write) | fakehwmon | Controller detects torn write and retries |
| ENOSPC on checkpoint | fakefile | Calibrate surfaces error without corrupting prior checkpoint |
| Slow I/O (100ms latency) | faketime + fakehwmon | Hot loop doesn't stall past poll interval |
| USB disconnect mid-frame | fakeliquid | Pump stays above `pump_minimum`; re-arms on reconnect |
| BMC busy (0xC3) | fakeipmi | Backend retries with backoff, caps at 3 |
| ENOBUFS on AF_NETLINK | fakeuevent | Watcher falls back to poll |
| modprobe exits non-zero | fakemodprobe | Autoload surfaces error to diag; no crash |
| Config file truncated mid-YAML | fakefile | Clear error; fallback to last-good if applicable |
| Setup token file missing | fakefile | Daemon regenerates; clear log |
| NVML dlopen returns nil | fakenvml | GPU features disabled silently; no panic |
| OOM during MPC solve | `testing.AllocsPerRun` | Solver falls back to PI |
| Clock skew during calibration | faketime | Calibration step timeout robust to wallclock jumps |
| `/sys` re-enumeration mid-tick | fakehwmon | Controller re-resolves via ChipName; no fan stalled |
| **System sleep/resume mid-tick** (NEW) | fakedbus + faketime | Controller yields; device reinit on resume per P4-SLEEP-01 |
| **Third-party PWM write** (NEW) | fakehwmon | Controller detects external change within 2 ticks per P4-INTERFERENCE-01 |

---

## 6 · Concurrency testing

Every test package imports `go.uber.org/goleak`. `TestMain` wrappers assert
zero goroutine leaks.

Race-specific suites:

| Suite | Verifies |
|---|---|
| `TestRace_ConfigHotReload` | SIGHUP mid-tick; atomic.Pointer swap ordering |
| `TestRace_CalibrateVsController` | Calibration yields controller, resumes without double-write |
| `TestRace_PanicVsTick` | Panic gate engages within 1 tick |
| `TestRace_WatchdogRestore` | Restore concurrent with tick; last write deterministic |
| `TestRace_WebSSEBroadcast` | 100 SSE subscribers; buildStatus called once per cadence |
| `TestRace_HotplugDuringCalibrate` | uevent mid-calibration; fingerprint fence prevents contamination |
| `TestRace_FleetGossip` | 10 nodes gossip concurrently; membership converges |
| `TestRace_SleepResume` (NEW) | Sleep signal mid-tick; clean shutdown + reinit |

All run under `-race`. Alpine's `-race` skip lifted for pure-Go packages.

**Deadlock detection:** `TestDeadlock_*` suites under `-timeout=30s` with a
`runtime/deadlock` build-tag variant. Nightly, not gating.

---

## 7 · Benchmarks

Run in `bench.yml` with `benchstat` comparing PR vs base. Regressions > 10%
fail.

| Benchmark | Package | Budget |
|---|---|---|
| `BenchmarkTick_5Fans_10Sensors` | internal/controller | < 50 µs/tick, 0 alloc |
| `BenchmarkTick_30Fans_50Sensors` | internal/controller | < 500 µs/tick |
| `BenchmarkCurveLinear` | internal/curve | < 100 ns, 0 alloc |
| `BenchmarkCurvePointsBinarySearch` | internal/curve | < 200 ns, 0 alloc |
| `BenchmarkCurveMix_10Sources` | internal/curve | < 2 µs, 0 alloc |
| `BenchmarkMPCSolve` | internal/curve | < 10 ms, < 32 KB alloc |
| `BenchmarkPIEvaluate` | internal/curve | < 500 ns |
| `BenchmarkConfigLoad` | internal/config | < 5 ms |
| `BenchmarkHWMonEnumerate_50Devices` | internal/hwmon | < 20 ms |
| `BenchmarkModulesAliasParse` | internal/hwmon | < 50 ms |
| `BenchmarkSSEBroadcast_100Subscribers` | internal/web | O(1) in producer |
| `BenchmarkFFT_48kHz_1s` | internal/acoustic | < 50 ms |
| `BenchmarkMetricsScrape` | internal/web | < 5 ms |
| `BenchmarkBuildStatus_30Fans` | internal/web | < 2 ms |
| `BenchmarkHWDBMatch_500Entries` (NEW) | internal/hwdb | < 1 ms |

Baselines committed at `docs/benchmarks.md`.

---

## 8 · Integration tests (Tier 3)

| Test | Scope |
|---|---|
| `TestIntegration_HappyPath_DesktopSuperIO` | fakehwmon MSI MEG X570; controller converges; /api/status green |
| `TestIntegration_HappyPath_Server` | fakeipmi; enumerate + write + socket-sidecar |
| `TestIntegration_HappyPath_Laptop` | fakecrosec Framework 13; EC curve |
| `TestIntegration_HappyPath_AIO` | fakeliquid Commander Core; pump_minimum enforced |
| `TestIntegration_HappyPath_GPU` | fakenvml RTX 4090 |
| `TestIntegration_HappyPath_MixedPlatform` | hwmon + NVML + liquid simultaneously |
| `TestIntegration_SetupWizard` | First-boot: no config → token → wizard → apply → running |
| `TestIntegration_SIGHUPReload` | Config edit mid-run; curves update without restart |
| `TestIntegration_SIGTERMGraceful` | Signal → watchdog restore within 2 s |
| `TestIntegration_SIGKILLRecovery` | `ventd-recover` oneshot within 2 s |
| `TestIntegration_Hotplug_FanAdd` | uevent adds pwm4; watcher notices; UI lists |
| `TestIntegration_Hotplug_FanRemove` | uevent removes pwm4; goroutine stops; no leak |
| `TestIntegration_HwmonRenumber` | Reboot scenario: hwmon5→hwmon3; ChipName re-anchors |
| `TestIntegration_CalibrationCrashResume` | Kill mid-calibration; resume from checkpoint |
| `TestIntegration_PanicButton` | POST /api/panic; all fans → MaxPWM; timer restores |
| `TestIntegration_ProfileImport` | Fingerprint match → wizard zero-click |
| `TestIntegration_FleetDiscover` | Two daemons on test LAN; mDNS converges |
| `TestIntegration_MPCFallback` | MPC residual exceeds threshold → PI fallback without glitch |
| `TestIntegration_SleepResume` (NEW) | DBus sleep signal → clean shutdown + reinit |
| `TestIntegration_ImportCoolerControl` (NEW) | `ventd --import-from coolercontrol` produces valid config |

CI budget: integration suite completes in < 2 minutes via faketime.

---

## 9 · End-to-end (browser) tests

Extended in `internal/web/e2e_test.go` via go-rod:

| Scenario | Asserts |
|---|---|
| `E2E_FirstBoot_TokenFlow` | Token → password → dashboard |
| `E2E_WizardZeroClick` | Profile-matched system; single Apply |
| `E2E_FanEdit_DragPoints` | Curve editor drag; API updates |
| `E2E_ApplyDiffModal` | Edit → modal diff → accept/reject |
| `E2E_PanicButton_CountdownVisible` | Countdown → auto-restore |
| `E2E_SSE_Stream` | 2s updates without polling fallback |
| `E2E_SSE_Failover` | SSE killed → polling fallback |
| `E2E_Settings_SystemStatus` | All endpoints return |
| `E2E_Settings_Watchdog_CountdownPing` | sd_notify state surfaces |
| `E2E_Theme_DarkMode` | localStorage persists across reload |
| `E2E_CSP_NoInlineViolations` | Zero CSP violations |
| `E2E_Rollback_Config` (NEW) | Apply → revert to previous snapshot → verify |

CI runs headless Chromium + Firefox (rod supports both). Retry budget ≤2.

---

## 10 · Hardware-in-loop (Tier 5)

Current state: `validation/` has `fresh-vm-smoke.sh`, `run-rig-checks.sh`,
and per-rig phoenix-MS-7D25-* logs.

Extensions (each required for the relevant backend's release):

| Script | Runs on | Gates |
|---|---|---|
| `validation/fresh-vm-smoke.sh` | Incus (Ubuntu/Debian/Fedora/Arch) | existing |
| `validation/rig-check-desktop.sh` | Developer's desktop (Super I/O) | All hwmon invariants vs real hardware |
| `validation/rig-check-server.sh` (new) | BMC/IPMI box | IPMI enumerate + write + graceful restore |
| `validation/rig-check-aio.sh` (new) | Corsair/NZXT/Lian Li box | Liquid backend lifecycle; pump safety |
| `validation/rig-check-laptop.sh` (new) | Framework 13 / ThinkPad | EC read/write; lockout handling |
| `validation/rig-check-sbc.sh` (new) | RPi 5 + ARM NAS | PWM sysfs enumerate + write |
| `validation/rig-check-asahi.sh` (new) | Mac Mini M2 Asahi | Detection + monitoring |
| `validation/rig-check-acoustic.sh` (new) | Rig with USB mic | Baseline + synthetic anomaly |

Each script emits a markdown log committed to `validation/`. **A release tag
is blocked until every applicable rig-check has a green log dated after the
release branch was cut.** This rule is in `docs/release-process.md`.

**Cross-platform rig-checks removed.** Windows/macOS/BSD validation is the
Windows subproject's problem. Linux HIL stays here.

---

## 11 · Regression / issue replay

Every closed bug issue gets a replay test. Naming:
`TestRegression_Issue<N>_<short_slug>`.

**Pattern:**
1. Reproduce the exact conditions (fixture, input, sequence).
2. Assert the correct behaviour.
3. Commit the test in the same PR that fixes the issue; the test fails
   before the fix and passes after.

**Meta-lint** (`tools/regresslint`): scans closed issues with `type: bug`
and verifies `grep -r "Issue<N>_" internal/ cmd/` returns a hit. Issues
labeled `no-regression-test` are exempt with an explanation.

Seed backlog from existing `CHANGELOG.md` / `COVERAGE.md` — see prior
revision of this document for the full 25-item list (issues #86, #103, #115,
#116, #118, #124, #131, #132, #133, #163, #197, #202–#226). No changes; all
still valid.

---

## 12 · Observability & metrics tests

| Test | Asserts |
|---|---|
| `TestMetrics_Shape` | Every metric matches committed `docs/metrics.md` golden |
| `TestMetrics_NoHighCardinality` | No label > 1000 distinct values over 24h synthetic |
| `TestOTel_TraceStructure` | Every tick emits one span with expected attributes |
| `TestHistory_Ring_Correctness` | 30-min ring; eviction, order, memory cap |
| `TestHistory_ConcurrentWriter` | 10 Hz write + 1 Hz read; race-clean |
| `TestStructuredLog_JSONShape` | Every slog emits valid JSON; no interpolated strings |
| `TestAlerts_Drift` | Injected 20% drift triggers `ventd_fan_anomaly{kind="drift"}` |
| `TestAlerts_Bearing` | Injected sub-harmonic triggers `ventd_fan_anomaly{kind="bearing"}` |

---

## 13 · Security tests

| Test | Package | Asserts |
|---|---|---|
| `TestSec_PathTraversal_HwmonWrite` | config | `../`, `%2e%2e`, absolute-symlink rejected |
| `TestSec_CSRFOrigin` | web | Cross-origin POST/PUT/DELETE rejected |
| `TestSec_CSP_NoUnsafeInline` | web | Strict CSP header |
| `TestSec_HSTS_Present` | web | HSTS on TLS |
| `TestSec_PermissionsPolicy` | web | Header present; denies unused features |
| `TestSec_ConstantTimeTokenCompare` | web | Timing histogram ≤ 1σ between correct/wrong over N=10k |
| `TestSec_RateLimitPerIP` | web | 6th attempt in 15min → 429; cooldown honoured |
| `TestSec_RateLimit_Proxy_XFF` | web | XFF walk right-to-left; trusted CIDRs only |
| `TestSec_SelfSignedCert_SANs` | web | Local IP + hostname.local + localhost in SANs |
| `TestSec_TLS_TLS12Min` | web | TLS ≤ 1.1 handshake rejected |
| `TestSec_SetupToken_OneShot` | web | Second use of same token → 403 |
| `TestSec_SessionCookie_Flags` | web | HttpOnly + Secure + SameSite |
| `TestSec_YAMLBombs` | config | Alias/anchor/huge-doc bombs rejected, no OOM |
| `TestSec_UDEVRule_NoWorldWritable` | deploy | All rules scoped to ventd group |
| `TestSec_SystemdUnit_ZeroCaps` | deploy | Main unit CapabilityBoundingSet empty |
| `TestSec_BinaryHasNoCGO` | cmd | `go version -m ventd` shows no cgo |
| `TestSec_BinaryFullRelro` | cmd | `readelf -d` shows BIND_NOW, NX, RELRO |
| `TestSec_ModprobeAllowlist` | ventd-modprobe | Only allowlisted modules; fuzzed for bypass |
| `TestSec_ExprCurve_Sandbox` | curve | No `os`/`io`/`net` reachable from expressions |
| `TestSec_ProfileCapture_NoPII` (NEW) | hwdb | `FuzzAnonymise` 10-min zero-leak (binds to spec-03 RULE-HWDB-06) |

---

## 14 · Supply-chain tests

Gate release tags via `drew-audit.yml` (already landed; gates activate as
P10 tasks merge).

| Test | Asserts |
|---|---|
| `SupplyChain_SBOM_Valid` | `cyclonedx-cli validate` on the SBOM |
| `SupplyChain_SBOM_NoCVE_Critical` | `govulncheck` returns no CRITICAL |
| `SupplyChain_Cosign_VerifyBlob` | Every released artefact verifies |
| `SupplyChain_SLSA_Provenance` | Generated provenance validates |
| `SupplyChain_Reproducible_Rebuild` | Rebuild same tag twice; SHA256 identical |
| `SupplyChain_Docker_Rootless` | Container image `USER` ≠ 0 |
| `SupplyChain_Checksums_Match` | `checksums.txt` present and valid |

---

## 15 · Documentation tests

| Test | Asserts |
|---|---|
| `TestDocs_ConfigExampleParses` | `config.example.yaml` parses cleanly |
| `TestDocs_ConfigReferenceExhaustive` | Every YAML field in code appears in `docs/config.md`; reverse too |
| `TestDocs_RoadmapReferencesValidPhases` | `docs/roadmap.md` phase names match masterplan |
| `TestDocs_APIRefMatchesRoutes` | Every route in server.go documented in `docs/api.md` |
| `TestDocs_MetricsRefMatches` | `docs/metrics.md` matches code-registered metrics |
| `TestDocs_Changelog_Unreleased` | Every merged PR since last tag has a `## [Unreleased]` entry |
| `TestDocs_ExamplesCompile` | Go fences in docs compile |
| `TestDocs_HwdbSchemaReference` (NEW) | `docs/hwdb-schema.md` matches schema.go types |

---

## 16 · CI matrix

### Per-PR, blocking

- `build-and-test-ubuntu` — amd64, glibc, `-race`
- `build-and-test-ubuntu-arm64` — native arm64, `-race`
- `build-and-test-fedora-42` — dnf, `-race`
- `build-and-test-arch` — pacman, `-race`
- `build-and-test-alpine-3.20` — musl, `-race` (pure-Go packages only)
- `e2e-chromium` — go-rod on headless Chromium
- `e2e-firefox` — go-rod on Firefox
- `integration-linux` — Tier 3 integration suite
- `chaos-linux` — Tier 3 chaos-injection
- `fuzz-smoke` — every fuzz target for 10s
- `bench-regression` — benchstat; fail on > 10% regression
- `static-analysis` — vet, golangci-lint, staticcheck, gofmt, govulncheck
- `security-tests` — §13 suite
- `meta-lint` — rule→subtest binding; regression tests for each `Fixes:`
- `shellcheck` — scripts/ + deploy/
- `apparmor-parse-debian13` — unchanged

### Nightly

- `fuzz-long` — every target 10m
- `deadlock-probe` — deadlock-mutex variant
- `govulncheck-deep` — full module graph
- `bench-trend` — commit baseline to `docs/benchmarks.md`

### Release-gating

- `supply-chain` — §14 suite via `drew-audit.yml`
- `rig-check-*` — manual, developer runs; logs committed
- `reproducibility-rebuild` — SHA256 match

### PR annotation, non-blocking

- `coverage-report` — per-package delta as PR comment
- `test-time-budget` — top 20 slowest tests; alert on growth

### Removed from this matrix

- `cross-compile-smoke` (Windows/macOS/BSD/illumos/Android build) — moves to
  the Windows subproject's CI when it spins up. For the Linux product, a
  single nightly `go vet -tags "linux"` and `go build` covers regression.

---

## 17 · Task catalogue

Format: `T-TASK-ID | Track | Depends on main-plan P-task | Files | Goal`.
When a T-task's P-dependency ships, the T-task becomes ready.

### Phase T0 — Foundation [PARTIALLY LANDED]

**T0-INFRA-01 | INFRA | — | `internal/testfixture/**`** — LANDED (partial;
more fixtures ship as backends land).

**T0-INFRA-02 | INFRA | T0-INFRA-01 | `internal/testfixture/fakehwmon/**`** — LANDED.

**T0-INFRA-03 | INFRA | T0-INFRA-01 | `internal/testfixture/faketime/**`** — LANDED.

**T0-META-01 | META | — | `tools/rulelint/**`, `.github/workflows/meta-lint.yml`** — LANDED.

**T0-META-02 | META | — | `tools/regresslint/**`, same workflow** — LANDED.

**T0-META-03 | META | — | `.github/PULL_REQUEST_TEMPLATE.md`** — LANDED.

### Phase T1 — Tier promotion for current backends

**T-HAL-01 | HAL | P1-HAL-01 | `internal/hal/contract_test.go` (new), `.claude/rules/hal-contract.md` (new)**
Goal: HAL contract as table-driven test every backend obeys.
DoD: `TestHAL_Contract` exercises hwmon + nvml; every new backend PR must
add a row.
**Priority:** HIGH — ship before any Phase 2 backend that doesn't already
have it.

**T-CAL-01 | CAL | P1-HAL-02 | `internal/calibrate/safety_test.go`, `.claude/rules/calibration-safety.md`**
Goal: `TestCalSafety_Invariants` mirror of controller safety suite.
DoD: existing ZeroPWMSentinel as subtests; 6 new invariants (checkpoint
atomicity, fingerprint fence, abort, resume idempotency, pump sentinel,
zero-during-stop-sweep).

**T-WD-01 | WD | P1-HAL-01 | `internal/watchdog/safety_test.go` (new), `.claude/rules/watchdog-safety.md` (new)**
Goal: watchdog coverage 23% → >80% via rule-bound invariants.
DoD: 7 invariants (restore on panic, SIGTERM, ctx cancel; per-fan recovery;
fallback on missing pwm_enable; NVIDIA reset; rpm_target writes maxRPM).
**Priority:** HIGHEST — watchdog is safety-critical and undertested.

**T-HOT-01 | HOT | P1-HOT-01 | `internal/controller/bench_test.go` (new)**
Goal: benchmarks + alloc assertions for optimised hot loop.
DoD: `BenchmarkTick_5Fans_10Sensors` and `BenchmarkTick_30Fans_50Sensors`
meet §7 budgets.

**T-FP-01 | FP | P1-FP-01 | `internal/hwdb/match_test.go`, `internal/testfixture/fakedmi/**`**
Goal: table-driven tests for 18+ fingerprints. Becomes 25+ after spec-03.

**T-HWMON-01 | HWMON | P1-MOD-01 | `internal/hwmon/modalias_test.go` (new)**
Goal: modules.alias + modules.builtin-modinfo parser tests with real kernel
samples.
DoD: Ubuntu 24.04, Fedora 42, Arch, Alpine corpus.

**T-HWMON-02 | HWMON | P1-MOD-02 | `internal/hwmon/persist_test.go` (new)**
Goal: append-not-overwrite tests. Existing-file merge + dedup + atomic
rename all covered.

### Phase T2 — New backend coverage

**T-IPMI-01 | IPMI | P2-IPMI-01 | `internal/hal/ipmi/**_test.go`, `internal/testfixture/fakeipmi/**`, `.claude/rules/ipmi-safety.md`**
Goal: full unit + integration coverage for IPMI.
DoD: happy path + BMC-busy retry + Supermicro/Dell/HPE matrix + daemon-exit
fallback.
**Spec:** `specs/spec-01-ipmi-polish.md`.

**T-IPMI-02 | IPMI | P2-IPMI-02 | `internal/hal/ipmi/socket_test.go`**
Goal: socket-sidecar model verified — main unit zero-cap, sidecar one-cap.

**T-LIQUID-01 | LIQUID | P2-LIQUID-01 | `internal/hal/liquid/corsair/**_test.go`, `internal/testfixture/fakeliquid/**`, `.claude/rules/liquid-safety.md`**
Goal: Corsair Commander Core / Core XT / Commander Pro protocol tests.
DoD: USB disconnect + reconnect + pump_minimum enforcement.
**Spec:** `specs/spec-02-corsair-aio.md`.

**T-LIQUID-01b | LIQUID | P2-LIQUID-01b | extends above** — NZXT.
**T-LIQUID-01c | LIQUID | P2-LIQUID-01c | extends above** — Lian Li.
**T-LIQUID-02 | LIQUID | P2-LIQUID-02 | extends above** — Aqua + EK + AORUS.

**T-CROSEC-01 | CROSEC | P2-CROSEC-01 | `internal/hal/crosec/**_test.go`, `internal/testfixture/fakecrosec/**`**
Goal: Framework laptop EC coverage — EC_CMD_HELLO gating + read/write +
lockout handling.

**T-CROSEC-02 | CROSEC | P2-CROSEC-02 | extends above** — ThinkPad/Dell/HP
vendor wrappers.

**T-PWMSYS-01 | PWMSYS | P2-PWMSYS-01 | `internal/hal/pwmsys/**_test.go`, `internal/testfixture/fakepwmsys/**`**
Goal: sysfs PWM coverage — RPi 5 fixture + writeable channel lifecycle.

**T-ASAHI-01 | ASAHI | P2-ASAHI-01 | `internal/hal/asahi/**_test.go`**
Goal: detection gate + role classification on top of hwmon.

### Phase T3 — Install & module subsystem

**T-MODPROBE-01 | MODPROBE | P3-MODPROBE-01 | `cmd/ventd-modprobe/**_test.go`, `internal/testfixture/fakedbus/**`**
Goal: allowlist enforcement + syscall path.
DoD: bypass attempts rejected; logged events verified.

**T-UDEV-01 | UDEV | P3-UDEV-01 | `deploy/rules_test.go` (new)**
Goal: `udevadm verify` under test harness.
DoD: bad rule fixture asserts error.

**T-INSTALL-01 | INSTALL | P3-INSTALL-01 | `scripts/install.test.sh` (new, bats or POSIX)**
Goal: install script under fakeroot.
DoD: systemd-run path + fallback path; rerun idempotent.

**T-INSTALL-02 | INSTALL | P3-INSTALL-02 | extends above**
Goal: coexistence detection. Active fancontrol → warning + exit 2 without
`--force`.

**T-IMPORT-01 | IMPORT | P3-IMPORT-01 | `cmd/ventd/import_test.go` (new)** — NEW.
Goal: `--import-from coolercontrol|fancontrol|thinkfan` round-trips cleanly.
DoD: committed fixtures for each source format; output validates as ventd.yaml.

**T-RECOVER-01 | RECOVER | P3-RECOVER-01 | `cmd/ventd-recover/noalloc_test.go`**
Goal: zero-alloc assertion. Binary ≤ 8 KB amd64. LANDED.

### Phase T4 — Control algorithm

**T-PI-01 | PI | P4-PI-01 | `internal/curve/pi_test.go`, `.claude/rules/pi-stability.md`**
Goal: PI correctness + anti-windup + NaN fallback.
DoD: `PropPIStability` pass; integral clamp enforced; gain bounds rejected
out-of-range.
**Spec:** `specs/spec-04-pi-autotune.md`.

**T-PI-02 | PI | P4-PI-02 | `internal/calibrate/autotune_test.go`**
Goal: Ziegler-Nichols autotune on synthetic plant.
DoD: gains within 10% of analytic optimum.

**T-HYST-01 | HYST | P4-HYST-01 | `internal/controller/hysteresis_test.go`**
Goal: banded hysteresis state machine.
DoD: `PropHysteresisNoFlutter` across 10k random sensor traces.

**T-DITHER-01 | DITHER | P4-DITHER-01 | `internal/controller/dither_test.go` (new)**
Goal: dither distribution + sync-break.
DoD: `PropDitherMean` ≈ 0; adjacent fans distinct instantaneous PWM.

**T-MPC-01 | MPC | P4-MPC-01 | `internal/curve/mpc_test.go`, `.claude/rules/mpc-stability.md`**
Goal: MPC feasibility + residual fallback + model persistence atomicity.
Ships when P4-MPC-01 ships (post-PI soak).

**T-SLEEP-01 | SLEEP | P4-SLEEP-01 | `internal/controller/sleep_test.go` (new)** — NEW.
Goal: DBus sleep/resume via fakedbus.
DoD: PrepareForSleep triggers clean shutdown; PrepareForShutdown is
distinguished; resume reinits devices.

**T-INTERFERENCE-01 | INTERFERENCE | P4-INTERFERENCE-01 | `internal/controller/interference_test.go` (new)** — NEW.
Goal: third-party PWM write detection.
DoD: external pwm_enable change observed within 2 ticks; structured event.

**T-STEP-01 | STEP | P4-STEP-01 | extends controller tests** — NEW.
Goal: step-size thresholds + safety latch.
DoD: degenerate curves can't cause oscillation.

**T-LATCH-01 | LATCH | P4-LATCH-01 | `internal/controller/latch_test.go` (new)** — NEW.
Goal: silence-detection safety latch.
DoD: no writes for N ticks triggers latch; latched write bypasses step-min
threshold but still honours step-max; latch resets after successful write.
Must land in the same PR as T-STEP-01 — tested together or not at all.

**T-HWCURVE-01 | HWCURVE | P4-HWCURVE-01 | `internal/hal/hwmon/hwcurve_test.go` (new), `internal/testfixture/fakehwmon/**`** — NEW, highest-leverage item.
Goal: hardware curve offload detection + upload tests.
DoD: (1) sysfs file detection correctly identifies chips with hardware-
curve support vs without (fakehwmon fixtures for NCT6775, NCT6798, IT87,
and one chip family without the auto_point files). (2) curve upload
round-trips cleanly — written points match what the fixture reports back.
(3) fallback test — when chip claims support but sysfs write fails, falls
back to tick-driven path without PWM glitch. (4) pwm_enable chip-specific
AUTO values verified per family. (5) controller dispatch test: channels
with `supports_hardware_curve` caps skip the tick-driven path.

### Phase T5 — Calibration & profiles

**T-LIVECAL-01 | LIVECAL | P5-LIVECAL-01 | `internal/calibrate/live_test.go`**
Goal: live calibration converges on synthetic 4-hour trace.
DoD: ≥ 80% curve coverage; zero audible ramp events.

**T-HEALTH-01 | HEALTH | P5-HEALTH-01 | `internal/monitor/health_test.go`**
Goal: drift detection on synthetic traces.
DoD: 15/20/25% drift triggers; <5% noise doesn't.

**T-PROF-SCHEMA-01 | PROF | P5-PROF-SCHEMA-01 | `internal/hwdb/schema_test.go`, `.claude/rules/hwdb-schema.md`** — NEW.
Goal: 7 schema invariants bound.
**Spec:** `specs/spec-03-profile-library.md`.

**T-PROF-MATCH-01 | PROF | P5-PROF-MATCH-01 | `internal/hwdb/match_test.go`**
Goal: three-tier matcher + benchmark < 1ms on 500-entry DB.

**T-PROF-SEED-01 | PROF | P5-PROF-SEED-01 | CI check on `profiles.yaml`**
Goal: pre-commit PII grep returns zero; every entry has source citation.

**T-PROF-CAPTURE-01 | PROF | P5-PROF-CAPTURE-01 | `internal/hwdb/capture_test.go`, `internal/hwdb/anonymise_test.go`**
Goal: local profile capture + file perms + anonymisation.
DoD: 100-sample fuzz zero PII; mode 0640; owner ventd.

**T-PROF-CAPTURE-02 | PROF | P5-PROF-CAPTURE-02 | extends above**
Goal: opt-in submission dry-run.
DoD: no network call unless user confirms; diff surfaced before push.

**T-TRUST-01 | TRUST | P5-TRUST-01 | extends schema + hwdb tests** — NEW.
Goal: per-sensor trust level plumbing.
DoD: `sensor_quirks` round-trip; curve engine refuses untrusted by default.

### Phase T7 — Advanced sensing

**T-ACOUSTIC-01 | ACOUSTIC | P7-ACOUSTIC-01 | `internal/acoustic/**_test.go`, `internal/testfixture/fakemic/**`, `.claude/rules/acoustic-safety.md`**
Goal: baseline capture + FFT bounds + non-blocking capture.

**T-ACOUSTIC-02 | ACOUSTIC | P7-ACOUSTIC-02 | extends above**
Goal: anomaly detection. Injected sub-harmonic triggers; clean bearing
doesn't.

**T-FLOW-01 | FLOW | P7-FLOW-01 | extends liquid tests**
Goal: flow/coolant surfaces in /api/status and curves.

**T-COORD-01 | COORD | P7-COORD-01 | `internal/thermal/coord_test.go`**
Goal: headroom file write atomicity + content.

**T-APP-PROFILES-01 | APPPROF | P7-APP-PROFILES-01 | `internal/apphook/**_test.go`** — NEW.
Goal: process-exec detection + rule DSL + false-positive guard.
DoD: sustained-load requirement prevents transient-process switching.

### Phase T8 — Observability

**T-METRICS-01 | METRICS | P8-METRICS-01 | `internal/web/metrics_test.go`, `docs/metrics.md`**
Goal: metric-shape golden + cardinality guard.

**T-OTEL-01 | OTEL | P8-OTEL-01 | `internal/telemetry/otel_test.go`**
Goal: trace structure + sampler default.

**T-HISTORY-01 | HISTORY | P8-HISTORY-01 | `internal/monitor/history_test.go`**
Goal: ring-buffer correctness + concurrent writer.

**T-CLI-01 | CLI | P8-CLI-01 | `cmd/ventdctl/**_test.go`**
Goal: every subcommand against local daemon. Exit codes + --help golden.

**T-FLEET-01 | FLEET | P8-FLEET-01 | `internal/fleet/**_test.go`**
Goal: mDNS in test-LAN namespace. Two daemons converge within 10s simulated.

**T-FLEET-02 | FLEET | P8-FLEET-02 | `internal/web/e2e/fleet_test.go`**
Goal: fleet view; clicking opens target URL.

**T-ALERTS-01 | ALERTS | P8-ALERTS-01 | `internal/alerts/**_test.go`** — NEW.
Goal: built-in defaults + rule DSL + delivery plumbing (webhook/email mocks).

**T-ROLLBACK-01 | ROLLBACK | P8-ROLLBACK-01 | `internal/config/history_test.go`** — NEW.
Goal: snapshot on every Apply; retention 30 applies; one-click restore.

### Phase T9 — UX

**T-WIZARD-01 | WIZARD | P9-WIZARD-01 | extends e2e**
Goal: zero-click on profile-matched fixture.

**T-I18N-01 | I18N | P9-I18N-01 | extends e2e**
Goal: Accept-Language drives UI; override persists.

**T-EXPR-01 | EXPR | P9-EXPR-01 | `internal/curve/expr_test.go`**
Goal: sandbox + fuzz + bounded eval.
DoD: `FuzzExprCurve` pass; no `os`/`io`/`net` reachable.

### Phase T10 — Supply chain

**T-SUPPLY-01 | SUPPLY | P10-SBOM-01 | extends `drew-audit.yml`**
Goal: SBOM validate + govulncheck both green at tag.

**T-SUPPLY-02 | SUPPLY | P10-SIGN-01 | extends above**
Goal: cosign keyless verify + SLSA verifier.

**T-SUPPLY-03 | SUPPLY | P10-REPRO-01 | `.github/workflows/verify-reproducible.yml` (landed)**
Goal: rebuild-and-diff. Ships at every tag.

### Phase TX — Standing coverage (evergreen)

**TX-COVERAGE-CRAWL | META | — | `COVERAGE.md`**
Every two weeks: re-measure coverage, update `COVERAGE.md`, open issues for
regressions > 2%.

**TX-FUZZ-CORPUS-GROW | META | — | `internal/*/testdata/fuzz/`**
Weekly: run all fuzz targets 1h; commit new crashers as corpus entries.

**TX-REGRESSION-AUDIT | META | — | `.github/ISSUE_TEMPLATE/**`**
Monthly: audit closed issues without regression tests.

**TX-BENCH-TREND | META | — | `docs/benchmarks.md`**
Weekly: record benchmark baselines; trend as GitHub Discussion.

**TX-HIL-SCHEDULE | META | — | `validation/`**
Pre-release: confirm all applicable rig-check logs dated after release-branch
cut.

---

## 18 · Prioritisation — what lands first

Solo-dev reality: not every task here runs. Critical path:

1. **T-WD-01** — watchdog coverage from 23% to >80%. Safety-critical.
2. **T-HAL-01** — lock the HAL contract before any more backends land.
3. **T-CAL-01** — calibrate safety-invariant binding.
4. **T-HOT-01** — bench + alloc assertions on the optimised hot loop.
5. **T-IPMI-01** — ships with spec-01 (v0.3.x polish).
6. **T-SLEEP-01** — ships with P4-SLEEP-01 (highest-leverage Phase 4 item
   per marketresearch.md §5).
7. **T-LIQUID-01** — ships with spec-02 (Corsair, v0.4.0).
8. **T-PI-01 + T-PI-02 + T-HYST-01** — ships with spec-04 (v0.6.0).
9. **T-LATCH-01 + T-STEP-01** — must land together. v0.6.x follow-up.
10. **T-HWCURVE-01** — hardware curve offload. Ships when real NCT/ITE
    hardware validation confirms the sysfs pattern.
11. **T-PROF-SCHEMA-01 + T-PROF-MATCH-01** — ships with spec-03 (v0.5–0.7).
12. **TX-COVERAGE-CRAWL** — start the cadence early.
13. Everything else in dependency order behind feature merges.

---

## 19 · What Claude Code does NOT do with tests

- **Run tests on real hardware.** That's the developer's role under main
  plan §11.
- **Add tests for features not yet merged.** If the P-task hasn't landed,
  the T-task is blocked.
- **Mutate non-test code in a test-only PR.** If a test reveals a bug, the
  PR is closed and the bug is filed; the fix PR adds the test alongside it.
- **Rewrite existing tests for style.** Only delete/rewrite if the invariant
  is being deleted or strengthened.
- **Publish test metrics externally.** Coverage goes in `COVERAGE.md`;
  nothing leaves the repo without explicit developer action.

---

## 20 · Cost discipline for test work

Test-writing is Haiku-friendly mechanical work in the majority of cases:

- Writing fixture corpus data (fakeipmi canned responses, fakedmi fingerprints): **Haiku**.
- Writing test scaffolds from an existing pattern: **Haiku**.
- Writing new table-driven tests against a clear schema: **Haiku** or Sonnet.
- Writing property-test generators or fuzz harnesses: **Sonnet**.
- Writing safety-invariant tests with subtle concurrency: **Sonnet**.
- Designing a new invariant or proving a safety property: **chat with Opus**,
  then Sonnet implements.

Budget: $5–15 per T-task. T-tasks that run over are usually too big — split
them by invariant or by subsystem.

---

## 21 · Review rows (applied by main plan §14)

These rows, part of the main plan's review checklist, make tests first-class
in every PR:

| Row | Check |
|---|---|
| R10 | Every `.claude/rules/*.md` touched has matching subtest edits |
| R11 | Every closed issue in `Fixes:` has a `TestRegression_Issue<N>_*` |
| R12 | Any new safety-critical function bound to a rule invariant |
| R13 | Any new goroutine covered by a lifecycle + cancellation test |
| R14 | Any new backend passes `TestHAL_Contract` for its channel kind |
| R15 | Any new public metric/route/config field reflected in docs golden |

---

**End of document.** Maintained by the developer. When reality diverges,
fix one of them deliberately.
