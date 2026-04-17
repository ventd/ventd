# Testing guide

This doc has one job: make the test suite operable as a diagnostic
tool, both for humans staring at a regression and for Claude Code
running unattended against a PR.

If you add a new test group, update `scripts/diagnose-tests.sh` AND
the "Groups" and "Failure playbook" sections below.

---

## One-command diagnosis

```sh
scripts/diagnose-tests.sh            # all groups, race on
scripts/diagnose-tests.sh safety     # safety-critical subset only
scripts/diagnose-tests.sh fuzz       # fuzz seed corpora (fast)
scripts/diagnose-tests.sh fuzz-long  # -fuzz for DIAGNOSE_FUZZTIME (default 30s)
```

Every run ends with a `DIAGNOSE-SUMMARY BEGIN` … `DIAGNOSE-SUMMARY END`
block. This block is the stable parseable surface — treat everything
above it as freeform log.

Exit code: `0` iff every selected group passed. One red group does
not abort the run; the point is to surface all breakage in one pass.

---

## Groups

| Group name            | What it exercises                                                         | File(s)                                                    |
|-----------------------|---------------------------------------------------------------------------|------------------------------------------------------------|
| `safety_watchdog`     | `internal/watchdog` — every restore branch: hwmon normal/fallback, rpm_target normal/fallback, nvidia skip, Deregister LIFO | `internal/watchdog/restore_matrix_test.go`                 |
| `safety_controller`   | `internal/controller` — the 12-rule safety invariant suite                | `internal/controller/safety_test.go` (pre-existing)        |
| `safety_calibrate`    | `DetectRPMSensor` happy path + no-correlation + nvidia refusal + concurrency + existing `Abort*` tests | `internal/calibrate/detect_test.go`, `calibrate_test.go`   |
| `hwmon_parsers`       | autoload.go module and driver parsers, koBasename, moduleFromPath         | `internal/hwmon/autoload_test.go` (pre-existing)           |
| `nvidia_unavailable`  | Every public function's `ErrNotAvailable` path, `goStringFromC`, refcount concurrency | `internal/nvidia/unavailable_test.go`                      |
| `web_handlers`        | Setup/Detect/Abort HTTP handlers — methods, params, state machine         | `internal/web/setup_handlers_test.go`, `handlers_detect_abort_test.go` |
| `cmd_preflight`       | `ventd --preflight-check` subcommand JSON shape + reason strings          | `cmd/ventd/preflightcheck_smoke_test.go`                   |
| `config_fuzz_seed`    | `FuzzParseConfig` — panic-safety + validate contract on seed corpus       | `internal/config/fuzz_test.go`                             |
| `hwmon_fuzz_seed`     | `FuzzParseSensorsDetect` — panic-safety on sensors-detect stdout          | `internal/hwmon/fuzz_test.go`                              |

`fuzz-long` additionally runs real fuzzing (`-fuzz` with a wall-clock
budget) against the two seeded targets. Reserve it for overnight runs
or when investigating a suspected parser regression.

---

## Workflows for Claude Code

These are the canonical recipes Claude Code should follow in an
automated session. They all start by running the relevant group via
`scripts/diagnose-tests.sh` and then narrowing to the specific test
that failed.

### Workflow 1 — "A user reports a broken fan after daemon exit"

Symptom class: watchdog or controller.

1. Run the safety slice:
   ```sh
   scripts/diagnose-tests.sh safety
   ```
2. If `safety_watchdog` failed, the failing test name identifies the
   branch. Map the name to the branch in `watchdog.go`:
   - `TestRestore_HwmonPWM_*` → `restoreOne` default branch, lines 186–208
   - `TestRestore_RPMTarget_*` → `restoreOne` rpmTarget branch, lines 159–184
   - `TestRestore_Nvidia_*` → `restoreOne` nvidia branch, lines 142–157
   - `TestDeregister_*` → `Deregister`, lines 104–113
