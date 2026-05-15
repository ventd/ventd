# RULE-POLARITY-13: Hwmon and NVML polarity probes MUST classify on a bipolar low/high pulse delta; baseline PWM influences restore-only, never classification.

`HwmonProber.ProbeChannel` and `NVMLProber.ProbeChannel` MUST drive the
channel through two PWM/speed pulses and compare the observed RPM/speed
between the two — `delta = response_high − response_low`. The two pulses
are:

- **hwmon**: `BipolarLowPWM` (51 ≈ 20% of 255) and `BipolarHighPWM`
  (204 ≈ 80% of 255), each held for `BipolarPulseHold` (2 s) before the
  500 ms tach-read window.
- **NVML**: `BipolarLowPct` (20) and `BipolarHighPct` (80), same hold
  envelope. The GPU's existing fan-control policy is set to manual /
  temperature-discrete BEFORE the LOW pulse and restored on every exit
  path.

Classification:

- `|delta| < ThresholdRPM` (150 RPM) / `ThresholdPct` (10 %) → `phantom`
  with `PhantomReasonNoResponse`.
- `delta > 0` → `normal`.
- `delta < 0` → `inverted`.

Baseline PWM (read once before the LOW pulse) is captured for the
deferred restore in `RULE-POLARITY-04` ONLY. Baseline RPM is never read
or used in classification — the pre-#1110 algorithm read baseline RPM
and compared a single midpoint write (128 / 50%) against it, which
misclassified every normal fan whose baseline PWM was above midpoint:
a fan held at PWM=255 / 2300 RPM by BIOS auto slowed to ~1500 RPM under
PWM=128, producing `delta = -800` and a false-inverted label. Closed
the 2026-05-15 incident on Phoenix's 13900K / NCT6687 box where six of
seven controlled channels landed in that misclassification.

The bipolar test mirrors `internal/validity/`'s 20%/80% probe pattern
(RULE-CALIB-PR2B-01) so the two calibration-adjacent surfaces converge
on the same correct algorithm. The two probes remain separate per
`RULE-PKG-VALIDITY-PROBE-BOUNDARY` — polarity probes for control-time
direction; validity probes for channel-controllability gating.

A `0` baseline-PWM read failure falls back to `128` for restore-only
purposes (the restore byte must be a valid uint8). Context cancellation
between pulses returns `ctx.Err()` via the existing exit-path defer,
preserving the `RULE-POLARITY-04` restore contract.

Bound: internal/polarity/polarity_test.go:TestPolarityRules/RULE-POLARITY-13_bipolar_baseline_invariant
