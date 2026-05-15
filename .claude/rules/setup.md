# Setup wizard rules

These invariants govern the first-boot setup wizard's responsibility for
hwmon channels it probes and classifies. The wizard runs detection and
calibration in a window where channels are intentionally left at
`pwm_enable=1` (manual mode) so the calibration sweep can write `pwm=0`
without EBUSY on chips like it8772; that pattern is correct DURING the
sweep but must be undone for excluded channels before the wizard
returns.

Each rule below is bound 1:1 to a subtest in `internal/setup/`. If a
rule text is edited, update the binding subtest in the same PR; if a
new rule lands, it must ship with a matching subtest or `tools/rulelint`
blocks the merge.

## RULE-SETUP-NO-ORPHANED-CHANNELS: Every probed hwmon channel that does NOT make it into the generated config MUST be handed back to BIOS auto (pwm_enable=2) before the wizard returns.

`restoreExcludedChannels(fans, doneFans, logger)` walks every channel in
`fans` whose `Type == "hwmon"` and whose `PWMPath` is NOT present in any
`doneFans[i].pwmPath`. For each, it writes `pwm_enable=2` via
`hwmonpkg.WritePWMEnable`. Drivers that do not expose `pwm_enable`
(nct6683 / NCT6687D) are silently skipped — the channel never had
manual mode in the first place. Non-hwmon fan types (NVML / IPMI) are
skipped because their backends own their own restore surface.

The wizard's calibration loop deliberately leaves `pwm_enable=1` on
every probed channel so the stall sweep can write `pwm=0` without
returning EBUSY on chips like it8772. This is correct during the
sweep but leaves a trail of "manual mode + frozen calibration leftover
PWM byte" on every channel that fails detection or calibration unless
this rule fires. The daemon's watchdog only restores on graceful
exit; during normal operation post-wizard, no restore ever fires
without this hook.

The motivating failure: HIL on a Gigabyte B550M Aorus Pro / IT8688
where pwm2/pwm3 (detection failed) sat at PWM=70 / 0 RPM and pwm4
(calibration aborted on RPM sentinel) sat at PWM=0 / 0 RPM after
wizard completion. The front chassis fan was off and the user had no
diagnostic surface — issue #753.

Bound: internal/setup/restore_excluded_test.go:TestRestoreExcludedChannels_HandsBackToBIOS
Bound: internal/setup/restore_excluded_test.go:TestRestoreExcludedChannels_NoOpWhenAllControlled
Bound: internal/setup/restore_excluded_test.go:TestRestoreExcludedChannels_SkipsNonHwmonTypes
Bound: internal/setup/restore_excluded_test.go:TestRestoreExcludedChannels_TolerantOfMissingPwmEnable

## RULE-HWMON-ENABLE-EINVAL-FALLBACK: Excluded channel handback resolves through a probe-based chain before logging a final-position WARN.

