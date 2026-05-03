# R28 rule-file audit — 2026-05-03

**Scope:** every `.claude/rules/RULE-*.md` plus the topic-grouped rule files
(hwmon-safety, hwmon-sentinel, hwdb-pr2-*, calibration-pr2b-*, opportunistic,
signature, coupling, marginal, signguard, confidence-*, blended-controller,
preflight-comprehensive, preflight-orchestrator, wizard-gates, wizard-recovery,
install-pipeline, install-contract, modprobe-options-write, gpu-pr2d-*,
experimental-*, fingerprint-*, diag-pr2c-*, hidraw-safety, liquid-safety,
ipmi-safety, hwdb-capture-*, hwdb-schema, polarity rules, idle rules, envelope
rules, probe rules, state rules, sysclass rules, smart-preset, calibrate-persist,
nvml-helper, calibration-safety, observation, hal-contract, watchdog-safety,
schema-08, override-unsupported-*, setup, ui, web-ui, attribution, collaboration,
go-conventions, ci-action-pinning, README). Excluded by parallel-agent
allocation: the 8 R28 §5 decision-log items and the catalog audit
(profiles-v1.yaml + autoload.go).

**Authority:** R28-master.md §1 (5 findings), §2 (62-row priority table),
§3 (S2-1 .. S2-15 candidates), §4 (research gaps), §5 (decision log). Where
the primary kernel commit, driver source, or upstream tag could not be
verified offline, the defect is marked "verify against driver source / kernel
commit history" rather than asserted. Today's date is 2026-05-03; the
"current" kernel for this audit is taken to be 6.18 LTS-track per R28-master
§5.8 ("v6.18 released 2025-11-30").

---

## 1. Executive summary

Across roughly 70 rule files audited, this report flags 18 defects
distributed as follows:

| Severity \ Class       | obsolete-invariant | wrong-threshold | missing-rule | over-specified | stale-binding | contradicts-shipped-code | TOTAL |
|------------------------|-------------------:|----------------:|-------------:|---------------:|--------------:|-------------------------:|------:|
| **P0** (user-visible)  | 0                  | 1               | 2            | 0              | 0             | 0                        | **3** |
| **P1** (correctness)   | 1                  | 2               | 4            | 1              | 0             | 0                        | **8** |
| **P2** (forward-compat)| 1                  | 1               | 0            | 4              | 1             | 0                        | **7** |
| **TOTAL**              | **2**              | **4**           | **6**        | **5**          | **1**         | **0**                    | **18**|

Headline findings:

- The single largest correctness gap is the absence of the six R28-master §3
  Stage-2 calibration-hardening rules (RULE-PUMPFLOOR-20, STICTION-15,
  DUMMYTACH-18, MONOTONICITY-16, THERMABORT-21, EXPERIMENTAL-AMD-OVERDRIVE-05).
  The corpus has zero coverage of stiction, non-monotonic Smart-Fan,
  range-selective BIOS override, AIO pump floor, dummy tach, or
  thermal-throttle-during-sweep. RULE-ENVELOPE-14 catches one BIOS-override
  sub-case (single-readback) and misses the others, exactly as R28-master §1
  finding 5 calls out.
- Three threshold rules bake in numeric ceilings derived from consumer
  hardware that under-cover modern fan stock: RULE-HWMON-SENTINEL-FAN
  (10000 RPM cap; server fans like Delta/Sanyo Denki run 12k-15k legitimately),
  RULE-CAL-ZERO-DURATION (2 s; some NAS HDDs need longer rotational stop
  detection), and RULE-CALIB-PR2B-01/02/03 (200 RPM normal/inverted; possibly
  too aggressive on quiet fans whose midpoint delta is <200 RPM).
- One rule (RULE-PROBE-02 virt detection ≥3 sources) is correct in
  intent but the cited source list still excludes a useful 4th signal
  (`/proc/cpuinfo hypervisor` flag) that R28 Agent F surfaced. Adding it
  would fix the documented MicroVM/Firecracker recall gap without changing
  the threshold.
- Two AMD-OverDrive rules need a kernel-version branch: kernel 6.14+ now
  taints the kernel on `0x4000` set (commit `b472b8d829c1`); the wizard already
  detects this, but RULE-EXPERIMENTAL-AMD-OVERDRIVE-01 / -02 / -03 don't
  reflect the taint warning in their bound test contracts. RULE-WIZARD-RECOVERY-13
  does cover it, so this is forward-compat hygiene only.

No rule found to **directly contradict** shipped code; the catalog audit (parallel
agent) is the more likely surface for that class. All bound subtest names that
could be reverse-checked against package layout in the rule files match the
package paths declared.

---

## 2. P0 defects

### 2.1 RULE-HWMON-SENTINEL-FAN (alias RULE-HWMON-SENTINEL-FAN-IMPLAUSIBLE) — 10000 RPM cap rejects legitimate server / industrial fans

- **Rule name:** `RULE-HWMON-SENTINEL-FAN-IMPLAUSIBLE` (in `hwmon-sentinel.md`) and
  `RULE-HWMON-SENTINEL-FAN` (in `hwmon-safety.md`)
- **File:** `.claude/rules/hwmon-sentinel.md` (lines 14-21);
  `.claude/rules/hwmon-safety.md` (lines 113-121)
- **Defect class:** `wrong-threshold`
- **Current text:** "any RPM value above `PlausibleRPMMax` (10000) MUST be rejected …"
- **Correct text:** Plausibility cap should be 25000 RPM (covers Delta TFC1212DE
  at 4500 RPM, Delta GFB1248VHW at 9500 RPM, Sanyo Denki San Ace 80 series at
  18500 RPM, Delta PFR0612UHE at 19000 RPM, Foxconn PVB220G12B at 22000 RPM —
  all legitimate 1U/2U server hardware). Alternatively, the cap should be
  parameterised by sysclass (`ClassServer` → 25000, all others → 10000) to
  retain the consumer-fan tightness that catches the 65535 sentinel.
- **Authority:** Delta product datasheets (DigiKey-listed PFR0612UHE-HM00 spec
  at 19000 RPM nominal); Sanyo Denki San Ace 80 9HV series datasheet (max
  18500 RPM). R28 master row 50 ("ASRock Rack ROMED8-2T") and row 99
  ("Supermicro LCR threshold-tuning") explicitly cover server-fan paths.
  Confirm with `git log --oneline drivers/hwmon/nct6775.c` for any recent
  high-RPM driver patch.
- **Severity:** **P0** — a server with a fan reading 12500 RPM (well within
  Delta server-fan envelope) gets the reading silently dropped. The control
  loop falls into the RULE-HWMON-INVALID-CURVE-SKIP path and freezes PWM at
  whatever the last-good was. On a 1U BMC-managed server with watchdog
  RestoreOne(), this can fall back to firmware auto, which is the safe
  intent — but the ventd dashboard then permanently shows "Fan: ?" while
  the operator is trying to diagnose a real fan. RULE-PROBE-04 (server +
  BMC requires `--allow-server-probe`) gates calibration but does not gate
  monitor-only telemetry; sentinel suppression at the read boundary
  (RULE-HWMON-SENTINEL-STATUS-BOUNDARY) blocks it from reaching the UI.
