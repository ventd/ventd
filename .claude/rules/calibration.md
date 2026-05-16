# Calibration validity-probe rules (PR-2b)

These invariants govern the PR-2b channel-validity probe in
`internal/validity/` — the polarity probe, stall sweep,
BIOS-override detection, and on-disk calibration record format.
The validity probe answers "is this channel valid for control?"
(polarity correct? not stalling? not BIOS-overridden?) before
calibration proper runs. The boundary with `internal/calibrate/`
(legacy V-model sweep) and `internal/probe/` (catalog-less primary
path) is documented at
`docs/research/r-bundle/smart-mode-handoff.md` and pinned by
RULE-PKG-VALIDITY-PROBE-BOUNDARY (free-form note, no test).

Each rule below binds 1:1 to a subtest. If a rule text is edited,
update the binding subtest in the same PR; if a new rule lands,
it must ship with a matching subtest or `tools/rulelint` blocks
the merge.

Related family files:
- `calibration-safety.md` — zero-PWM sentinel + RPM-sensor detect rules.
- `calibrate-persist.md` — `Manager.RemapKey` migration rules.

## RULE-CALIB-PR2B-01: Polarity probe classifies normal polarity when RPM delta ≥ 200 RPM at 80% vs 20% PWM.

`ProbePolarity` writes 20% of `pwmUnitMax` to the channel,
waits `latency*3`, reads RPM as `rpmAtLow`; then writes 80%,
waits, reads `rpmAtHigh`. When `rpmAtHigh - rpmAtLow ≥ 200`,
the function returns `PolarityNormal`. A normal-polarity
classification enables the apply path to write PWM values
without inversion. A delta below the threshold is not a valid
normal-polarity classification — rounding errors, slow motor
response, or BIOS intervention can all produce a small delta on
a physically-normal fan.

Bound: internal/validity/probe_test.go:polarity_normal_detected

## RULE-CALIB-PR2B-02: Polarity probe classifies inverted polarity when rpmAtLow − rpmAtHigh ≥ 200 RPM.

`ProbePolarity` returns `PolarityInverted` when
`rpmAtLow - rpmAtHigh ≥ 200`. An inverted channel spins faster
at low PWM than at high PWM — it87 on some Gigabyte boards and
nct6683 on MSI boards exhibit this. The apply path MUST invert
duty-cycle values via `InvertPWM` before writing them to the
sysfs PWM file. Failing to invert produces the opposite of the
requested speed: asking for minimum speed drives the fan to
maximum and vice versa.

Bound: internal/validity/probe_test.go:polarity_inverted_detected
Bound: internal/validity/probe_test.go:inverted_polarity_write_inverts_value

## RULE-CALIB-PR2B-03: Polarity probe returns PolarityAmbiguous when |rpmAtHigh − rpmAtLow| < 200; channel is marked phantom.

`ProbePolarity` returns `PolarityAmbiguous` when neither the
normal nor inverted threshold is met. This outcome occurs for:
physically disconnected headers, BIOS-locked fans that ignore
manual PWM, and driver sysfs channels that map to no physical
fan (phantom channels on some Super I/O chips). The probe
orchestrator MUST mark `ChannelCalibration.Phantom = true` for
an ambiguous result so the apply path registers the channel as
monitor-only.

Bound: internal/validity/probe_test.go:phantom_marked_from_ambiguous_polarity
Bound: internal/validity/probe_test.go:phantom_write_returns_monitor_only

## RULE-CALIB-PR2B-04: Stall PWM is detected for duty_0_255 channels via a descending sweep with step size 16.

`ProbeStall` first writes `pwmUnitMax` to establish a spinning
baseline. It then descends in steps of 16 until the first step
where RPM drops to 0. The `StallPWM` field in
`ChannelCalibration` is set to the sweep value where RPM
became 0 (within one step of the true stall point). The sweep
resolution of 16 is a balance between calibration duration and
accuracy; a sweep step of 1 would take 256 writes and 128
settle-waits on a 50ms-hint driver. A NULL `StallPWM` indicates
that the fan never stalled across the full sweep
(fan-always-on hardware) or that the channel is phantom.

Bound: internal/validity/probe_test.go:stall_pwm_detected_duty_0_255

## RULE-CALIB-PR2B-05: MinResponsivePWM is set to the sweep step immediately above the detected stall point.

`ProbeStall` sets `MinResponsivePWM` to the last sweep step
where RPM > 0 just before RPM dropped to 0. This is the lowest
PWM value the apply path can use with confidence that the fan
will spin. The `MinPWM` config field for the fan is overridden
by `MinResponsivePWM` when calibration data is present,
preventing writes below the observed spin threshold. A
`MinResponsivePWM` of 0 is valid and means the fan spins at the
minimum sweep step; a NULL means phantom or the sweep never
found a transition.

