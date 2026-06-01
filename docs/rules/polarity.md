# Polarity probe + write-path rules

These invariants govern fan-polarity classification (the wizard's
direction-detection step) and the controller-side write helper that
applies the classification. Polarity matters because some hwmon /
NVML / IPMI channels are wired so that low duty cycle corresponds
to maximum fan speed (inverted polarity); writing a raw byte to
those channels without inversion produces the opposite of the
requested behaviour.

The probe lives in `internal/polarity/`. The control-time write
helper is `polarity.WritePWM`. The controller hot-path enforces
write-path routing through that helper in
`internal/controller/controller.go`.

Each rule below binds 1:1 to a subtest. If a rule text is edited,
update the binding subtest in the same PR; if a new rule lands, it
must ship with a matching subtest or `tools/rulelint` blocks the
merge.

## Spec divergence (POLARITY-01..05 vs POLARITY-13)

The pre-#1110 polarity algorithm was a single midpoint write (PWM
128 / NVML 50 %) compared against the pre-write baseline RPM. That
algorithm misclassified normal fans whose BIOS auto-curve held
them above midpoint at probe entry — a fan at PWM=255 / 2300 RPM
slowed to ~1500 RPM under the midpoint write, producing a negative
delta and a false "inverted" label. RULE-POLARITY-13 replaced the
midpoint write with the bipolar low/high pulse arrangement that
the validity probe (`internal/validity/`) had already proven
correct (RULE-CALIB-PR2B-01). RULE-POLARITY-01..05 pin the
specific values and timings the bipolar contract relies on; they
are subsets of RULE-POLARITY-13's broader correctness statement,
retained so a regression that changes only one knob (e.g. the hold
duration without the rest) still fails CI.

## RULE-POLARITY-01: Bipolar probe writes are exactly BipolarLowPWM then BipolarHighPWM for hwmon, BipolarLowPct then BipolarHighPct for NVML, and vendor-specific for IPMI.

`HwmonProber.ProbeChannel` writes `BipolarLowPWM` (51 ≈ 20% of
255) followed by `BipolarHighPWM` (204 ≈ 80% of 255) to
`ch.PWMPath` as the bipolar stimulus before classifying.
`NVMLProber.ProbeChannel` calls
`SetFanSpeed(gpuIdx, fanIdx, BipolarLowPct)` then
`SetFanSpeed(gpuIdx, fanIdx, BipolarHighPct)` — the NVML 20/80
pair. `DellIPMIProbe` and `SupermicroIPMIProbe` use vendor-
specific OEM write primitives (deferred to v0.7+, see
RULE-POLARITY-07).

See RULE-POLARITY-13 for the full bipolar contract; this rule
pins the two specific PWM / percentage values the probe must
write.

Bound: internal/polarity/polarity_test.go:RULE-POLARITY-01_midpoint_write
Bound: internal/polarity/polarity_test.go:hwmon_writes_bipolar_low_then_high
Bound: internal/polarity/polarity_test.go:nvml_writes_bipolar_20_then_80

## RULE-POLARITY-02: Each bipolar pulse holds for BipolarPulseHold (6s) before the tach-read window across all hwmon and NVML probes.

`HwmonProber.ProbeChannel` calls `p.clock()(BipolarPulseHold)`
after writing `BipolarLowPWM` (51) AND after writing
`BipolarHighPWM` (204), before each `readRPMMean` call that
samples the channel's RPM. The total injected sleep per probe is
at least `2 × BipolarPulseHold` (12 s) plus the post-restore
`RestoreDelay` (500 ms). The test verifies this via an injected
clock accumulator and asserts
`totalSleep >= 2 × BipolarPulseHold - 200ms`.

The pre-#1110 algorithm held for a single 3 s window after the
midpoint write. The post-#1110 bipolar replacement initially
halved each individual pulse to 2 s on the assumption that the
two-pulse arrangement provides redundant settling — a fan that
hadn't settled at the LOW read would still produce a signed delta
against the HIGH read, and the 150 RPM phantom threshold would
absorb the remaining transient.

Issue #1221 HIL on a 13900K / MSI Z690-A DDR4 / NCT6687D board
falsified that assumption for large case fans on splitter cables.
Manual sweep with `pwm_enable=1` showed:

| Channel    | pwm=255 | LOW pulse RPM @ t=2s | @ t=6s | @ t=11s |
| ---------- | ------- | -------------------- | ------ | ------- |
| pwm1 (CPU) |   2836  |             1775     |   851  |    842  |
| pwm3       |   2290  |             1628     |   851  |    845  |
| pwm6       |   2941  |             1477     |   635  |    625  |
| pwm8       |   3000  |             1593     |   720  |    657  |

