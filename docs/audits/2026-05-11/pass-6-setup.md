# Pass 6: Deep-Read of `internal/setup/`

**Date**: 2026-05-11
**Audit**: Structural deep-read of every non-test `.go` file under `internal/setup/`
**Baseline commit**: `b46c1a5` (post #1034 + audit pass 1-5 docs)
**Scope**: `setup.go` (2879 LOC), `gates.go`, `gates_acoustic.go`, `lock.go`, `modprobe.go`, `heuristic.go`
**Output**: 10 findings

The contents of `phantom_verify*.go`, `restore_excluded*.go`, `reprobe.go` referenced in
pass-1's scope-list are actually all inlined into `setup.go` itself (the test files
`phantom_verify_test.go` / `restore_excluded_test.go` / `reprobe_test.go` exist; the
production code does not split). The audit covers the live production code as it sits.

## Severity legend

- **CRITICAL** — load-bearing safety/correctness invariant fails on a realistic input path
- **HIGH** — rule violation or design contract not enforced; reachable in production
- **MEDIUM** — gap that won't bite today but will silently regress later
- **LOW** — code-hygiene observation; not a functional bug

---

## Finding 1 (CRITICAL) — `restoreExcludedChannels` is skipped on the `allPhantom` early-exit path

**File**: `/root/ventd-work/internal/setup/setup.go`
**Lines**: 1144-1161 (`allPhantom` block)
**Rule**: RULE-SETUP-NO-ORPHANED-CHANNELS

When Phase 5b's polarity probe classifies every controllable hwmon channel as
`phantom`, the wizard early-returns at line 1159 with the "All fan channels are
firmware-locked" message. `restoreExcludedChannels` is at line 1303 — past the
early-exit — and therefore **never runs on this path**.

At the point of the early return, Phase 5a's chip-freeze loop (lines 1029-1033) has
already written `pwm_enable=1` (manual) plus the original PWM byte to every probed
hwmon channel, and Phase 5b's polarity probe has just driven each channel to PWM≈128
and let the polarity prober's deferred restore write the baseline back. Manual mode
and the probe's end-state byte are left in place; no channel gets handed back to
BIOS auto. Watchdog only fires on daemon-exit.

