# Hardware Safety Rules

These are non-negotiable. Violating these can damage hardware.

Each rule below is bound to one subtest in `internal/controller/safety_test.go`
or `internal/calibrate/calibrate_test.go`. If a rule text is edited, update the
corresponding subtest in the same PR; if a new rule lands, it must ship with a
matching subtest or the rule-lint in `tools/rulelint` blocks the merge.

## RULE-HWMON-STOP-GATED: PWM=0 requires allow_stop=true AND min_pwm=0

Writing duty-cycle zero to a fan stops the rotor entirely. This is only
permissible when the fan config explicitly opts in via both `allow_stop: true`
and `min_pwm: 0`. If either condition is absent the controller must refuse
the zero write and leave the fan at its current speed. Silently stalling a
fan whose config did not declare it fan-stop-safe risks thermal runaway.

Bound: internal/controller/safety_test.go:allow_stop/disabled_refuses_zero

## RULE-HWMON-CLAMP: PWM writes clamped to [min_pwm, max_pwm]

Every duty-cycle value produced by a curve must be clamped to the
[MinPWM, MaxPWM] range from the fan's config entry before the write reaches
the sysfs PWM file. A curve bug or a floating-point edge case must not be
able to stall a fan below its configured floor or overdrive it above its
configured ceiling.

Bound: internal/controller/safety_test.go:clamp/below_min_pwm

## RULE-HWMON-ENABLE-MODE: pwm_enable set to 1 (manual) before first PWM write

hwmon drivers default to pwm_enable=2 (BIOS/auto control). Writing a PWM
value while the driver is in auto mode has no lasting effect -- the firmware
override loop re-asserts its own speed within milliseconds. The controller
must write pwm_enable=1 before the first PWM write of a Run session.
Drivers that do not expose pwm_enable (e.g. nct6683) must be treated as
already in manual mode and proceed without error.

Bound: internal/controller/safety_test.go:pwm_enable/manual_mode_set_on_run_start

## RULE-HWMON-RESTORE-EXIT: Watchdog.Restore() fires on every documented exit path

The controller's Run method must call Watchdog.Restore() on every exit:
context cancellation (normal daemon shutdown), tick-level panic (hardware
driver crash), and any error return. Restore writes pwm_enable back to the
pre-daemon value for every registered channel. A Run that returns without
triggering Restore leaves fans at whatever PWM the daemon last wrote --
often zero, always wrong.

Bound: internal/controller/safety_test.go:watchdog/restore_on_context_cancel

## RULE-HWMON-SYSFS-ENOENT: ENOENT and EIO on sysfs reads are logged and skipped

A sensor file that disappears at runtime (device hot-removed, driver
unloaded) returns ENOENT; a transient driver error returns EIO. Neither
must crash the controller or produce a panic. The tick must log the error,
skip the affected sensor for this cycle, and continue writing to all fans
that still have valid readings. An erring sensor must never silently stop
all fan control.

Bound: internal/controller/safety_test.go:sensor_read/enoent_skip

## RULE-HWMON-PUMP-FLOOR: pump fans never written below pump_minimum

Fans marked `is_pump: true` circulate coolant; spinning below a threshold
risks coolant stall and thermal damage. The controller must enforce a hard
floor at `pump_minimum` even when the configured MinPWM is lower or the
curve output is lower. This floor is applied as part of the clamp step and
takes precedence over every other limit.

Bound: internal/controller/safety_test.go:clamp/pump_floor_beats_curve

## RULE-HWMON-CAL-INTERRUPTIBLE: calibration restores original PWM on abort

The calibration sweep drives fans to fixed duty cycles to measure RPM
curves. If calibration is aborted (SIGINT, context cancel, or error), the
original PWM values captured at sweep start must be restored before the
function returns. Leaving fans at calibration-time duty cycles after an
interrupted sweep (often full speed or zero) is unacceptable even
transiently.

Bound: internal/calibrate/calibrate_test.go:TestAbortRestoresPWM

## RULE-HWMON-INDEX-UNSTABLE: hwmon paths resolved via device path, not index number

hwmonN directory numbers (hwmon0, hwmon1, ...) are kernel-assigned at boot
and change across reboots, module reloads, and hotplug events. The daemon
must store the stable sysfs device path (the `hwmon_device` link target)
and re-resolve the current hwmonN index at startup via `hwmon.ResolvePath`.
Hardcoding an index in persistent config or in-memory state will silently
write to the wrong fan after a reboot.

Bound: internal/controller/safety_test.go:hwmon_index_instability/resolve_by_device_path