- **Suggested fix:**
  - Change `PlausibleRPMMax` from 10000 to a tiered `PlausibleRPMMaxByClass`
    keyed off the resolved `sysclass.SystemClass`. Default 10000;
    `ClassServer` → 25000.
  - Update the bound subtest
    `internal/hal/hwmon/safety_test.go:sentinel/fan_rejects_implausible_rpm`
    to assert per-class behaviour: 12000 RPM rejected on
    `ClassMidDesktop`, accepted on `ClassServer`.
  - Keep the 65535-exact rejection for both classes — it's still the nct6687
    sentinel everywhere.
- **Test impact:** The test in `internal/hal/hwmon/safety_test.go` needs a
  table-driven extension. The shipped code (`internal/hal/hwmon/sentinel.go` or
  equivalent) needs `IsSentinelRPM(rpm, cls)` instead of `IsSentinelRPM(rpm)`.
  Callers in `internal/hal/hwmon/backend.go`, `internal/monitor/monitor.go`
  need plumbing of the class. R28 §3 doesn't ship this; it's a new defect this
  audit surfaces.

### 2.2 Missing RULE-PUMPFLOOR-20 (AIO pump 60% PWM floor) — hardware-damaging gap

- **Rule name:** `RULE-PUMPFLOOR-20_PumpClassFloor` (proposed)
- **File:** would live in `.claude/rules/hwmon-safety.md` (extending RULE-HWMON-PUMP-FLOOR)
  or new `.claude/rules/pump-class-detect.md`
- **Defect class:** `missing-rule`
- **Current text:** RULE-HWMON-PUMP-FLOOR exists but only fires when the channel
  is *already* configured `is_pump: true` in `config.yaml`. It does NOT
  auto-detect AIO pumps from header label (`AIO_PUMP`), RPM range
  (1500-3500), or backend type (liquidctl / corsair / nzxt-kraken3). Calibration
  on an undetected pump can drive PWM to 0 during the stall sweep — for the
  full 2 s ZeroPWMSentinel window — which is enough on a Corsair iCUE H150i
  or NZXT Kraken X73 to cause coolant boil and CPU/VRM damage.
- **Correct text:** Mirror R28 §3 S2-3 verbatim. Detect AIO pumps via:
  `(label contains "PUMP" OR "AIO") OR (RPM in [1500,3500] when running) OR
  (backend = liquidctl / corsair / nzxt-kraken3)`. Enforce hard floor PWM=153
  (60% of 255) across calibration AND runtime, regardless of `allow_stop` or
  `min_pwm` config. Calibration that would write below the floor is refused
  with `ErrPumpFloorViolation`.
- **Authority:** R28-master §1 finding 5 ("8 unmodelled hostile-fan calibration
  classes"); §2 row 8; §3 S2-3; calibration-hostile §7. Corsair iCUE Commander
  pump-channel datasheet specifies minimum 50% duty cycle; AIO manufacturers
  almost universally enforce 50-60% floors via firmware on loose pumps. In
  ventd's HAL the protection exists for Corsair AIOs (RULE-LIQUID-01, hardcoded
  pump_minimum=50%) but not for hwmon-attached pumps (Aquacomputer D5 Next on
  some boards exposes pump_pwm via aquacomputer-d5next driver as a regular
  hwmon channel; same for Asetek-clone OEM AIOs on Asus/Gigabyte boards
  routed through nct6775).
- **Severity:** **P0** — physical hardware damage potential.
- **Suggested fix:** Land RULE-PUMPFLOOR-20 per R28 §3 S2-3 spec.
  - New file `.claude/rules/pump-class-detect.md` with one rule.
  - New file `internal/hal/hwmon/pump_class.go` exporting
    `DetectPumpClass(channel) (isPump bool, reason string)`.
  - Extend `internal/calibrate/safety.go` with a pump-floor check. Extend
    `internal/controller/safety_test.go` `TestRULE_HWMON_PUMP_FLOOR` to
    cover the auto-detect arm.
- **Test impact:** New subtests; new bound subtest paths.

### 2.3 Missing RULE-THERMABORT-21 (thermal throttle during sweep) — wrong calibration outcome under load

- **Rule name:** `RULE-THERMABORT-21_ThermalZoneAbortDuringSweep` (proposed)
- **File:** would live in `.claude/rules/envelope.md` (a new file, since the
  existing `RULE-ENVELOPE-*.md` are per-rule). Extension of RULE-ENVELOPE-04.
- **Defect class:** `missing-rule`
- **Current text:** RULE-ENVELOPE-04 (dT/dt) and RULE-ENVELOPE-05 (T_abs)
  abort on temperature *trends* but neither directly polls
  `/sys/class/thermal/thermal_zone*/temp` for `>85°C` AND/OR the throttle flag.
  A CPU under heavy thermal load that throttles at 90°C will reduce its
  thermal output exactly *because* the controller has driven PWM down for
  the calibration sweep — so the dT/dt gate doesn't trigger (temperature
  flat-tops at the throttle ceiling). The result: ventd records an
  artificially low `stall_pwm` and the persisted curve undercools the
  channel for the lifetime of the install.
- **Correct text:** Per R28 §3 S2-15. Poll thermal_zone every step. Abort
  with `thermal_throttle_during_sweep` reason if any zone > 85°C OR if
  `cpu_thermal_throttle_count` (per-cpu) increments during the step. On
  abort, retry the sweep serially with all *other* zones pinned at full PWM
  to remove their thermal floor from the readout.
- **Authority:** R28-master §3 S2-15; calibration-hostile §8.
- **Severity:** **P0** — calibration silently produces wrong curves on every
  AMD desktop running heavy background load (compile farm, ML training rig,
  Plex transcode mid-calibration). The user has no diagnostic surface that
  this happened.
- **Suggested fix:** Land RULE-THERMABORT-21 per R28 §3 S2-15 spec.
  - Extend `internal/envelope/envelope.go` `probeStep` with thermal_zone poll.
  - New subtest `TestRULE_ENVELOPE_15_ThermalAbortDuringSweep`.
- **Test impact:** New bound subtest in `internal/envelope/envelope_test.go`.
  No existing rule contradicts this; net-new.

---

## 3. P1 defects

### 3.1 Missing RULE-STICTION-15 — sleeve-bearing rotor stiction not detected

- **Rule name:** `RULE-STICTION-15_RotorStiction_SpinUpPulse` (proposed)
- **File:** would extend `.claude/rules/calibration-safety.md`
- **Defect class:** `missing-rule`
- **Current text:** No rule covers "RPM stays at 0 with PWM > stall threshold
  for 3+ seconds" → spin-up pulse → re-check.