The motivating failure (issue #753) is "channels stranded at probe-end PWM under
manual mode"; the allPhantom path is the exact class the rule was written to prevent.

**Fix**: insert `restoreExcludedChannels(fans, nil, m.logger)` (or equivalent — every
channel is "excluded" by virtue of the early return) before the `return` at line 1160.

---

## Finding 2 (CRITICAL) — NVIDIA-only hosts wrongly classified as "all phantom"

**File**: `/root/ventd-work/internal/setup/setup.go`
**Lines**: 1107-1108, 1146-1152

Phase 5b marks every non-hwmon fan (NVIDIA, IPMI) as `PolarityPhase = "phantom"`
at line 1108 — a *convention* meaning "no polarity probe applicable" rather than
"firmware-locked". The subsequent `allPhantom` check at line 1147-1152 then says:

```go
allPhantom := true
for _, f := range fans {
    if f.Type == "hwmon" && f.DetectPhase == "found" && f.PolarityPhase != "phantom" {
        allPhantom = false
        break
    }
}
```

On a host with **zero hwmon fans + one NVIDIA GPU fan**, the loop never finds a
hwmon-found-non-phantom entry, so `allPhantom` stays `true`. The wizard exits with
"All fan channels are firmware-locked or unresponsive. Ventd will run in
monitor-only mode" even though the NVIDIA fan is fully controllable.

Same bug affects `DetectPhase == "heuristic"` — a hwmon fan that responded to PWM
but couldn't correlate a tach sensor (so the heuristic sensor-binding path landed)
also fails the `DetectPhase == "found"` test, so the allPhantom check excludes it.

**Fix**: include NVIDIA fans and heuristic-bound hwmon fans in the "real" count.
The right semantics is "are there ANY controllable channels surviving Phase 5", not
"are there any non-phantom hwmon channels".

---

## Finding 3 (HIGH) — Wizard PID lock is defined but never acquired in production

**File**: `/root/ventd-work/internal/setup/lock.go` (all of it)
**Rule**: RULE-WIZARD-GATE-LOCK-01..03

`AcquireWizardLock` / `ForceReleaseWizardLock` / `ErrWizardAlreadyRunning` form a
complete coordination primitive with test bindings under
`internal/setup/lock_test.go`. `internal/hwmon/preflight_probes.go::liveAnotherWizardRunning`
reads the lock file path. But **no production code calls `AcquireWizardLock`** —
verified via:

```
$ rg -n 'AcquireWizardLock' --type=go | grep -v _test.go
internal/setup/lock.go:63:func AcquireWizardLock() (release func(), err error)
```

Result:
- `Manager.Start()` / `Manager.run()` do not write the PID file.
- `internal/hwmon`'s `liveAnotherWizardRunning` probe always returns `false`
  (no file exists to read), so the preflight `ReasonAnotherWizardRunning` branch
  is dead. RULE-PREFLIGHT-CONCURRENT_wizard is structurally inert.
- Concurrent wizard runs across two sibling daemons (or daemon + CLI re-entry
  via `--setup`) cannot detect each other; the existing `m.running` mutex
  guard only serialises within a single Manager instance.
- The "Take over PID N" UI button (referenced by RULE-WIZARD-GATE-LOCK-03's
  rationale) has no live signal to consume.

This is structurally the same class as pass-1's category-B findings: a
documented contract with bound tests, no production wiring.

**Fix**: wrap `Manager.run`'s outer goroutine in
`release, err := AcquireWizardLock(); defer release()`. Surface
`ErrWizardAlreadyRunning` to the caller as the recovery-classifier's
`ClassConcurrentInstall`.

---

## Finding 4 (HIGH) — `Manager.run` has no panic-recover; calibration loop crash kills the daemon goroutine

**File**: `/root/ventd-work/internal/setup/setup.go`
**Lines**: 732-744 (`run`'s only `defer`)

The `defer` in `run` does nothing but flip `m.running=false; m.done=true` and call
`m.cancel`. There is no `recover()`. Any panic — and there are multiple panic-prone
paths in the call tree (catalog match, `hwdiag.Store.Set` with unexpected payload,
malformed sysfs from a flaky chip, `regexp.MustCompile` reuse under concurrent
state) — will propagate to the goroutine and kill the wizard with no recovery
surface for the operator.

The bound subtests for RULE-WIZARD-GATE-04 only cover `PhaseGate.Body` panics. The
PhaseGate-driven `Manager.run` refactor is documented as deferred (per the
`gates_acoustic.go` comment: "the wizard's eventual `Manager.run`
PhaseGate-slice refactor (#67); until that lands the gate is exercised in isolation").

Until #67 lands, `run` is panic-bare. The fact that the goroutine *also* hosts the
inner Phase 5a `detWg.Wait()` and Phase 6 `wg.Wait()` calibration WaitGroups means a
panic in the surrounding code can leave per-fan goroutines unjoined as well.

**Fix**: add `defer func() { if r := recover(); r != nil { m.logger.Error(...);
m.mu.Lock(); m.errMsg = ...; m.mu.Unlock() } }()` as the outermost defer in `run`.

---

## Finding 5 (HIGH) — Polarity probe outcome is in-memory only; the daemon never reads it back

**File**: `/root/ventd-work/internal/setup/setup.go`, lines 1100-1142
**Rules**: RULE-POLARITY-08 (ApplyOnStart matches persisted polarity results to live channels)

The wizard's Phase 5b polarity classification writes the result to `fans[i].PolarityPhase`
and feeds it into the allPhantom decision + the Phase 6 calibration skip. It never
calls into `polarity.PolarityStore.Save` or anything in
`internal/polarity/persistence.go`. The generated `config.Fan` carries no polarity
field either. Result: on the next daemon restart, RULE-POLARITY-08's
`ApplyOnStart` finds no persisted record for any channel the wizard probed, sets
every channel's `Polarity` to `"unknown"`, and the daemon either re-runs the probe
(if it's wired — pass-1 noted `polarity.HwmonProber.ProbeAll` has zero production
callers) or operates with `Polarity="unknown"`, at which point
`polarity.WritePWM` refuses every write with `ErrPolarityNotResolved`.

The wizard's polarity work is *advisory* only — it gates calibration skip on
phantom and nothing else. The persisted contract that the rule documents
(daemon-start reads what wizard wrote) is not implemented from the wizard side.

**Fix**: at the end of Phase 5b (or Phase 6 success path), call
`polarity.PolarityStore.Save(...)` for every classified channel so the daemon's
`ApplyOnStart` can hydrate state on the next restart.

---

## Finding 6 (HIGH) — Calibration writes bypass `polarity.WritePWM`; inverted-fan calibration miscalibrates

**File**: `/root/ventd-work/internal/setup/setup.go`, lines 1163-1221
**File**: `/root/ventd-work/internal/calibrate/calibrate.go`, lines 614, 692, 877, 1091
**Rules**: RULE-POLARITY-05, RULE-OPP-PROBE-04 (analogous), and the polarity-aware-write invariant

Phase 6 calibration calls `m.cal.RunSync(ctx, cfgFan)`. The calibrate manager
writes via `b.Write(ch, pwm)` directly through the HAL backend. The hwmon HAL
backend does NOT invert polarity (verified — `internal/hal/hwmon/backend.go` has
zero `polarity` references). So an inverted fan that Phase 5b just classified as
`PolarityPhase="inverted"` will go through Phase 6 calibration as though it were
normal — PWM=255 writes drive the fan to minimum, PWM=0 writes drive it to
maximum, the StartPWM detection misbehaves, and the fan likely lands as
`CalPhase="error"` or `CalPhase="skipped"`.

Phase 6's only polarity check is `PolarityPhase == "phantom"` (line 1170). Inverted
fans proceed to calibration without their polarity being applied.

This is partly mitigated because v0.5.x's polarity infrastructure isn't fully
wired anyway (Finding 5), but in any test or production where the calibration
sweep ends up writing PWM through `b.Write` without going through
`polarity.WritePWM`, the rule is violated. Pass-1 found this is true generally
(opportunistic probes write via polarity, controller writes don't).

**Fix**: either (a) restructure Phase 6 to call a polarity-aware write helper, or
(b) have the calibrate package consume per-fan polarity hints. The latter is
deeper-touch but matches the v0.6.x smart-mode pivot.

---

## Finding 7 (MEDIUM) — `EmitEvent` exported but zero production callers; per-fan events never emitted

**File**: `/root/ventd-work/internal/setup/setup.go`, lines 432-442
**Pass-1 reference**: category E "NEEDS_VERIFICATION" for `setup.Manager.EmitEvent`

Confirmed: the only emission site is `setPhase` (line 381) which calls
`appendEventLocked` directly with `"phase."+phase`. `EmitEvent` is a
public-API hook with the comment "for callers outside setup.Manager (e.g. the
calibrate manager bridging fan-level transitions)". The calibrate manager does not
call it. No production code does.

The activity-feed SSE (`/api/setup/events` → `handleSetupEvents` →
`s.setup.EventsSince`) is *partially* wired — phase transitions reach the stream,
per-fan events (detect-started, calibration-done, etc.) never do. The UI sees
only the seven phase strings, not the richer per-fan timeline the comment promises.

This is the same class as Finding 5: a documented contract with bound tests, no
production wiring.

**Fix**: either wire `EmitEvent` from the calibration manager (passing it through
the existing `setup.Manager` reference) or delete the `EmitEvent` surface and
narrow `appendEventLocked` to package-internal use.

---

## Finding 8 (MEDIUM) — Phase 5b transient probe errors classify channel as `phantom`

**File**: `/root/ventd-work/internal/setup/setup.go`, lines 1132-1138

```go
res, err := prober.ProbeChannel(ctx, ch)
if err != nil && ctx.Err() != nil {
    break
}
if err != nil {
    fans[i].PolarityPhase = "phantom"
} else {
    fans[i].PolarityPhase = res.Polarity
}
```

Any non-context probe error (sysfs EIO, tach read failure on a glitchy chip, file
deadline, etc.) becomes `phantom`. RULE-POLARITY-03's definition of phantom is
"physically dead channel" — a transient read error is a different failure class
that should retry or surface as `unknown`. RULE-POLARITY-04's restore-on-all-paths
already protects against PWM stranding, so a retry would be safe.

The consequence: a flaky hwmon chip mid-Phase-5b will degrade a real fan to phantom
silently, the fan gets handed back to BIOS auto, and the operator never sees the
fan in their config. RULE-DOCTOR-04's graceful-degrade rationale applies — but
here the asymmetry is wrong (admit on transient error rather than refuse).

**Fix**: distinguish ctx errors from probe-internal errors. On probe-internal
error, log WARN + leave `PolarityPhase = "unknown"` rather than collapsing to
phantom, and let downstream phases treat unknown as "include but un-calibrated"
or retry.

---

## Finding 9 (MEDIUM) — `Manager.Start()` does not reset the in-memory event ring

**File**: `/root/ventd-work/internal/setup/setup.go`, lines 346-372

Every other state field is cleared on Start (`errMsg`, `failureClass`, `phase`,
`fans`, `result`, `installLog`, etc.). `m.events` is not. In tests that re-Start
the same Manager (rare — wizard refuses re-Start after `done`, see line 352-354,
so this affects only test fixtures that clear `done` directly), events from
the previous run remain in the ring and bleed into the next run's SSE feed.

Not a functional bug today because `Manager.Start` is one-shot per Manager
lifetime. Becomes a latent issue if a future "retry from failed state" path is
added.

**Fix**: clear `m.events` alongside the other Start-time resets.

---

## Finding 10 (MEDIUM) — Production code constructs `polarity.HwmonProber{}` zero-value with no clock injection; tests can't observe the live-time path

**File**: `/root/ventd-work/cmd/ventd/main.go`, line 887

```go
setupMgr.SetPolarityProber(&polarity.HwmonProber{})
```

The zero-value works (each method has a nil-check fallback to `time.Sleep` /
`os.ReadFile` etc.), and the wiring closes the #1026 gap. But:

- The `clock()`, `now()`, `readFile`, `writeFile` defaults are deferred to live
  syscall paths. No test can hook in to verify production behaviour without
  patching the Manager-side wiring (which they don't, because the wiring is
  in main.go).
- The 3-second `HoldDuration` per channel adds ~3s × N to the wizard time
  budget. On a 5-fan host that's +15s, on a 12-fan server +36s. Probably fine
  but worth flagging for HIL feedback.
- There's no clear path for the prober to honour an Abort. The ctx is threaded
  through `ProbeChannel` but the prober's `time.Sleep` inside `clock()` is the
  default — `time.Sleep` doesn't observe ctx. The Phase 5b loop checks
  `ctx.Err()` between channels (line 1116) but a stuck `clock()` mid-channel
  can hold for up to 3s past Abort.

**Fix**: pass a context-aware clock from main.go (e.g. a function that does
`select { case <-time.After(d): case <-ctx.Done(): }`) and ensure the prober uses
it. Stretch goal: surface the per-channel timeout as a config knob.

---

## Cross-cutting observations

**Lock contract self-consistency**: The lock-path precedence
(`$VENTD_WIZARD_LOCK_DIR > /run > $XDG_RUNTIME_DIR > /tmp`) is duplicated
verbatim in `internal/setup/lock.go::WizardLockPath` and
`internal/hwmon/preflight_probes.go::wizardLockPath`. The two helpers must stay
in sync. There's no shared function; both files re-implement the precedence
from scratch. Worth a follow-up to consolidate.

**RULE-SETUP-PHANTOM-VERIFY admit-on-error policy**: `verifyHwmonChannelSpins`
admits the channel on any IO error (line 1497, 1505, 1523-1524). Rule's stated
asymmetry: "a verify-IO failure is NOT a downgrade signal". Consistent.
However, the deferred restore at line 1500-1503 swallows its own error
(`_ = writeSysfsUint8(...)`) — if both the original-PWM-capture and the
PWM=255-write succeeded but the deferred restore fails (very unlikely but
possible under sysfs flakiness), the channel is left at PWM=255 with no
diagnostic surface. The next wizard run's restoreExcludedChannels would
recover it. Low priority.

**RULE-SETUP-REPROBE-01 compliance**: `Manager.LoadModule`'s success path calls
`afterDriverInstall` at line 57. Failure path returns early at line 46. The
`reprobeFn` nil-check at `setup.go:518` handles the unwired case. All four
bound subtests are satisfied. No findings here.

**RULE-WIZARD-GATE-CALIBRATE-ACOUSTIC-01**: The `calibrate_acoustic` gate is
correctly invoked at line 1264 (`runAcousticGate`). The "no-op when MicDevice
empty" / "non-fatal on runner error" semantics match the rule. The gate is
unreachable in production today because no main.go path calls
`SetAcousticGateOptions` with a non-empty `MicDevice` — confirmed by:

```
$ rg -n 'SetAcousticGateOptions' --type=go | grep -v _test.go
internal/setup/setup.go:282:func (m *Manager) SetAcousticGateOptions(...)
```

This is a pass-1-class wiring gap but mentioned only for completeness — the
mic-calibration CLI is its own subcommand and the wizard's gate is documented
as "until that wiring lands the gate is exercised in isolation". Track only.

---

## Findings summary

| # | Severity | Title |
|---|---|---|
| 1 | CRITICAL | restoreExcludedChannels skipped on allPhantom early-exit |
| 2 | CRITICAL | NVIDIA-only / heuristic-bound hosts wrongly classified all-phantom |
| 3 | HIGH | Wizard PID lock has zero production callers |
| 4 | HIGH | Manager.run has no panic-recover |
| 5 | HIGH | Polarity probe outcome never persisted; daemon never reads it back |
| 6 | HIGH | Calibration writes bypass polarity.WritePWM |
| 7 | MEDIUM | EmitEvent exported but zero production callers |
| 8 | MEDIUM | Phase 5b transient probe errors collapse to phantom |
| 9 | MEDIUM | Manager.Start does not reset event ring |
| 10 | MEDIUM | HwmonProber zero-value wired; no ctx-aware clock |

Counts: 2 CRITICAL, 4 HIGH, 4 MEDIUM, 0 LOW.

Top finding: an NVIDIA-only or heuristic-bound host hits the Phase 5b
"all phantom" early-exit (Finding 2), and that exit path also skips
`restoreExcludedChannels` (Finding 1), so every probed hwmon channel sits at
pwm_enable=1 with the probe-end PWM byte forever — both bugs compound on the
same path.