3. If `safety_controller` failed: the subtest name is a direct 1:1 of
   a rule in `.claude/rules/hwmon-safety.md`. Fix the rule's
   implementation in `internal/controller`, not the test.
4. If `safety_calibrate` failed on abort or detect, check whether the
   regression is in the fan-control primitive (hwmon package) vs the
   orchestration (calibrate package).

### Workflow 2 — "Setup wizard broken in the browser"

Symptom class: web handler.

1. Run the web slice:
   ```sh
   scripts/diagnose-tests.sh web
   ```
2. Method-enforcement regressions (`*_NonPOST_*`) mean someone removed
   a `if r.Method != http.MethodPost` guard from a handler in
   `internal/web/server.go`. Restore the guard; do not "fix" the test.
3. Missing-param regressions (`*_MissingFanParam_*`) mean the handler
   stopped validating `r.URL.Query().Get("fan")`. Same fix: restore
   the 400 path.
4. If `TestHandleSetupStatus_ReturnsJSONWithNeededField` fails, the
   `ProgressNeeded` struct in `internal/setup` lost its `needed`
   field (probably renamed). Update the struct tag back or update
   **both** the test and the UI consumer in `internal/web/ui/`.

### Workflow 3 — "Setup returns `UNKNOWN` for a known hardware state"

Symptom class: new `hwmon.Reason` constant without CLI wiring.

1. Run `scripts/diagnose-tests.sh cmd`.
2. `TestRunPreflightCheck_RespectsMaxKernel` deterministically expects
   `KERNEL_TOO_NEW` on any host. If it says `UNKNOWN`, the fix is in
   `cmd/ventd/preflightcheck.go:preflightReasonString` — add a case.
3. `TestPreflightReasonString_HandlesAllKnownReasons` enumerates every
   constant this switch must handle. Add the new one to its table and
   the matching case to the switch.

### Workflow 4 — "NVML crashes or returns weird values"

Symptom class: nvidia package.

1. Run:
   ```sh
   scripts/diagnose-tests.sh all 2>&1 | grep -A2 nvidia_unavailable
   ```
2. If `TestGoStringFromC_*` fails, the C-string-to-Go copy in
   `nvidia.go:goStringFromC` broke. This function is reached every
   time NVML returns an error string; a panic here crashes the
   daemon.
3. If `TestPublicFunctions_ReturnErrNotAvailable/*` fails for a
   specific function, that function lost its `!Available()` short-
   circuit. Restore it at the top of the function.
4. If `TestInit_ConcurrentCallsRespectRefcount` fails, `initMu` or
   `loadOnce` sync coverage regressed. See `nvidia.go:96-127`.

### Workflow 5 — "Found a malformed config in the wild"

Symptom class: parser regression.

1. Reproduce the crash by saving the offending YAML as, e.g.,
   `/tmp/crash.yaml`.
2. Seed the fuzz corpus:
   ```sh
   cp /tmp/crash.yaml internal/config/testdata/fuzz/FuzzParseConfig/crash
   scripts/diagnose-tests.sh fuzz
   ```
3. If the seed crashes reproducibly, run the long fuzzer:
   ```sh
   DIAGNOSE_FUZZTIME=2m scripts/diagnose-tests.sh fuzz-long
   ```
4. Fix the parser in `internal/config/config.go:Parse` (or the called
   `yaml.Unmarshal` handling). Do NOT delete the seed — it becomes a
   regression test automatically.

### Workflow 6 — "Install step loses fan control after an lm-sensors upgrade"

Symptom class: sensors-detect parser regression.

1. Capture the real `sensors-detect` output from the affected host:
   ```sh
   sudo sensors-detect --auto > /tmp/sd.txt 2>&1
   ```
2. Drop into the fuzz corpus:
   ```sh
   cp /tmp/sd.txt internal/hwmon/testdata/fuzz/FuzzParseSensorsDetect/real
   scripts/diagnose-tests.sh fuzz
   ```
