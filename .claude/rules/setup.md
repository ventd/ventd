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

## RULE-HWMON-ENABLE-EINVAL-FALLBACK: Excluded channel handback resolves through three stages before logging a final-position WARN.

The hwmon `pwm_enable` enum isn't universal: standard convention is
`{0=off, 1=manual, 2=auto}`, but the mainline kernel `nct6687`
driver uses `{0=off, 1=manual, 5=SmartFan auto}` — writing `2`
returns `EINVAL`. `restoreExcludedChannels` previously hardcoded
`2` and bailed to a WARN log on rejection, which left every
NCT6687D-driven channel stranded at the calibration sweep's last
PWM byte (issue #909).

The operator-promise that "the daemon should never silently ignore
a failure it could attempt to resolve" requires `restoreExcludedChannels`
to work through a resolution chain instead of giving up on the
first error. `handbackExcludedChannel(f, logger)` MUST attempt:

1. **`pwm_enable = 2`** — standard hwmon "auto". Most chips. On
   `errors.Is(err, fs.ErrNotExist)`: short-circuit clean (the chip
   doesn't expose `pwm_enable`; nothing to restore — distinct from
   the EINVAL case).
2. **On `errors.Is(err, syscall.EINVAL)`: `pwm_enable = 5`** —
   nct6687 SmartFan / known quirk. Tried unconditionally on EINVAL
   because the cost of an extra failed write on a non-nct6687 chip
   is one syscall and one log line; the cost of skipping it is
   leaving every nct6687-driven system stuck forever.
3. **On both auto-mode writes failing: write `safeExcludedPWM`
   (`153 ≈ 60%`)** to the PWM path directly and leave
   `pwm_enable = 1` (manual, where the calibration sweep already
   left it). Any controlled state is preferable to stranding the
   channel at the sweep-end byte. Mirrors the watchdog's
   `WritePWM(path, 255)` fallback (RULE-HWMON-RESTORE-EXIT) but
   tuned for non-emergency cleanup.
4. **Only after all three exhaust** does the daemon log a WARN.
   The WARN names every attempted strategy and its specific
   failure (`err_mode_2`, `err_safe_pwm`) so any operator-facing
   surface (recovery card, diag bundle) has the full picture
   rather than the previous opaque log-and-ignore.

Asymmetry: the mode=5 retry fires ONLY on `EINVAL`. Other errors
(IO error, permission denied, etc.) skip step 2 and fall straight
through to step 3. Step 2 is a known-driver-quirk recovery, not a
generic "try anything" loop.

Test injection: `writePWMEnableFn` and `writePWMFn` are
package-level `var`s defaulted to the production `hwmonpkg`
functions. Tests swap them via `t.Cleanup` to simulate the EINVAL
behaviour that real tmpfs sysfs fixtures can't reproduce
(kernel-side enum validation isn't observable through plain file
writes).

Bound: internal/setup/restore_excluded_test.go:TestRestoreExcludedChannels_EINVALFallsBackToMode5
Bound: internal/setup/restore_excluded_test.go:TestRestoreExcludedChannels_BothModesFailFallsBackToSafePWM
Bound: internal/setup/restore_excluded_test.go:TestRestoreExcludedChannels_OnlyEINVALTriggersMode5Retry
