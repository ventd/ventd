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