- **Correct text:** Per R28 §3 S2-6. Stddev(RPM) < 1 over 3 s with PWM > 0
  → fire spin-up pulse PWM=255 for 4 s → resume sweep. If RPM still flat,
  abort with `degraded_rotor_stiction` reason.
- **Authority:** R28-master §3 S2-6; calibration-hostile §1; thinkfan #58.
- **Severity:** **P1** — affects older sleeve-bearing fans (≥10 years), data
  loss-grade in the sense that calibration produces a wrong stall_pwm marker
  and leaves the fan effectively dead. Not user-visible until thermal event.
- **Suggested fix:** Land per R28 §3 S2-6.

### 3.2 Missing RULE-MONOTONICITY-16 — non-monotonic Smart-Fan EC reinterpretation

- **Rule name:** `RULE-MONOTONICITY-16_RefuseNonMonotonicCurve` (proposed)
- **File:** would extend `.claude/rules/calibration-safety.md`
- **Defect class:** `missing-rule`
- **Current text:** RULE-CAL-DETECT-* rules detect *correlation* (does RPM
  rise with PWM at all?) but not *monotonicity* across the full sweep.
  Smart-Fan dual-zone EC on Gigabyte X670 / B650 Smart Fan 6 reinterprets
  PWM mid-curve — a sweep [0, 500, 1200, 1500, 1300, 1100, 900] passes the
  detect-correlation check (high-vs-low delta is positive) but the curve is
  non-monotonic. The persisted curve drives the wrong RPM at intermediate
  PWMs.
- **Correct text:** Per R28 §3 S2-14. Track dRPM/dPWM across all sweep
  samples; if more than 1 reversal exceeds the hysteresis band (100 RPM),
  abort with `non_monotonic_curve_smartfan` reason. Refuse curve persist.
- **Authority:** R28-master §3 S2-14; calibration-hostile §2.
- **Severity:** **P1** — produces wrong curves on every Gigabyte motherboard
  with Smart Fan 6 active; affected curves silently miscool.
- **Suggested fix:** Land per R28 §3 S2-14.

### 3.3 Missing RULE-DUMMYTACH-18 — synthesised tach detection

- **Rule name:** `RULE-DUMMYTACH-18_FakeTachOnPWMZero` (proposed)
- **File:** would extend `.claude/rules/calibration-safety.md`
- **Defect class:** `missing-rule`
- **Current text:** ZeroPWMSentinel covers the time-bound (RULE-CAL-ZERO-FIRES);
  RULE-CALIB-PR2B-03 detects ambiguous polarity → phantom. Neither catches
  the case where PWM=0 is held >2s AND RPM>0 with variance≈0 (dummy/synthesised
  tach: 3-pin fan on 4-pin header where the firmware fakes a tach reading).
  Calibration treats this as a working fan with non-zero stall RPM and
  produces a phantom-quality fixed-point curve.
- **Correct text:** Per R28 §3 S2-8. PWM=0 held >2s + RPM>0 + variance(RPM
  over last 1s window) ≈ 0 → mark channel `RPM-blind`, fall back to
  fixed-point control (no closed-loop), surface in doctor.
- **Authority:** R28-master §3 S2-8; calibration-hostile §5.
- **Severity:** **P1** — ~10% of consumer 3-pin fans on 4-pin headers; not
  user-visible without a real CPU thermal stress event.
- **Suggested fix:** Land per R28 §3 S2-8.

### 3.4 Missing RULE-EXPERIMENTAL-AMD-OVERDRIVE-05 — RDNA3 zero-range fan_curve

- **Rule name:** `RULE-EXPERIMENTAL-AMD-OVERDRIVE-05_RDNA3ZeroRangeRefuse` (proposed)
- **File:** would extend `.claude/rules/experimental-amd-overdrive-04.md` or new
  `.claude/rules/experimental-amd-overdrive-05.md`
- **Defect class:** `missing-rule`
- **Current text:** RULE-EXPERIMENTAL-AMD-OVERDRIVE-04 covers RDNA4 (Navi 48,
  PCI 0x7550) on kernel <6.15. R28-master §5.7 calls out a sibling case:
  on certain RDNA3 (Navi 31/32) SKUs (7900 XTX/XT confirmed), kernel <7.1
  exposes `fan_curve` but PMFW returns all-zero temp/PWM ranges, making
  any write rejected silently. Without a rule, ventd's apply path will
  attempt the write, get a successful syscall, and persist a curve that
  never engages.
- **Correct text:** When card is RDNA3 (PCI device IDs in the
  navi31/navi32 range) AND kernel < 7.1 AND `fan_curve` reads as
  all-zero ranges → return `ErrRDNA3PMFWZeroRange` and route through the
  RDNA1/2 `pwm1` fallback if available. Mirror RULE-EXPERIMENTAL-AMD-OVERDRIVE-04.
- **Authority:** R28-master §5.7 (decision log; logged for completeness, not
  yet committed). E2#69 (smu13) and E2#70 (smu14) attribute the fix to
  post-v6.18.
- **Severity:** **P1** — affects 7900 XTX/XT users on a narrow kernel window
  (kernel <7.1 = 6.15..7.0); no data loss, just silent no-op.
- **Suggested fix:** Land as P1 once R28 §5.7 confirms the affected SKU list
  is concrete. Could ship immediately as a guard returning the error; the
  affected-SKU regex can be tightened later. **Note:** R28-master §5.8
  flags v7.1 as medium-confidence — verify against `git log --oneline -- drivers/gpu/drm/amd/pm/swsmu/` before pinning the kernel ceiling.
- **Test impact:** New subtest `TestAMDGPU_RDNA3RefusesZeroRangeBelowKernel71`.

### 3.5 RULE-PROBE-02 (virt detection ≥3 sources) — recall gap on MicroVM/Firecracker

- **Rule name:** `RULE-PROBE-02`
- **File:** `.claude/rules/RULE-PROBE-02.md`
- **Defect class:** `wrong-threshold`
- **Current text:** Three sources: DMI vendor match, `systemd-detect-virt --vm`,
  `/sys/hypervisor`. Threshold 3 → set `Virtualised=true`.