The hwmon `pwm_enable` enum isn't universal: standard convention is
`{0=off, 1=manual, 2=auto}`, but the mainline kernel `nct6687`
driver uses `{0=off, 1=manual, 5=SmartFan auto}` — writing `2`
returns `EINVAL`. PR #910 hardcoded `5` as the EINVAL fallback;
HIL on Phoenix's MSI PRO Z690-A then surfaced the in-tree-nct6687
case where the chip rejects `5` AS WELL — the driver build accepts
ONLY `{0,1}` (issues #909, follow-up).

The operator-promise that "the daemon should never silently ignore
a failure it could attempt to resolve" requires the daemon to
discover at runtime which `pwm_enable` values the chip actually
accepts, rather than carrying a static enum table that lags every
new driver build. `handbackExcludedChannel(f, logger)` MUST attempt:

1. **`pwm_enable = 2`** — standard hwmon "auto". Fast path for the
   >95 % of chips that follow convention; one sysfs write, no probe.
   On `errors.Is(err, fs.ErrNotExist)`: short-circuit clean (the
   chip doesn't expose `pwm_enable`; nothing to restore — distinct
   from the EINVAL case).
2. **On `errors.Is(err, syscall.EINVAL)`: probe the chip's accepted
   pwm_enable enum** by writing each candidate in `pwmEnableProbeRange`
   ({2..7}) once, observing which return success vs EINVAL, then
   restoring `pwm_enable = 1` (manual). Pick the highest-numbered
   accepted value and write it as the handback target. Highest-wins
   because conventional drivers number "richer" auto modes higher
   (nct6687 SmartFan = 5 over standard auto = 2). Probe runs once
   per pwm path per daemon lifetime; cached in `probedPWMEnableModes`.
3. **On the probe finding nothing OR every probed value also failing:
   write `safeExcludedPWM` (`153 ≈ 60 %`)** to the PWM path directly
   and leave `pwm_enable = 1` (manual, where the calibration sweep /
   probe-restore left it). Any controlled state is preferable to
   stranding the channel at the sweep-end byte. Mirrors the watchdog's
   `WritePWM(path, 255)` fallback (RULE-HWMON-RESTORE-EXIT) but
   tuned for non-emergency cleanup. The probe-came-up-empty branch
   logs an INFO line ("driver supports only manual control") so the
   operator-facing surface knows it's a manual-only driver, not a
   transient failure.
4. **Only after all three exhaust** does the daemon log a final-
   position WARN. The WARN names every attempted strategy and its
   specific failure (`err_mode_2`, `err_safe_pwm`) so any operator-
   facing surface (recovery card, diag bundle) has the full picture
   rather than the previous opaque log-and-ignore.

Asymmetry: the probe fires ONLY on `EINVAL`. Other errors (IO
error, permission denied, etc.) skip the probe and fall straight
through to safe-PWM. The probe is a known-driver-quirk discovery
path, not a generic "try anything" loop.

Per-path caching: drivers like `nct6798` have per-pwm-channel enum
quirks; one chip's pwm1 might accept `{2,5}` while pwm4 accepts
only `{1}`. Per-path caching costs O(channels) memory and zero
extra syscalls for the common case where every channel of a chip
behaves identically.

Test injection: `writePWMEnableFn` and `writePWMFn` are
package-level `var`s defaulted to the production `hwmonpkg`
functions. Tests swap them via `t.Cleanup` to simulate the EINVAL
behaviour that real tmpfs sysfs fixtures can't reproduce
(kernel-side enum validation isn't observable through plain file
writes). `resetProbedAutoModesForTest(t)` clears the per-process
cache between tests.

Bound: internal/setup/restore_excluded_test.go:TestRestoreExcludedChannels_EINVALProbeFindsAcceptedMode
Bound: internal/setup/restore_excluded_test.go:TestRestoreExcludedChannels_ProbeFindsNothingFallsBackToSafePWM
Bound: internal/setup/restore_excluded_test.go:TestRestoreExcludedChannels_NonEINVALSkipsProbe
Bound: internal/setup/restore_excluded_test.go:TestRestoreExcludedChannels_ProbeResultIsCached

## RULE-SETUP-PHANTOM-VERIFY: post-calibration sanity check writes PWM=255 + 3s + 3 RPM samples; channels with all-zero readings re-classify as phantom.

Even with the polarity probe wired (RULE-POLARITY-03 via #1026 layer 1)
and the pre-ramp stability gate fronting `DetectRPMSensor`
(RULE-CAL-DETECT-STABILITY via #1026 layer 2), the wizard's `CalPhase`
filter at `internal/setup/setup.go:1206` admits a channel as `done`
when EITHER `result.StartPWM` or `result.MaxRPM` is non-zero. A single
chip mode-transition glitch during the calibration sweep can satisfy
that condition on phantom channels, landing them in `controls:`
despite being physically dead.

Layer 3 catches the false-positive class with one final write-and-read
after calibration completes:

1. For every channel with `CalPhase == "done"` and `Type == "hwmon"`:
2. Write the polarity-aware full-speed byte to `PWMPath`: raw `255`
   for `PolarityPhase == "normal"` (default), raw `0` for
   `PolarityPhase == "inverted"`. The polarity-aware split was added
   in #1110 — pre-#1110 the verify wrote raw 255 unconditionally,
   which on a genuinely-inverted channel (NCT6683 on MSI, IT87 on
   some Gigabyte) is 0% effective duty and produces 0 RPM, falsely
   re-classifying a perfectly working channel as phantom.
3. Sleep 3 seconds for the fan to settle.
4. Take three RPM samples from `RPMPath`, spaced 200 ms apart.
5. If at least one sample reads > 0, admit (real fan).
6. If every sample reads 0, re-classify: set `CalPhase = "skipped"`
   and `PolarityPhase = "phantom"`. The downstream `doneFans`
   collection skips this channel; `restoreExcludedChannels` hands
   it back to BIOS auto.

The deferred restore in `verifyHwmonChannelSpins` writes the captured
`origPWM` byte back on every exit path (admit, refuse, ctx-cancel,
sysfs error). Failure to restore would leave the channel running at
PWM=255 indefinitely after the wizard exits — louder than any
calibration-sweep residue.

Production-path errors (file open / write / read) are logged at WARN
and treated as "could not verify" → admit. The original `CalPhase`
classification was reached via the calibration sweep itself, so a
verify-IO failure is NOT a downgrade signal — better to risk admitting
a real fan that lost a tach read than to drop a real fan because
sysfs glitched. The asymmetry mirrors RULE-DOCTOR-04 graceful-degrade
semantics.

Cost: ~3 s per "done" hwmon channel — for a typical 5-fan host that's
+15 s on the wizard's once-per-install runtime. Negligible vs the
operator burden of tracking down "why is ventd writing PWM to a
header that has no fan plugged in" months later.

The verification runs unconditionally (no operator override). A
falsely-admitted phantom channel under real workload silently writes
to dead headers forever; the verification's runtime cost is bounded
and one-shot.

Bound: internal/setup/phantom_verify_test.go:TestVerifyHwmonChannelSpins
Bound: internal/setup/phantom_verify_test.go:TestVerifyHwmonChannelSpins_OrigPWMRestoredOnAllExitPaths
