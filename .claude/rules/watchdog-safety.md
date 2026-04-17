# Watchdog Safety Rules

These invariants govern the last-line-of-defence layer that restores
pre-ventd fan state on every documented shutdown path. Violating any of
them risks leaving a fan at a PWM the daemon last wrote — often zero,
usually wrong.

Each rule below is bound to one subtest in `internal/watchdog/safety_test.go`.
If a rule text is edited, update the corresponding subtest in the same PR;
if a new rule lands, it must ship with a matching subtest or the rule-lint
in `tools/rulelint` blocks the merge.

## RULE-WD-RESTORE-EXIT: every documented exit path restores pwm_enable

Restore, RestoreOne (restoreOne), the daemon-level defer driving Restore
on ctx cancel / SIGTERM, and the per-entry recover inside restoreOne all
must end with pwm_enable=1 (or the originally-captured value) written back
for every registered channel. The watchdog is the last-line-of-defence
covering graceful exits — if this loop returns without touching a
registered entry, that fan stays at the daemon's last PWM write.

Bound: internal/watchdog/safety_test.go:wd_restore_exit_touches_all_entries

## RULE-WD-RESTORE-PANIC: one entry's panic must not abort the rest

A panic inside restoreOne for entry N must be recovered per-entry so the
Restore loop continues with entries N+1..end. A synthetic panic injected
via the slog handler proves the recover frame actually fires and the
remaining entries still receive their restore writes. Without this, the
first bad fan silently forfeits the rest of the fleet.

Bound: internal/watchdog/safety_test.go:wd_restore_panic_continues_loop

## RULE-WD-FALLBACK-MISSING-PWMENABLE: missing pwm_enable is logged, not fatal

If a registered channel's pwm_enable file is absent at Restore time
(nct6683, silicon-labs pwm-fan, any driver that exposes a write-only duty
cycle), restoreOne logs the fallback and writes the full-speed safety net
(PWM=255 for duty-cycle channels, fan*_max for rpm_target channels) — no
panic, no early return from the loop, no skipped successor entries.

Bound: internal/watchdog/safety_test.go:wd_fallback_missing_pwm_enable_continues

## RULE-WD-NVIDIA-RESET: NVML channels hand back to the driver, never PWM=0

Channels whose fanType is "nvidia" must dispatch to nvidia.ResetFanSpeed
(which calls nvmlDeviceSetDefaultFanSpeed_v2, the manufacturer-default
equivalent of nvmlDeviceSetDefaultAutoBoostedClocksEnabled for fans) on
Restore. Writing PWM=0 or PWM=255 to an NVML channel is not a valid
restore — NVML does not expose a "full speed" duty-cycle primitive; auto
is the only safe hand-back. A malformed gpu index is logged-and-skipped,
never panicked on.

Bound: internal/watchdog/safety_test.go:wd_nvidia_restore_uses_auto_not_zero

## RULE-WD-RPM-TARGET: rpm_target channels restore via fan*_max, not raw PWM

Channels registered via a fan*_target path (pre-RDNA amdgpu) take a
different restore path than duty-cycle pwm* channels: writing "255" to
fan1_target would mean 255 RPM, not full speed. When pwm_enable is
unavailable, restoreOne must write ReadFanMaxRPM(fan*_max) to the target
file as the safety-net equivalent of PWM=255.

Bound: internal/watchdog/safety_test.go:wd_rpm_target_restore_uses_max_rpm

## RULE-WD-DEREGISTER: Deregister on unknown or already-removed path is a no-op

Deregister("/does-not-exist") must not panic and must not mutate the
entries slice. Double-Deregister of the same path removes at most one
entry (the LIFO top) per call; the second call is a no-op. This keeps
calibration and setup sweeps safe to unwind in any order without
cross-talk.

Bound: internal/watchdog/safety_test.go:wd_deregister_unknown_and_double_is_noop

## RULE-WD-REGISTER-IDEMPOTENT: startup origEnable survives re-registration

A Register call stacks on top of the startup registration for the same
pwmPath (per-sweep registration pattern). The startup entry's origEnable
— captured once at first Register from the pre-daemon pwm_enable — must
be preserved across any subsequent Register + Deregister cycle for the
same path. The daemon-exit Restore reads the startup entry, not a sweep
entry, so the original value must still be there when the daemon shuts
down.

Bound: internal/watchdog/safety_test.go:wd_register_preserves_startup_origenable