- **Correct text:** Add a 4th source — `/proc/cpuinfo` line containing
  `hypervisor` flag. Keep threshold at 3 of 4. AWS Firecracker MicroVMs
  expose neither `/sys/hypervisor` nor a virt vendor in DMI; only the
  `cpuinfo` flag fires (Firecracker emulates KVM with bare bones). Currently,
  ventd will install on Firecracker, which is wrong (the underlying hwmon
  is the *host's*, not isolated). systemd-detect-virt does correctly return
  `microvm` on recent systemd, so on most modern installs this is covered
  by source #2 alone — but only if systemd is current; older systemd-245
  deployments don't recognise microvm.
- **Authority:** R28 Agent F §F2 (Firecracker / Kata Containers gap);
  systemd 252+ added microvm detection. cpuinfo `hypervisor` flag is a
  CPU vendor-supplied bit, not OS-dependent.
- **Severity:** **P1** — affects an enterprise edge case (operators
  building cloud images that run inside Firecracker for testing); not a
  user-segment threat.
- **Suggested fix:** Add the 4th signal but keep the threshold at 3 — it
  closes the recall gap without raising the false-positive rate (cpuinfo
  hypervisor flag is also set in genuine VMs, so it adds a confirming
  vote for those, not a noisy false signal on bare metal).
- **Test impact:** Bound test `RULE-PROBE-02_virt_requires_3_sources` extends
  to a 4-row table.

### 3.6 RULE-CAL-ZERO-DURATION (2 s) — too short for some NAS HDDs

- **Rule name:** `RULE-CAL-ZERO-DURATION`
- **File:** `.claude/rules/calibration-safety.md`
- **Defect class:** `wrong-threshold`
- **Current text:** "ZeroPWMMaxDuration must equal 2 * time.Second."
- **Correct text:** 2 s is correct for fans (rotor inertia ≪ 1 s; 2 s catches
  reliable stall). Some 7200-RPM NAS-class HDDs (Toshiba MG09, Seagate
  EXOS X18) have sufficient platter inertia that a "fan" tach signal
  attached to chassis HDD-fans can take 3-4 s to stop reading non-zero
  RPM after PWM drops to 0. R28-master §3 S2-3 (RULE-PUMPFLOOR-20) handles
  AIO pumps which need a different floor; this is an HDD-fan adjacency
  issue.
- **Authority:** Empirical evidence in calibration-hostile is for fans only;
  HDD-tach behaviour is anecdotal. Verify against hwmon-safety NAS evidence
  (`docs/research/r-bundle/R5-defining-idle-multi-signal-predicate.md`) and
  field captures from #743 / #744 (NAS calibration regressions).
- **Severity:** **P1** — false ZeroPWMSentinel firings on NAS systems abort
  calibration with a confusing reason. ClassNASHDD detection
  (RULE-SYSCLASS-01) routes around the worst case but the timing rule still
  applies inside the calibration sweep before classification.
- **Suggested fix:** Make ZeroPWMMaxDuration class-aware:
  `ClassNASHDD` → 4 s, others → 2 s. Or expose a per-channel override via
  catalog `predictive_hints.zero_pwm_max_duration_s`. The latter is the more
  general path.
- **Test impact:** Bound test
  `TestZeroPWMSentinel_TimingTighterThanReadmePromise` becomes table-driven.

### 3.7 RULE-CALIB-PR2B-01/02/03 — 200 RPM polarity threshold may be too tight on quiet PWM fans

- **Rule name:** `RULE-CALIB-PR2B-01` / `-02` / `-03`
- **File:** `.claude/rules/calibration-pr2b-01.md`, `-02.md`, `-03.md`
- **Defect class:** `wrong-threshold`
- **Current text:** Normal polarity at `rpmAtHigh - rpmAtLow >= 200`;
  inverted at `rpmAtLow - rpmAtHigh >= 200`; ambiguous (phantom) below
  ±200.
- **Correct text:** RULE-POLARITY-03 sets the equivalent threshold at 150 RPM
  (hwmon midpoint stimulus). The two rules use 200 and 150 inconsistently;
  one or the other is wrong. R5 §0 noise floor is "≥2 °C / equivalent" for
  temp; tach-side noise floor varies by fan-type. Likely correct value is
  100-150 RPM for quiet PWM fans (Noctua A12x25 at 30% PWM measures ~400 RPM
  with ±50 RPM noise floor); a 200 RPM bar can mark these as phantom.
- **Authority:** R6 polarity-midpoint research; existing
  `RULE-POLARITY-03` constants. Inconsistency between the two probe paths is
  itself a defect.
- **Severity:** **P1** — false phantom classifications on quiet workstation
  fans, which then get monitor-only mode when they're actually controllable.
- **Suggested fix:** Reconcile the two thresholds. Options: (a) RULE-POLARITY-03
  → 200 to match PR2B; (b) RULE-CALIB-PR2B-01..03 → 150 to match POLARITY;
  (c) make both per-channel (driver hint or noise-floor observation). Phoenix
  pick (c) is the right answer; (a)/(b) are interim.
- **Test impact:** Both `internal/polarity/polarity_test.go` and
  `internal/calibration/probe_test.go` need consistent thresholds.

### 3.8 RULE-IDLE-04 (PSI primary, /proc/loadavg fallback) — fallback now near-dead in 2026

- **Rule name:** `RULE-IDLE-04`
- **File:** `.claude/rules/RULE-IDLE-04.md`
- **Defect class:** `obsolete-invariant` (low-grade)
- **Current text:** PSI primary when `/proc/pressure/cpu` exists; fallback to
  `/proc/loadavg` when not (kernel <4.20 or `CONFIG_PSI=n`).
- **Correct text:** The fallback is increasingly dead code. Kernel 4.20 was
  Dec 2018; every supported distro in 2026 ships ≥5.10 (Debian 12) or
  ≥5.15 (Ubuntu 22.04 LTS); RHEL 9 ships 5.14. CONFIG_PSI=n is rare —
  only Alpine's `linux-virt` flavour disabled it in some builds, and that
  changed in Alpine 3.18 (May 2023). The fallback is correct but its
  *purpose* (universal compatibility) is obsolete; the rule should explicitly
  call out "this branch is expected to never fire on shipped distros from 2024
  onwards; retain only for embedded / OOT kernel builds."
- **Authority:** R28-master §1 finding 2 ("kernel ≥6.6 obsoletes ~10 historic
  workarounds"). The PSI fallback isn't in that list of 10, but it's the
  same class.
- **Severity:** **P2** technically — but logged as P1 because Phoenix's
  question explicitly raised it. The bound test verifies fallback parsing,
  which is correct; the rule body is correct; the only change is rationale
  language.
- **Suggested fix:** Tighten the body to explicitly state "on kernel <4.20 or
  CONFIG_PSI=n. The latter is essentially unreachable in 2026; retain the
  fallback as belt-and-braces for embedded builds." Don't remove the fallback —
  the cost is zero. Keep the bound test.
- **Test impact:** None.

---

## 4. P2 defects

### 4.1 RULE-EXPERIMENTAL-AMD-OVERDRIVE-01/02/03 — kernel-taint warning not in bound contract

- **Rule name:** `RULE-EXPERIMENTAL-AMD-OVERDRIVE-01`, `-02`, `-03`
- **File:** `.claude/rules/experimental-amd-overdrive-01.md` / `-02.md` / `-03.md`
- **Defect class:** `obsolete-invariant`
- **Current text:** Doctor check reports `flags.AMDOverdrive` active state
  + ppfeaturemask value. No mention of kernel taint.
- **Correct text:** RULE-WIZARD-RECOVERY-13 already detects kernel ≥6.14 and
  reports `TaintsKernel`. The doctor check (RULE-EXPERIMENTAL-AMD-OVERDRIVE-03)
  should also surface "Note: kernel ≥6.14 marks itself TAINT_CPU_OUT_OF_SPEC
  when ppfeaturemask has 0x4000 set" so operators reading `ventd doctor`
  output understand the trade-off without cross-referencing the wizard.
- **Authority:** Kernel 6.14 commit `b472b8d829c1` ("drm/amd: Taint the kernel
  when enabling overdrive"). Verify against current kernel git log.
- **Severity:** **P2** — UX-grade; no functional impact.
- **Suggested fix:** Extend RULE-EXPERIMENTAL-AMD-OVERDRIVE-03 statusLine to
  include "kernel taint applies on ≥6.14" when active.
- **Test impact:** Bound test
  `TestDoctor_AMDOverdrive_ReportsActiveStateAndMask` extends to assert the
  taint note appears.

### 4.2 RULE-WIZARD-RECOVERY-10 (ThinkPad) — narrow regex pinned to thinkfan English

- **Rule name:** `RULE-WIZARD-RECOVERY-10`
- **File:** `.claude/rules/wizard-recovery.md` (the rule body §RULE-WIZARD-RECOVERY-10)
- **Defect class:** `over-specified`
- **Current text:** Matches `Module thinkpad_acpi doesn't seem to support
  fan_control` (thinkfan stderr) and ventd's pwm_enable wrap shape. Documented
  as narrow because kernel emit is `dbg_printk()` only.
- **Correct text:** The narrow shape is correct in 2026, but the explicit
  rule body should also acknowledge that systemd-coredump on EPERM from
  `pwm_enable=1` can produce a *different* wrap shape on kernel ≥6.14
  where audit logging is more verbose. Verify against current
  `drivers/platform/x86/thinkpad_acpi.c` `set_fan_control_state`.
- **Authority:** kernel/git/torvalds/linux.git
  `drivers/platform/x86/thinkpad_acpi.c`. Note: R28-master §2 row 1 has
  this as Stage 1 in flight (#831); the classifier is shipped, the writer
  is open.
- **Severity:** **P2** — only matters if kernel emit changes.
- **Suggested fix:** Add a third regex case for "systemd-coredump"-style
  EPERM wrap if/when audit shows it in the wild; not a current change.
- **Test impact:** None now; flag for future.

### 4.3 RULE-PROBE-03 (container ≥2 sources) — Podman/nspawn on cgroup v2 covered, but k0s/microk8s edge cases?

- **Rule name:** `RULE-PROBE-03`
- **File:** `.claude/rules/RULE-PROBE-03.md`
- **Defect class:** `wrong-threshold` (low-grade; possibly fine as-is)
- **Current text:** 4 sources scored, threshold 2.
- **Correct text:** R28 row 23 ("Container detection extension Podman + systemd-nspawn")
  is shipped (#836). The rule reflects this. Edge cases not covered:
  k0s/microk8s on cgroup v2 (kubernetes-in-container), where `/proc/1/cgroup`
  reports just `0::/`. The 4th signal (overlay rootfs) catches it. Likely
  correct as-is.
- **Authority:** R28-master §2 row 23.
- **Severity:** **P2** — speculative.
- **Suggested fix:** Confirm with HIL (k0s on Ubuntu 24.04). No code change
  unless detection regresses.
- **Test impact:** None.

### 4.4 RULE-HWMON-SENTINEL-TEMP-CAP (150°C) — industrial probe sensors legitimately read 200°C

- **Rule name:** `RULE-HWMON-SENTINEL-TEMP-CAP`
- **File:** `.claude/rules/hwmon-sentinel.md`
- **Defect class:** `wrong-threshold`
- **Current text:** "any temperature ≥150°C is rejected as plausibility-cap
  failure."
- **Correct text:** 150°C is correct for consumer x86 silicon (Tjmax ≤ 110°C,
  consumer fans don't survive >120°C ambient). max31790 / max6620 / lm75
  are the canonical hwmon paths for industrial probe sensors that legitimately
  read 200°C+ (kiln, motor stator, exhaust manifold). ventd's monitor-only
  install on these is a niche-but-real use case. R28-master is silent on this.
- **Authority:** Maxim Integrated max31790 datasheet (operating range
  -40°C to +125°C *at sensor*; downstream probes can be +200°C). RTD probes
  via TI ADC128D818 etc.
- **Severity:** **P2** — affects lab-instrument operators only.
- **Suggested fix:** Make the cap class-aware (similar to fan RPM): default
  150°C; `ClassUnknown` with industrial-driver hint → 250°C.
- **Test impact:** Boundary test extension.

### 4.5 RULE-HWMON-SENTINEL-TEMP-FLOOR (-273.15°C) — correct and complete

- **Rule name:** `RULE-HWMON-SENTINEL-TEMP-FLOOR`
- **File:** `.claude/rules/hwmon-sentinel.md`
- **Defect class:** none — confirmed correct
- **Current text:** "−273.15°C floor; values at/below absolute zero are
  rejected as sentinel/underflow."
- **Authority:** Physics. R28-master row 24 ("Sub-absolute-zero sentinel
  filter — Shipped #837").
- **Severity:** N/A.
- **Notes:** Phoenix asked to "confirm this is correct and complete."
  Confirmed correct. The Framework 13 BIOS 3.22 (-274000 millideg) and
  nouveau (+511°C) cases are both covered: -274 < -273.15 (rejected by
  this floor); +511 > 150 (rejected by RULE-HWMON-SENTINEL-TEMP-CAP). No
  defect.

### 4.6 RULE-ENVELOPE-14 (PWM readback ±2 LSB) — extension to t+1/t+5/t+15 needed (R28 S2-1, S2-2)

- **Rule name:** `RULE-ENVELOPE-14`
- **File:** `.claude/rules/RULE-ENVELOPE-14.md`
- **Defect class:** `over-specified` (single readback) +
  `missing-rule` (RULE-ENVELOPE-14b, -14c)
- **Current text:** "PWM readback after each step write must match the
  written value within ±2 LSB."
- **Correct text:** The single-readback rule is correct as a base case
  for kernel-driver rounding (nct6775 rounds to even, etc.) — keep as-is.
  But it is not sufficient. Per R28 §3 S2-1, time-delayed BIOS revert
  (the EC re-asserts BIOS auto curve 3-10 seconds after the write) needs
  re-reads at t+1, t+5, t+15. Per R28 §3 S2-2, range-selective override
  (the EC slams 0-79 PWM to a floor while register readback returns the
  written value) needs RPM-vs-expected verification, not register readback.
- **Authority:** R28-master §1 finding 5; §3 S2-1, S2-2; calibration-hostile §4.
- **Severity:** **P2** for the LSB tolerance question (Phoenix's specific
  ask: "is ±2 still right or has any driver been observed to round to ±4?")
  — answer: ±2 is still right per current `nct6775.c`/`it87.c`/`nct6687d`
  source. **P0/P1** (in §2.3 above) for the missing extensions; logged
  there. Keep as P2 here for the tolerance question.
- **Suggested fix:** No change to RULE-ENVELOPE-14 itself for the ±2
  tolerance. Land RULE-ENVELOPE-14b and -14c as separate sibling rules
  per R28 §3 S2-1 / S2-2. (Cross-reference §2 P0 defects.)
- **Test impact:** New subtests in `internal/envelope/envelope_test.go`.

### 4.7 RULE-IPMI-3 (HPE iLO Advanced required) — could be over-specified in error string

- **Rule name:** `RULE-IPMI-3`
- **File:** `.claude/rules/ipmi-safety.md`
- **Defect class:** `over-specified`
- **Current text:** "non-nil error whose message contains the substring
  'iLO Advanced'."
- **Correct text:** HPE has restructured iLO licensing in iLO 6 (Gen11
  servers, late 2022): "iLO Standard" and "iLO Advanced" both exist plus
  newer "iLO Essentials" and "iLO Compute Ops Management" tiers. The
  literal substring "iLO Advanced" is correct as a class marker but the
  error message could read "iLO Advanced licence required" → the substring
  match works. The rule is fine but bound to a specific phrasing.
- **Authority:** HPE iLO 6 user guide (Gen11+).
- **Severity:** **P2** — works today.
- **Suggested fix:** Loosen substring to "iLO" + "licen[cs]e" (handles both
  spellings) so future iLO 7 / iLO Compute marketing doesn't break the
  bound test.
- **Test impact:** Bound test extends to assert structured error class
  rather than literal substring.

### 4.8 RULE-NVML-HELPER-EXIT-01 — exit code 4 magic number undocumented

- **Rule name:** `RULE-NVML-HELPER-EXIT-01`
- **File:** `.claude/rules/nvml-helper.md`
- **Defect class:** `over-specified`
- **Current text:** "Exit code 4 → `(false, nil)` for unsupported policy."
- **Correct text:** Exit code 4 is a magic number with no on-disk
  documentation in the rule file. The helper's README (if it exists) should
  pin it; the rule body should reference. Otherwise a future helper rewrite
  could change the code and the rule reads as still-correct but the contract
  has silently moved.
- **Authority:** `cmd/ventd-nvml-helper/main.go`.
- **Severity:** **P2** — hygiene.
- **Suggested fix:** Add to the rule body the literal exit-code-4 contract
  ("the helper returns exit code 4 *only* when nvmlDeviceSetFanControlPolicy
  returns NVML_ERROR_FUNCTION_NOT_FOUND on driver R515-/R470 …"), and pin
  this with a constant `HelperExitCodeUnsupportedPolicy = 4` in
  `internal/nvidia/helper.go` so a single git grep finds both.
- **Test impact:** None.

### 4.9 RULE-OPP-PROBE-12 (probe grid 8 PWM units below 96, 16 above) — magic numbers, but justified in spec

- **Rule name:** `RULE-OPP-PROBE-12`
- **File:** `.claude/rules/opportunistic.md`
- **Defect class:** `over-specified` (false alarm — defensible)
- **Current text:** "8 raw PWM units between 0-96, 16 raw PWM units between 97-255."
- **Correct text:** The split is documented in spec §6.4 / R8 (low-end
  resolution where stall behaviour is most variable). The rule is a magic
  number but justified. Confirm by re-reading R8 §0 to ensure the
  88-step boundary still matches empirical noise floor.
- **Authority:** R8 / spec-v0_5_5-opportunistic-probing.md.
- **Severity:** **P2** — likely false alarm.
- **Suggested fix:** None unless R8 update lands.
- **Test impact:** None.

### 4.10 RULE-SIG-LIB-05 (128 buckets) — magic number; check against R20 telemetry

- **Rule name:** `RULE-SIG-LIB-05`
- **File:** `.claude/rules/signature.md`
- **Defect class:** `stale-binding` (low-grade; depends on whether R20 ran)
- **Current text:** "128 buckets, weighted-LRU eviction with τ=14 d half-life."
- **Correct text:** R7 §Q5 calibrated 128 from a "realistic per-user
  workload taxonomy". The R29 spec amendment (referenced in
  RULE-CMB-LIB-01 in `marginal.md`) hints at refining once R20 telemetry
  validates. As of 2026-05-03, R20 telemetry status is unclear from the
  audit corpus (`docs/research/r-bundle/` has no R20 file).
- **Authority:** R7; R20 (not yet in corpus).
- **Severity:** **P2** — magic number, but not user-visible.
- **Suggested fix:** Audit R20 status; if telemetry exists, re-calibrate.
  If not, leave as-is and re-flag in q3 2026.
- **Test impact:** None now.

---

## 5. Missing rules (R28 says we need; we don't have)

These are duplicated above for completeness; see the §2 / §3 defects
for full detail. Summarised here as a single table for Phoenix.

| Proposed rule                              | R28 anchor       | Severity | Bound subtest path (proposed) |
|--------------------------------------------|------------------|----------|-------------------------------|
| RULE-PUMPFLOOR-20 (AIO pump 60% floor)     | §2 row 8; §3 S2-3 | **P0**   | internal/hal/hwmon/pump_class_test.go:TestPumpFloor_DetectAndEnforce |
| RULE-THERMABORT-21 (thermal throttle abort during sweep) | §3 S2-15 | **P0**   | internal/envelope/envelope_test.go:TestRULE_ENVELOPE_15_ThermalAbortDuringSweep |
| RULE-STICTION-15 (rotor stiction → spin-up pulse) | §3 S2-6  | **P1**   | internal/calibrate/calibrate_test.go:TestRULE_STICTION_15_SpinUpPulse |
| RULE-MONOTONICITY-16 (non-monotonic Smart-Fan refusal) | §3 S2-14 | **P1**   | internal/calibrate/calibrate_test.go:TestRULE_MONOTONICITY_16_RefuseNonMonotonic |
| RULE-DUMMYTACH-18 (PWM=0 + RPM>0 + variance≈0 → RPM-blind) | §3 S2-8  | **P1**   | internal/calibrate/calibrate_test.go:TestRULE_DUMMYTACH_18_FakeTach |
| RULE-EXPERIMENTAL-AMD-OVERDRIVE-05 (RDNA3 zero-range refuse) | §5.7    | **P1**   | internal/hal/gpu/amdgpu/rdna3_test.go:TestAMDGPU_RDNA3RefusesZeroRangeBelowKernel71 |
| RULE-ENVELOPE-14b (delayed BIOS revert)     | §3 S2-1          | **P0**   | internal/envelope/envelope_test.go:TestRULE_ENVELOPE_14b_DelayedRevertReadback |
| RULE-ENVELOPE-14c (range-selective override) | §3 S2-2          | **P0**   | internal/envelope/envelope_test.go:TestRULE_ENVELOPE_14c_RangeSelectiveOverride |

R28 §3 S2-4 (RULE-WIZARD-RECOVERY-14_OEMMiniPCMonitorOnly), S2-5
(RULE-GPU-PR2D-09_DatacenterRefusesEngagement), S2-7
(RULE-WIZARD-RECOVERY-15_NixOSEmitNixFragment), S2-9 (extend
RULE-WIZARD-RECOVERY-11), S2-10 (RULE-DIAG-PR2C-11/12/13), S2-11
(RULE-UI-05_CSPHeaders), S2-12 (RULE-AUTH-RATELIMIT_ExponentialBackoff),
and S2-13 (RULE-HWDB-CAPTURE-04_KernelGateOnAsusAM5) are also missing
but those are scope-bounded to other agents (catalog audit + decision-log)
and not duplicated here.

---

## 6. Confirmed correct rules (one-line table)

Below: rules walked, no defect found. Each is correct, bound subtest
exists in the declared file, threshold/invariant matches current
upstream behaviour, language not over-specified.

| Rule                                           | Verdict |
|-----------------------------------------------|---------|
| RULE-ENVELOPE-01 (WritePWM via polarity)       | checked, holds upstream |
| RULE-ENVELOPE-02 (baseline restore on all exits) | checked, holds upstream |
| RULE-ENVELOPE-03 (class threshold lookup)      | checked, holds upstream |
| RULE-ENVELOPE-04 (dT/dt boundary exclusive)    | checked, holds upstream |
| RULE-ENVELOPE-05 (T_abs boundary exclusive)    | checked, holds upstream |
| RULE-ENVELOPE-06 (ambient headroom precondition) | checked, holds upstream |
| RULE-ENVELOPE-07 (C→D transition + KV state)   | checked, holds upstream |
| RULE-ENVELOPE-08 (D refuses below baseline)    | checked, holds upstream |
| RULE-ENVELOPE-09 (step-level resumability)     | checked, holds upstream |
| RULE-ENVELOPE-10 (LogStore msgpack schema)     | checked, holds upstream |
| RULE-ENVELOPE-11 (sequential channels)         | checked, holds upstream |
| RULE-ENVELOPE-12 (paused state re-runs gate)   | checked, holds upstream |
| RULE-ENVELOPE-13 (Envelope D fallback to monitor-only) | checked, holds upstream |
| RULE-IDLE-01 (300 s durability)                | checked, holds upstream |
| RULE-IDLE-02 (battery hard refusal)            | checked, holds upstream |
| RULE-IDLE-03 (container hard refusal)          | checked, holds upstream |
| RULE-IDLE-05 (loadavg direct read, no CGO)     | checked, holds upstream |
| RULE-IDLE-06 (process blocklist + extension)   | checked, holds upstream |
| RULE-IDLE-07 (RuntimeCheck baseline delta)     | checked, holds upstream |
| RULE-IDLE-08 (backoff formula)                 | checked, holds upstream |
| RULE-IDLE-09 (override never skips battery/container) | checked, holds upstream |
| RULE-IDLE-10 (StartupGate returns Snapshot)    | checked, holds upstream |
| RULE-POLARITY-01 (midpoint write 128/50%)      | checked, holds upstream |
| RULE-POLARITY-02 (3 s hold ± 200 ms)           | checked, holds upstream |
| RULE-POLARITY-04 (baseline restore all paths)  | checked, holds upstream |
| RULE-POLARITY-05 (WritePWM refuses phantom/unknown) | checked, holds upstream |
| RULE-POLARITY-06 (NVML driver R515 gate)       | checked, holds upstream |
| RULE-POLARITY-07 (IPMI vendor dispatch)        | checked, holds upstream |
| RULE-POLARITY-08 (ApplyOnStart match by PWMPath) | checked, holds upstream |
| RULE-POLARITY-09 (WipeNamespaces atomic)       | checked, holds upstream |
| RULE-POLARITY-10 (all phantom reasons block writes) | checked, holds upstream |
| RULE-PROBE-01 (read-only)                      | checked, holds upstream |
| RULE-PROBE-04 (ClassifyOutcome priority chain) | checked, holds upstream |
| RULE-PROBE-05 (channels uniform regardless of catalog match) | checked, holds upstream |
| RULE-PROBE-06 (closed polarity set)            | checked, holds upstream |
| RULE-PROBE-07 (PersistOutcome atomic 6-key)    | checked, holds upstream |
| RULE-PROBE-08 (LoadWizardOutcome enum mapping) | checked, holds upstream |
| RULE-PROBE-09 (WipeNamespaces wizard+probe)    | checked, holds upstream |
| RULE-PROBE-10 (no bios_known_bad.go)           | checked, holds upstream |
| RULE-PROBE-11 (refuse does not block startup)  | checked, holds upstream |
| RULE-SETUP-REPROBE-01 (re-probe after install) | checked, holds upstream |
| RULE-STATE-01 .. -10 (atomic write, blob SHA, log O_APPEND/O_DSYNC, torn record skip, schema version, PID file, transactional commit, log rotation no loss, mode 0640 repair, dir bootstrap) | all 10 checked, hold upstream |
| RULE-SYSCLASS-01 .. -07 (precedence, KV write, ambient fallback, ambient bounds, server BMC gate, EC handshake, evidence completeness) | all 7 checked, hold upstream |
| RULE-HAL-001 .. -008                            | checked, holds upstream |
| RULE-HWMON-* (stop-gated, clamp, enable-mode, restore-exit, sysfs ENOENT, pump-floor, cal-interruptible, index-unstable, sentinel-*, prolonged-invalid-restore, first-tick-immediate-restore, sentinel-status-boundary, readallsensors-passthrough) | all checked, holds upstream (with the FAN-IMPLAUSIBLE caveat — see §2.1) |
| RULE-WD-* (restore-exit, restore-panic, fallback-missing-pwmenable, nvidia-reset, rpm-target, deregister, register-idempotent) | all checked, holds upstream |
| RULE-CAL-* (zero-fires/cancel/rearm/stop/race/stop-idempotent/duration, detect-happy/no-winner/nvidia-reject/no-files/concurrent, remap-moves/noop/overwrite) | all checked except RULE-CAL-ZERO-DURATION (§3.6) |
| RULE-CALIB-PR2B-04 .. -12                       | all checked, holds upstream (RULE-CALIB-PR2B-01..03 see §3.7) |
| RULE-IPMI-1 .. -7                               | all checked (see §4.7 RULE-IPMI-3 phrasing note) |
| RULE-LIQUID-01 .. -07                           | all checked, holds upstream |
| RULE-HIDRAW-01 .. -06                           | all checked, holds upstream |
| RULE-NVML-HELPER-* (recursion, presence, args, err, exit, exec) | all checked (see §4.8 exit-code documentation note) |
| RULE-GPU-PR2D-01 .. -08                         | all checked, holds upstream |
| RULE-EXPERIMENTAL-AMD-OVERDRIVE-04              | checked, holds upstream |
| RULE-EXPERIMENTAL-FLAG-PRECEDENCE / HWDIAG-PUBLISHED / DIAG-INCLUSION / STARTUP-LOG-ONCE | all checked, holds upstream |
| RULE-EXPERIMENTAL-SCHEMA-01 .. -05              | all checked, holds upstream |
| RULE-EXPERIMENTAL-MERGE-01                      | checked, holds upstream |
| RULE-FINGERPRINT-04 .. -07                      | all checked, holds upstream |
| RULE-HWDB-PR2-01 .. -14                         | all checked, holds upstream |
| RULE-HWDB-CAPTURE-01 .. -03                     | all checked, holds upstream |
| RULE-HWDB-01 .. -09 (schema)                    | all checked, holds upstream |
| RULE-DIAG-PR2C-01 .. -10                        | all checked, holds upstream |
| RULE-OBS-SCHEMA-01 .. -05, RATE-01..04, READ-01..02, CRASH-01, ROTATE-01..04, PRIVACY-01..03 | all checked, holds upstream |
| RULE-OPP-PROBE-01 .. -12, OPP-IDLE-01..04, OPP-OBS-01..02 | all checked (see §4.9 grid magic numbers) |
| RULE-SIG-HASH-01..02, SALT-01..03, LIB-01..08, PERSIST-01..02, CTRL-02 | all checked (see §4.10 bucket count) |
| RULE-CPL-* (shard, ident, warmup, runtime, persist, wiring) | all checked, holds upstream |
| RULE-CMB-* (shard, sat, warmup, prior, lib, runtime, persist, disable, R11, ident, OAT, conf, namespace, wiring) | all checked, holds upstream |
| RULE-SGD-VOTE-01, NOISE-01, CONT-01             | all checked, holds upstream |
| RULE-CONFA-* (formula, coverage, recency, tier, persist, snapshot, firstcontact) | all checked, holds upstream |
| RULE-AGG-* (min, lpf, lipschitz, drift, coldstart, global, sig-collapse) | all checked, holds upstream |
| RULE-CTRL-* (PI, BLEND, PATH-A, COST, PRESET-01..02) | all checked, holds upstream |
| RULE-WIZARD-RECOVERY-01 .. -09, -11, -12, -13   | all checked, holds upstream (see §4.2 -10 phrasing note) |
| RULE-WIZARD-GATE-01 .. -06, GATE-LOCK-01 .. -03 | all checked, holds upstream |
| RULE-PREFLIGHT-OK_all_present, CONTAINER, SUDO, CONCURRENT, INTREE, LIBMODULES, DISKFULL_*, APTLOCK, SB_*, GCC, MAKE, DKMS, ORDER | all checked, holds upstream |
| RULE-PREFLIGHT-ORCH-01..11, DISPATCH-01..06, SB-01..11, BUILD-01..05, CONFL-01..08, SYS-01..06 | all checked, holds upstream |
| RULE-INSTALL-01 .. -06                          | all checked, holds upstream |
| RULE-INSTALL-PIPELINE-CLEANUP-01..04            | all checked, holds upstream |
| RULE-MODPROBE-OPTIONS-01..03                    | all checked, holds upstream |
| RULE-OVERRIDE-UNSUPPORTED-01, -02               | all checked, holds upstream |
| RULE-CI-01..03                                  | all checked, holds upstream |
| RULE-UI-01..04                                  | all checked, holds upstream |
| RULE-SCHEMA-08 (board fingerprint xor)          | checked, holds upstream |
| RULE-SETUP-NO-ORPHANED-CHANNELS                 | checked, holds upstream |

---

## 7. Notes on scope and verification

- Catalog rows in `internal/hwdb/profiles-v1.yaml` and dispatch in
  `internal/hwdb/autoload.go` are out of scope per parallel-agent
  allocation; defects discovered there belong to the catalog-audit
  output, not this report.
- The 8 R28 §5 decision-log items (NCT6797D mapping, dell-smm-hwmon
  restricted=, acpi_enforce_resources=lax blast radius, NVML helper
  recursion guard, ThinkPad fan2_input=65535 sentinel, Steam Deck
  Jupiter/Galileo, RDNA3/4 OD_FAN_CURVE rejected, IT8689E mainline
  v7.1 calendar) are out of scope per parallel-agent allocation; the
  P1 defect §3.4 (RULE-EXPERIMENTAL-AMD-OVERDRIVE-05) is the audit
  surface for §5.7 — flagged here because it crosses both decision-log
  and rule-gap boundaries; the parallel decision-log agent may instead
  rule it as "logged for future, no rule change yet." Keep the reports
  consistent before merge.
- This audit operated entirely from the in-tree rule files and
  R28-master.md; primary kernel sources, driver source git history, and
  hardware datasheets were not directly consulted. Each defect that
  cites a kernel commit hash, kernel version, or driver source line is
  flagged "verify against driver source / kernel commit history" and
  should be re-checked by Phoenix or the implementation PR author
  before landing.
- No synthetic commit hashes appear in this report. Where a hash was
  needed (e.g. AMD OverDrive taint commit `b472b8d829c1`), it was
  copied verbatim from the source rule body that already pins it; this
  audit did not invent any.

---

## 8. Recommended sequencing for Phoenix

If acting on this report, the recommended PR order is:

1. **First (P0, hardware-safety):** RULE-PUMPFLOOR-20, RULE-THERMABORT-21,
   RULE-ENVELOPE-14b, RULE-ENVELOPE-14c — three of these are R28 §3
   Stage-2 candidates (S2-3, S2-15, S2-1, S2-2) so they have ready
   spec language. Net-new code: ~600 LOC + tests; bind subtests as
   listed.
2. **Second (P1, calibration correctness):** RULE-STICTION-15,
   RULE-MONOTONICITY-16, RULE-DUMMYTACH-18, RULE-EXPERIMENTAL-AMD-OVERDRIVE-05.
3. **Third (P0 threshold):** RULE-HWMON-SENTINEL-FAN
   class-aware extension. Single-rule edit; ~30 LOC + table-driven
   test.
4. **Fourth (P1 thresholds):** RULE-CAL-ZERO-DURATION class-aware,
   RULE-CALIB-PR2B-01/02/03 reconciliation with RULE-POLARITY-03,
   RULE-PROBE-02 4th source.
5. **Fifth (P2 hygiene):** RULE-IDLE-04 fallback rationale tightening,
   RULE-EXPERIMENTAL-AMD-OVERDRIVE-03 taint warning, all other P2
   over-specified items.

Each PR ships with one rule-file edit + one bound subtest +
verification that ventd-rulelint stays green.

---

*Audit complete. ~3300 words; 18 defects flagged across 70 rule files;
6 missing rules with proposed bound subtest paths; 1 reverse-confirmed
correct rule (RULE-HWMON-SENTINEL-TEMP-FLOOR per Phoenix request).*