Spin-down has a first-order time-constant τ ≈ 2.2 s with settling
around t ≈ 6-8 s; spin-up has τ ≈ 1.3 s. At a 2 s LOW hold, the
sampled RPM is still 1500-1700 RPM above the steady-state target
of ~660 RPM, and the bipolar delta (HIGH−LOW) collapses to 43-407
RPM — half the eight channels straddle the 150 RPM phantom
threshold giving non-deterministic 1-4 false-phantoms per run.
At a 6 s hold the same channels yield deltas of 1474-1827 RPM —
two orders of magnitude above the threshold and unambiguous.

The hold was raised from 2 s to 6 s and the sample window was
decoupled from `RestoreDelay` (500 ms) into `BipolarSampleWindow`
(1 s) so the mean averages ≥10 tach edges at low RPM where the
tach period exceeds 100 ms. Total per-channel probe time rises
from ~5 s to ~14 s; 8-channel boards see polarity-phase wall time
rise from ~40 s to ~115 s. The 200 ms tolerance accommodates
clock-sleep jitter on a loaded system.

Bound: internal/polarity/polarity_test.go:RULE-POLARITY-02_hold_envelope
Bound: internal/polarity/polarity_test.go:RULE-POLARITY-02_spindown_inertia_classifies_normal_1221

## RULE-POLARITY-03: Classification thresholds — hwmon |delta| < 150 RPM → phantom; NVML |delta| < 10 pct → phantom.

`HwmonProber.ProbeChannel` computes
`delta = observedRPM - baselineRPM`. When
`math.Abs(delta) >= ThresholdRPM (150)` and `delta > 0`,
polarity is `"normal"`. When `delta < 0`, polarity is
`"inverted"`. When `math.Abs(delta) < ThresholdRPM`, polarity is
`"phantom"` with `PhantomReason = PhantomReasonNoResponse`. The
same logic applies to `NVMLProber` using `ThresholdPct (10)` on
percentage-point deltas. A threshold below the noise floor of a
stopped or BIOS-locked fan produces false normal/inverted
classifications; 150 RPM (hwmon) and 10 pct (NVML) are
empirically derived from field data in the polarity
disambiguation research notes.

Bound: internal/polarity/polarity_test.go:RULE-POLARITY-03_threshold_boundary

## RULE-POLARITY-04: Baseline PWM is restored on every exit path — write failure, context cancel, and normal return.

`HwmonProber.ProbeChannel` captures `baselinePWM` from
`ch.PWMPath` before any write. A `defer` restores `baselinePWM`
via `p.writeFile` and waits `RestoreDelay` unless the probe has
already written the restore explicitly. The restore fires on:
(a) write failure (phantom path), (b) context cancellation
(ctx.Done() check before and after HoldDuration), and (c) normal
classification return. `NVMLProber.ProbeChannel` restores both
the fan speed and the fan control policy. A probe that exits
without restoring leaves the fan at the midpoint write value
(128/255 or 50%) indefinitely, which is incorrect and audible.

Bound: internal/polarity/polarity_test.go:RULE-POLARITY-04_restore_on_all_paths
Bound: internal/polarity/polarity_test.go:hwmon_write_fail_restores_baseline
Bound: internal/polarity/polarity_test.go:hwmon_context_cancel_restores
Bound: internal/polarity/polarity_test.go:nvml_restores_policy_and_speed

## RULE-POLARITY-05: WritePWM refuses writes to phantom channels (ErrChannelNotControllable) and unknown channels (ErrPolarityNotResolved).

`WritePWM(ch *probe.ControllableChannel, value uint8, fn func(uint8) error)`
is the polarity-aware write helper (spec §3.4). It dispatches on
a closed set:
- `"normal"` → forwards `value` to `fn` unchanged
- `"inverted"` → forwards `255-value` to `fn`
- `"phantom"` → returns `ErrChannelNotControllable` without
  calling `fn`
- `"unknown"` → returns `ErrPolarityNotResolved` without calling
  `fn`
- any other value → returns a descriptive format error

A write to a phantom channel would attempt PWM manipulation on a
channel backed by no physical fan; a write to an unknown channel
would proceed without inversion when the fan may be inverted.
Both are incorrect.

Bound: internal/polarity/polarity_test.go:RULE-POLARITY-05_write_helper_refuses_phantom_unknown