Bound: internal/validity/probe_test.go:min_responsive_pwm_detected

## RULE-CALIB-PR2B-06: BIOS override is detected when the first readback matches the write but the second readback (≈200ms later) does not.

`ProbeBIOSOverride` writes `targetPWM`, reads back the register
within 50ms (`v1`), then reads again at ≈200ms (`v2`). If
`v1 == targetPWM && v2 != targetPWM`, the function returns
`overridden=true`. This pattern identifies the "writes accept
but BIOS reverts" class seen on Gigabyte boards (it8689 case
from hwmon-research.md §2.3). A channel with
`BIOSOverridden=true` is registered as monitor-only;
`CheckWrite` returns `ErrBIOSOverridden`. A fan with BIOS
override whose `v1 != targetPWM` (write silently fails) is NOT
a BIOS-override case; it is a driver capability issue.

Bound: internal/validity/probe_test.go:bios_override_detected
Bound: internal/validity/probe_test.go:bios_override_not_detected_for_normal_fan

## RULE-CALIB-PR2B-07: ShouldApplyCurve returns ErrPhantom for phantom channels; writes are unconditionally refused.

`hwdb.ShouldApplyCurve(ch *ChannelCalibration)` returns
`(false, ErrPhantom)` when `ch.Phantom == true`. The controller
apply path (`writeWithRetry`) calls `ShouldApplyCurve` before
every `backend.Write` and returns immediately on a non-nil
error — no PWM is written, no transient retry is attempted.
Phantom channels represent sysfs PWM entries with no physical
fan behind them; writing any value is a no-op at best and can
interfere with the BIOS auto-curve at worst. `ErrPhantom` is a
permanent skip condition, not a retryable error.

Bound: internal/controller/controller_test.go:TestWriteWithRetry_RefusesPhantom

## RULE-CALIB-PR2B-10: step_0_N stall detection uses binary search; convergence in ≤ ceil(log2(N+1)) + 1 samples.

`ProbeStallStep` uses binary search over the range [0, pwmUnitMax]
to find the minimum step where RPM > 0 (min_responsive_pwm).
The stall_pwm is min_responsive − 1. For an 8-level fan
(pwmUnitMax=7), binary search converges in ≤ 3 probe samples.
A linear sweep (ProbeStall) on a step_0_N fan is incorrect —
each step write and settle takes `polling_latency_hint * 3`,
and a 16-level fan would require 16 writes instead of 4.
Drivers in this category include thinkpad_acpi (levels 0..7),
dell-smm-hwmon (cooling_level 0..N), and steamdeck-hwmon
(0..255 discrete). The test verifies binary search for a
7-step fan produces the correct stall=0, min_responsive=1
result in ≤ 6 samples.

Bound: internal/validity/probe_test.go:step_0N_stall_binary_search

## RULE-CALIB-PR2B-11: CalibrationRun JSON round-trips without data loss; schema_version=1 is preserved.

A `hwdb.CalibrationRun` marshalled to JSON and unmarshalled
back must be field-for-field equal to the original:
`schema_version`, `dmi_fingerprint`, `bios_version`,
`calibrated_at`, `channels[*].channel_index`,
`channels[*].stall_pwm`, `channels[*].min_responsive_pwm`,
`channels[*].polarity_inverted`, `channels[*].phantom`,
`channels[*].bios_overridden`, and all nullable pointer
fields. The `schema_version` field MUST be included in
marshalled output (not zero-valued away) because PR 2c's
diagnostic bundle and any future migration tool depend on it
being present. A round-trip failure means the on-disk format
diverges from the in-memory struct, silently discarding
calibration data on next load.

Bound: internal/validity/probe_test.go:calibration_result_json_roundtrip

## RULE-CALIB-PR2B-12: Store.Filename produces "<dmi_fingerprint>-<bios_version_safe>.json"; non-alphanumeric chars in the BIOS version are replaced with hyphens.

`Store.Filename(fingerprint, biosVersion)` sanitises
`biosVersion` by replacing every character outside
`[a-zA-Z0-9]` with a hyphen, collapsing consecutive hyphens,
and producing the filename `<fingerprint>-<safe>.json`. BIOS
version strings from the field include spaces, slashes,
parentheses, and dots (e.g. "ASUS 0805 (04/26/2026)"); these
must not appear in filenames on any Linux filesystem. The test
fixture verifies that "ASUS 0805 (04/26/2026)" produces
"ASUS-0805-04-26-2026" as the safe component. A consistent,
predictable filename format lets `Store.Load` reconstruct the
path from the same inputs without a directory scan.

Bound: internal/validity/probe_test.go:store_filename_format
Bound: internal/validity/probe_test.go:store_write_then_load
