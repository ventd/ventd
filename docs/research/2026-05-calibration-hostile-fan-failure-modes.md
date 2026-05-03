# Calibration safety — hostile-fan failure modes

Captured 2026-05-02 from a research-agent pass. The current calibration safety set
(`RULE-ENVELOPE-14` PWM readback divergence, `ZeroPWMSentinel` 2 s escalation) catches
a subset of hostile-fan behaviour. This doc enumerates eight failure-mode classes the
agent identified that may need new rule bindings before ventd's catalog is robust on
real-world hostile hardware.

**Status: research only — no implementation PRs landing without review.**

## 1. Sticky fans (rotor stiction)

**Signal:** `fan*_input` reports a flat constant (last spinning RPM) then drops to 0,
or oscillates 0 ↔ ~300 RPM as the rotor twitches but cannot complete a revolution.
Time horizon is bimodal: clean bearings free in 5–30 s of full PWM; gummed sleeve
bearings (lubricant polymerised) may need minutes or never recover without manual
spin. *thinkfan #58 / X380 Yoga case showed RPM stuck at one value across all
levels until physical intervention.*

**Detection:** during low-PWM portion of sweep, RPM is non-zero but
`stddev(rpm) < 1` over a 3 s window with PWM in motion. Real spinning fans always
jitter ±30–60 RPM.

**Recovery:** spin-up pulse (PWM=255 for 4 s, then resume sweep). If RPM still flat
→ abort, mark fan `degraded`, hand back to firmware, surface to user.

**Proposed rule:** `RULE-STICTION-15`.

## 2. Non-monotonic PWM→RPM (Smart-Fan dual-zone)

**Signal:** RPM rises with PWM to a peak then *decreases* as PWM increases further —
EC reinterpreting PWM as a duty for a second curve, or PWM coupling into voltage
regulation on shared headers. Visible in fan2go #26.

**Detection:** monotonicity check during sweep; >1 reversal beyond hysteresis band
⇒ non-monotonic.

**Recovery:** abort, refuse to install learned curve, recommend disabling Smart Fan
in BIOS.

**Proposed rule:** `RULE-MONOTONICITY-16`.

## 3. Hysteresis (start_pwm ≠ stall_pwm)

**Signal:** sweep low→high finds `start_pwm ≈ 80`; sweep high→low finds
`stall_pwm ≈ 20`. Both are real and intrinsic to brushless motor cogging.

**Distinguishing from real stall threshold:** bidirectional sweep — record both,
store separately. At runtime ramp to `start_pwm` on transition out of stop, settle
to `stall_pwm + margin`.

**Recovery:** safe — this is the fan's normal physics. Failure mode is *not*
recording both.

**Proposed rule:** `RULE-HYSTERESIS-17` (informational).

## 4. BIOS overrides mid-sweep — patterns RULE-ENVELOPE-14 misses

Phoenix's MAG case is the well-known one. Patterns commonly missed by a pure
write/readback diff:

- **Range-selective override.** EC respects PWM 80–255 but slams 0–79 to a floor
  (e.g. 96). Readback at PWM=50 returns 50 (register accepts) but the **PWM
  output pin** is at 96 because EC overlays a min-duty register. Detection requires
  *RPM-vs-expected*, not register readback.
- **Time-delayed revert.** EC writes back to its preferred value after 3–10 s.
  RULE-ENVELOPE-14 may sample inside that window. Mitigation: re-read PWM at t+1,
  t+5, t+15 and require all three to match.