## RULE-POLARITY-06: NVML polarity probe refuses channels whose driver version is below R515 (major < 515).

`NVMLProber.ProbeChannel` calls `NVMLFuncs.DriverVersion()` and
parses the major version component (e.g. "570.211.01" → 570).
When `major < 515`, the probe returns a `ChannelResult` with
`Polarity = "phantom"` and
`PhantomReason = PhantomReasonDriverTooOld` without attempting
any fan speed write. NVML `nvmlDeviceSetFanSpeed_v2` and
`nvmlDeviceGetFanControlPolicy_v2` are available from driver
R515 onward; calling them on older drivers produces an
NVML_ERROR_FUNCTION_NOT_FOUND that cannot be cleanly recovered.

Bound: internal/polarity/polarity_test.go:RULE-POLARITY-06_nvml_driver_version_gate

## RULE-POLARITY-07: IPMI polarity probe vendor-dispatch surface is reserved for v0.7+; v0.5.39 removed the unused implementation per #1071.

**v0.5.39 deletion**. The original v0.5.x design declared an
`IPMIVendorProbe` interface in `internal/polarity/ipmi.go` with
three concrete implementations (`SupermicroIPMIProbe`,
`DellIPMIProbe`, `HPEIPMIProbe`) covering Supermicro OEM, Dell
firmware-locked, and HPE profile-only channels. Pass-2 of the
comprehensive code audit
(`(audit note in git history)`)
identified the surface as fully dead in production. v0.5.39
deleted the dead surface per option 2 from the audit
recommendation.