3. Existing unit tests (`internal/hwmon/autoload_test.go`) pin
   specific expected parses. The fuzz target only guards against
   panics and empty-module leakage. If the fuzz passes but the
   specific host still misdetects, add a case to `autoload_test.go`.

---

## Failure playbook (cheat sheet)

| Failing test substring            | First file to open                          |
|-----------------------------------|---------------------------------------------|
| `TestRestore_`                    | `internal/watchdog/watchdog.go:129-208`     |
| `TestDeregister_`                 | `internal/watchdog/watchdog.go:104-113`     |
| `TestSafety_`                     | `internal/controller/controller.go` + rule  |
| `TestDetectRPMSensor_`            | `internal/calibrate/calibrate.go:912-1030`  |
| `TestAbort`                       | `internal/calibrate/calibrate.go:255-270`   |
| `TestHandleSetup`                 | `internal/web/server.go:873-944`            |
| `TestHandleDetectRPM`             | `internal/web/server.go:1039-1067`          |
| `TestHandleCalibrateAbort`        | `internal/web/server.go:845-869`            |
| `TestRunPreflightCheck_`          | `cmd/ventd/preflightcheck.go`               |
| `TestPreflightReasonString_`      | `cmd/ventd/preflightcheck.go:50-64`         |
| `TestPublicFunctions_`            | `internal/nvidia/nvidia.go` (add short-circuit) |
| `TestGoStringFromC_`              | `internal/nvidia/nvidia.go:529-542`         |
| `TestNonVidiaBuild_`              | `internal/nvidia/nvidia_nonvidia.go`        |
| `FuzzParseConfig`                 | `internal/config/config.go:436-451`         |
| `FuzzParseSensorsDetect`          | `internal/hwmon/autoload.go:346-403`        |

---

## Ground rules for adding tests

1. **Test the behaviour, not the implementation.** Name the
   invariant in the test's doc comment. If the test would still pass
   after you secretly mutate a load-bearing constant, strengthen the
   assertion.
2. **Anchor log strings that ops greps for.** The watchdog tests pin
   `"wrote PWM=255"` because that substring appears in production
   incident runbooks. If you soften the log line, update the test
   AND the runbook together.
3. **Leave a reference note.** Every new test file starts with a
   block comment explaining WHY the file exists, WHAT it pins, and
   WHERE the branch under test lives. Future Claude Code sessions
   and future you both need that.
4. **Don't mock what you can fake.** `t.TempDir()` + a fake sysfs
   tree is cheaper to maintain than an injected interface. Use fakes
   for file-system semantics; reserve interfaces for things that
   can't be faked cheaply (NVML, exec.Command).
5. **New fan types, new reasons, new metrics → extend the matching
   exhaustiveness guard.** The guards are:
   - `TestRegister_FanTypeCoverage` (watchdog)
   - `TestPreflightReasonString_HandlesAllKnownReasons` (cmd)
   - `TestPublicFunctions_ReturnErrNotAvailable` (nvidia)
   - `TestReadMetric/*` table (nvidia)

---

## What this suite does NOT cover

Documented so nobody mistakes a green diagnose for full confidence.

- **Real NVML calls.** Requires the NVIDIA driver + a GPU. Exercised
  on the dev-box only via `internal/nvidia/nvidia_smoke_test.go`.
- **Real sysfs writes to a physical fan.** Covered by the validation
  matrix (see `validation/`), not by `go test`.
- **Install-time `modprobe` / `sensors-detect` exec paths.** Require
  root and matching hardware; exercised via `ventd --probe-modules`
  against real machines in the validation fleet.
- **End-to-end browser flows.** `internal/web/e2e_test.go` uses rod
  for a smoke test but does not cover the full setup wizard journey.
- **PID-1 reboot refusal.** Tracked by #177. The current reboot test
  pins CURRENT behaviour (always 200); when the guard lands the
  assertion flips.