- **Temperature-triggered override.** EC override engages only when a thermal
  threshold is crossed — and your sweep itself triggers it (see #8).
- **`pwm_enable` resets.** EC silently resets `pwm[N]_enable` to 2 (auto). Re-read
  `pwm_enable` every sample, not just `pwm`.

Reference: fan2go #64 "PWM was changed by third party".

**Recovery:** abort sweep, mark zone `firmware-managed`, refuse manual control
until user disables Smart Fan / Q-Fan in BIOS.

**Proposed rules:** `RULE-ENVELOPE-14b/c/d` (extensions).

## 5. 3-pin fan with dummy / synthesised tach

**Signal:** RPM reports a constant (often 1200, 1500, or a value derived from a
voltage divider on the 3-pin header) regardless of PWM. Distinct from stiction in
that variance is artificially zero AND magnitude is implausibly high at PWM=0.

**Detection:** at PWM=0 held >2 s (existing ZeroPWMSentinel range), if RPM > a few
hundred, the tach is fake. Real fans coast down.

**Recovery:** safe — mark fan as "RPM-blind", calibrate by acoustic/thermal proxy
or fall back to fixed-point control without RPM feedback.

**Proposed rule:** `RULE-DUMMYTACH-18`.

## 6. PWM noise floor / quantisation plateau

**Signal:** RPM identical across a PWM band (e.g. 30–60%) then jumps. Common on
EC-mediated boards that quantise PWM to 4–8 levels internally. Distinguishable
from stiction: variance is normal (±30 RPM jitter), only the *mean* is constant.

**Recovery:** safe, store as plateau in curve.

**Proposed rule:** `RULE-QUANT-19` (informational).

## 7. AIO pump stalling / convection masking

**Signal:** at low PWM, pump RPM=0 *but coolant temp does not rise immediately*
because thermal convection sustains some flow. Sweep may classify low PWM as safe
(no thermal alarm) and persist a curve that allows pump stop. CoolerControl docs
warn explicitly never to drop pump below ~60%.

**Detection heuristic:** if device reports as a pump (label / liquidctl / heuristic
— header named `AIO_PUMP`, or RPM range 1500–3500 typical pump), enforce a hard
floor of PWM=60% during AND after calibration.

**Recovery:** abort if pump-class fan stalls during sweep; surface as critical.

**Proposed rule:** `RULE-PUMPFLOOR-20`.

## 8. Thermal throttling during sweep

**Signal:** during low-PWM phase, CPU/GPU package temp rises, kernel asserts
`thermal_throttle`. Throttling reduces heat output → fan needs less duty →
calibration learns an artificially low `stall_pwm`. Next time the system is under
real load, that PWM won't keep up.

**Detection:** sample `thermal_zone*/temp` every sweep step. Abort if any zone
exceeds 85 °C OR if any zone shows throttling flag set.

**Recovery:** abort, return to safe curve, retry with sweep parallelism reduced
(one zone at a time, others held at full).

**Proposed rule:** `RULE-THERMABORT-21`.

## Summary table

| # | Detection signal | ventd action | Proposed rule |
|---|------------------|-------------|---------------|
| 1 | stddev(RPM)<1 over 3 s, PWM moving | spin-up pulse, then abort | `RULE-STICTION-15` |
| 2 | dRPM/dPWM reversal beyond hysteresis | abort, refuse curve | `RULE-MONOTONICITY-16` |
| 3 | start≠stall on bidirectional sweep | record both | `RULE-HYSTERESIS-17` (info) |
| 4 | range-selective / delayed readback drift | abort, mark firmware-owned | `RULE-ENVELOPE-14b/c/d` |
| 5 | RPM constant AND >0 at PWM=0 | mark RPM-blind | `RULE-DUMMYTACH-18` |
| 6 | mean flat, stddev normal | record plateau | `RULE-QUANT-19` (info) |
| 7 | pump-class + RPM=0 | abort critical | `RULE-PUMPFLOOR-20` |
| 8 | thermal_zone>85 °C or throttle flag | abort + retry serial | `RULE-THERMABORT-21` |

## Sources

- fan2go #324, #201, #64, #26, #63
- fan2go README — calibration ~8.5 min, settling
- thinkfan #114, #58
- CoolerControl FAQ — BIOS interference, full-speed BIOS recommendation
- CoolerControl hardware support — pwm_enable absent devices
- Kernel nct6775 driver doc — Smart Fan III/IV modes
- Kernel it87 driver doc
- Arch Wiki Fan speed control — stall vs start-up signal hysteresis
- Wejn — controlling fan speed correctly
- Anandtech — AIO pump not registering, EC override behaviour
- FanControl.ADLX #40 — Zero-RPM mode toggle requirement