A v0.7+ PR that re-introduces IPMI polarity probing (wired via
the wizard PhaseGate machinery from #800) will:

1. Re-add the `IPMIVendorProbe` interface + the concrete probe
   types in a new file under `internal/polarity/` (or in a
   fresh `internal/polarity/ipmi/` subpackage).
2. Wire a real construction site in the wizard's setup phase
   that detects IPMI hardware and dispatches the appropriate
   vendor probe.
3. Re-bind RULE-POLARITY-07 to a subtest that exercises the
   wizard call site, not the vendor probes in isolation.

This rule is documentation-only in v0.5.39+. It has no bound
subtest because the surface it would constrain no longer exists.

## RULE-POLARITY-08: On daemon start, ApplyOnStart matches persisted polarity results to live channels by PWMPath; unmatched channels remain "unknown".

`ApplyOnStart(db *state.KVDB, channels []*probe.ControllableChannel, logger *slog.Logger)`
loads the `PolarityStore` from the `calibration` KV namespace and
calls `ApplyPersisted` for each live channel. `ApplyPersisted`
returns `MatchApplied` when a persisted result matches
`ch.PWMPath`, sets `ch.Polarity` and `ch.PhantomReason`, and logs
the match at INFO level. Channels with no persisted entry remain
at `Polarity="unknown"` and receive a log entry noting that a
probe is required. Orphaned persisted entries (no live channel)
are logged at INFO level but do not cause an error. A `nil` db is
a no-op.

Bound: internal/polarity/polarity_test.go:RULE-POLARITY-08_daemon_start_match

## RULE-POLARITY-09: "Reset to initial setup" wipes the calibration KV namespace atomically via WipeNamespaces.

`probe.WipeNamespaces(db *state.KVDB)` wipes the `wizard`,
`probe`, and `calibration` KV namespaces in a single
`db.WithTransaction` call (spec RULE-PROBE-09 extension). After a
successful wipe, `db.List("calibration")` MUST return an empty
map. The polarity `PolarityStore` is persisted under the
`calibration` namespace; resetting without clearing it would
leave stale polarity results from a prior installation, causing
the post-reset probe to skip re-detection for channels that
appear to have known polarity. Wiping all three namespaces
atomically ensures that the post-reset daemon start sees a clean
slate across probe, wizard, and polarity state.

Bound: internal/polarity/polarity_test.go:RULE-POLARITY-09_reset_wipes_calibration_namespace

## RULE-POLARITY-10: All phantom reason codes are writable via WritePWM and return ErrChannelNotControllable.

Every defined `PhantomReason*` constant (`no_tach`,
`no_response`, `firmware_locked`, `profile_only`,
`driver_too_old`, `write_failed`, `implausible_tach`)
represents a permanent
non-controllable state. A `ControllableChannel` with
`Polarity="phantom"` and any `PhantomReason` value MUST cause
`WritePWM` to return `ErrChannelNotControllable` without calling
`fn`. This is verified exhaustively for all reason codes so
that adding a new reason code without updating the write path
cannot silently enable writes to an uncontrollable channel. The
reason code itself is advisory (shown in the setup wizard and
`ventd doctor` output) and does not affect the write refusal
behaviour.

Bound: internal/polarity/polarity_test.go:RULE-POLARITY-10_phantom_not_writable

## RULE-POLARITY-11: Every PWM write in the controller hot path MUST route through polarity.WritePWM.

The controller's `writeWithRetry`, its 50ms-retry sub-call, and
the sentinel-carry-forward branch in `tick()` all dispatch
backend PWM writes through `c.writePWMViaPolarity(ch, pwm)`
(`internal/controller/controller.go`). The helper wraps every
write in `polarity.WritePWM(c.polarityCh, pwm, fn)` so
inverted-polarity channels receive `255-pwm` (correctly flipped)
and phantom / unknown channels are refused at the polarity
helper boundary rather than silently writing wrong-direction
bytes to sysfs.

Pre-#1037 the controller wrote the raw PWM byte direct to
`backend.Write` on all three call sites. `hwdb.InvertPWM` was
the only inversion path, and it read
`ChannelCalibration.PolarityInverted` — a field no production
code path ever set to true. The pass-6 audit traced this
end-to-end: the wizard classified inverted-polarity channels
correctly (#1026) but the classification never reached the
controller, so on inverted-polarity boards (NCT6683 on MSI,
IT87 on some Gigabyte) ventd asked for slower cooling and the
fan went faster.

When the controller has no `polarityCh` wired (nil — test
scaffolding before the channel slice is plumbed),
`writePWMViaPolarity` falls back to the unchanged byte semantics
so existing tests that don't supply a channel continue to pass.
A polarity-helper refusal (phantom / unknown) returns nil from
the controller's perspective so the tick loop continues — the
refusal is operator-visible via the WARN log line and the
wizard / doctor surface escalation, not via a tick-level error.

Bound: internal/controller/safety_test.go:polarity_inverted_routes_via_writepwm
Bound: internal/controller/safety_test.go:polarity_inverted_sentinel_carry_forward_routes_via_writepwm
Bound: internal/controller/safety_test.go:polarity_normal_passes_value_through
Bound: internal/controller/safety_test.go:polarity_unknown_refused_no_backend_write
Bound: internal/controller/safety_test.go:nil_polarity_channel_falls_through_unchanged

## RULE-POLARITY-12: On the first polarity refusal per channel lifetime, the controller MUST hand the channel back to BIOS auto via watchdog.RestoreOne; subsequent refusals are silent.

When `polarity.WritePWM` returns `ErrChannelNotControllable`
(phantom) or `ErrPolarityNotResolved` (unknown) inside
`writePWMViaPolarity`
(`internal/controller/controller.go`), the controller MUST
dispatch `c.wd.RestoreOne(c.pwmPath)` and emit a single
operator-visible WARN ("controller: polarity refused write;
handing back to BIOS auto"). The controller MUST track the
handback on a per-instance `polarityHandedBack` boolean so
subsequent refusals within the same controller lifetime are
silent skips — no further WARN emission, no further watchdog
dispatch.

Pre-#1110 the controller logged the refusal and returned nil.
The fan sat at whatever PWM the last successful write committed
— most commonly the calibration sweep's final write of PWM=0.
For a non-pump fan that's a loud failure mode; for an AIO pump
on an inverted-polarity-misclassified channel it's a thermal
disaster. Closes the 2026-05-15 incident on Phoenix's 13900K
box where every NCT6687-controlled channel sat at PWM=0 for
nearly an hour because the wizard's polarity probe misclassified
fans whose BIOS auto-curve held them at high baseline PWM going
into the midpoint test (the root-cause classification bug is
addressed separately by RULE-POLARITY-13's bipolar probe).

The handback is one-shot per controller. A config reload
(SIGHUP) or daemon restart spawns a fresh controller whose flag
starts false again — re-probe + re-classification on the next
wizard run can promote a previously-refused channel back into
the control path without code changes here. The watchdog's
`RestoreOne` is the canonical handback primitive: it dispatches
through `restoreOne` which honours the chip-specific
`pwm_enable` fallback chain (RULE-HWMON-ENABLE-EINVAL-FALLBACK)
and the per-entry panic envelope (RULE-WD-RESTORE-PANIC). A nil
watchdog (test scaffolding) skips the dispatch cleanly.

Bound: internal/controller/safety_test.go:polarity_refused_phantom_hands_back_to_bios_auto_once

## RULE-POLARITY-13: Hwmon and NVML polarity probes MUST classify on a bipolar low/high pulse delta; baseline PWM influences restore-only, never classification.

`HwmonProber.ProbeChannel` and `NVMLProber.ProbeChannel` MUST
drive the channel through two PWM/speed pulses and compare the
observed RPM/speed between the two —
`delta = response_high − response_low`. The two pulses are:

- **hwmon**: `BipolarLowPWM` (51 ≈ 20% of 255) and
  `BipolarHighPWM` (204 ≈ 80% of 255), each held for
  `BipolarPulseHold` (2 s) before the 500 ms tach-read window.
- **NVML**: `BipolarLowPct` (20) and `BipolarHighPct` (80),
  same hold envelope. The GPU's existing fan-control policy is
  set to manual / temperature-discrete BEFORE the LOW pulse
  and restored on every exit path.

Classification:

- `|delta| < ThresholdRPM` (150 RPM) / `ThresholdPct` (10 %) →
  `phantom` with `PhantomReasonNoResponse`.
- `delta > 0` → `normal`.
- `delta < 0` → `inverted`.

Baseline PWM (read once before the LOW pulse) is captured for
the deferred restore in RULE-POLARITY-04 ONLY. Baseline RPM is
never read or used in classification — the pre-#1110 algorithm
read baseline RPM and compared a single midpoint write
(128 / 50%) against it, which misclassified every normal fan
whose baseline PWM was above midpoint: a fan held at PWM=255 /
2300 RPM by BIOS auto slowed to ~1500 RPM under PWM=128,
producing `delta = -800` and a false-inverted label. Closed the
2026-05-15 incident on Phoenix's 13900K / NCT6687 box where six
of seven controlled channels landed in that misclassification.

The bipolar test mirrors `internal/validity/`'s 20%/80% probe
pattern (RULE-CALIB-PR2B-01) so the two calibration-adjacent
surfaces converge on the same correct algorithm. The two probes
remain separate per RULE-PKG-VALIDITY-PROBE-BOUNDARY: polarity
probes for control-time direction; validity probes for
channel-controllability gating.

A `0` baseline-PWM read failure falls back to `128` for
restore-only purposes (the restore byte must be a valid uint8).
Context cancellation between pulses returns `ctx.Err()` via the
existing exit-path defer, preserving the RULE-POLARITY-04
restore contract.

Bound: internal/polarity/polarity_test.go:RULE-POLARITY-13_bipolar_baseline_invariant
Bound: internal/polarity/polarity_test.go:hwmon_normal_fan_high_baseline_classifies_normal
Bound: internal/polarity/polarity_test.go:hwmon_inverted_fan_low_baseline_classifies_inverted

## RULE-POLARITY-14: A probe pulse with no plausible tach sample classifies the channel phantom; a driver sentinel is never read as a real RPM.

`HwmonProber.readRPMMean` MUST drop tach samples that are driver
sentinels or above the plausibility ceiling
(`hal/hwmon.IsSentinelRPM` — `0xFFFF` / `> 25000` RPM) rather than
averaging them into the window mean, and MUST report whether the
window produced any plausible sample. When either bipolar pulse
yields no plausible sample (the tach was unreadable, or every
reading was a sentinel) the channel's tach cannot be trusted:
`ProbeChannel` MUST classify it `phantom` with
`PhantomReasonImplausibleTach` and MUST NOT derive a direction
from the corrupted delta.

Without the guard a fan whose tach reports the `0xFFFF` sentinel
at one probe point — an intermittent glitch common on super-I/O
chips at high RPM — produces `delta = 65535 − RPM_low ≈ +64000`
and is misclassified `normal` (or, with the sentinel at only one
pulse, sign-flipped to `inverted`), with the implausible 65535
RPM stored on the result and carried into calibration and the UI.
Because the polarity probe runs first during setup and gates
controllability, this admitted an untrustworthy channel under a
fabricated verdict. The guard mirrors the calibration sweep's
existing `IsSentinelRPM` rejection (`internal/calibrate`) and the
runtime `hal/hwmon` read path, so all three tach consumers agree.

A genuine `0` RPM (a stopped fan) is a plausible reading and is
NOT dropped: a phantom fan still classifies via the
RULE-POLARITY-03 zero-delta path with `PhantomReasonNoResponse`,
distinct from `implausible_tach`.

Bound: internal/polarity/polarity_test.go:RULE-POLARITY-14_implausible_tach_phantom
