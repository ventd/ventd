# Setup wizard rules

These invariants govern the first-boot setup wizard's responsibility for
hwmon channels it probes and classifies. The wizard runs detection and
calibration in a window where channels are intentionally left at
`pwm_enable=1` (manual mode) so the calibration sweep can write `pwm=0`
without EBUSY on chips like it8772; that pattern is correct DURING the
sweep but must be undone for excluded channels before the wizard
returns.

Each rule below is bound to a subtest under `internal/setup/` (or the
orchestrator subtree). If a rule text is edited, update the binding
subtest in the same PR; if a new rule lands, it must ship with a
matching subtest or `tools/rulelint` blocks the merge.

## RULE-SETUP-NO-ORPHANED-CHANNELS: Every probed hwmon channel that does NOT make it into the generated config MUST have `pwm_enable` restored to its probe-time captured value before the wizard returns.

<!-- rulelint:allow-orphan -->

The orchestrator's `ApplyPhase` (`internal/setup/orchestrator/apply.go`
lines 200-232) walks every fan in the ProbeArtifact. For each channel
that's NOT in the applied config (phantom-classified, calibrate-
phantom, monitor-only-demoted), it writes `fan.InitialEnable` — the
pre-ventd value captured at probe time — back to the channel's
`EnablePath`. The count of successfully-restored channels is surfaced
via `ApplyArtifact.EnableRestored`.

The wizard's calibration loop deliberately leaves `pwm_enable=1` on
every probed channel so the stall sweep can write `pwm=0` without
returning EBUSY on chips like it8772. This is correct during the
sweep but leaves a trail of "manual mode + frozen calibration leftover
PWM byte" on every channel that fails detection or calibration unless
this rule fires. The daemon's watchdog only restores on graceful exit;
during normal operation post-wizard, no restore ever fires without
this hook.

The motivating failure: HIL on a Gigabyte B550M Aorus Pro / IT8688
where pwm2/pwm3 (detection failed) sat at PWM=70 / 0 RPM and pwm4
(calibration aborted on RPM sentinel) sat at PWM=0 / 0 RPM after
wizard completion. The front chassis fan was off and the user had no
diagnostic surface — issue #753.

Restore semantics changed in the v0.8.x orchestrator rework: legacy
`restoreExcludedChannels` wrote `pwm_enable=2` (BIOS auto) unconditionally
and ran a probe-based EINVAL fallback chain via `handbackExcludedChannel`
(see #909, follow-ups). The orchestrator's simpler approach writes the
probe-time captured value, which by construction is what the chip
returned at read time. This trades the EINVAL recovery on chips that
lie on read (NCT6687D — see #1249) for a simpler one-shot write; the
watchdog now carries the EINVAL fallback for its own restore path. A
direct apply-side regression test is TODO (allow-orphan until then).
