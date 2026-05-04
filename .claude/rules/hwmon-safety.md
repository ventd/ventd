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
Bound: internal/controller/safety_test.go:allow_stop/enabled_permits_zero

## RULE-HWMON-CLAMP: PWM writes clamped to [min_pwm, max_pwm]

Every duty-cycle value produced by a curve must be clamped to the
[MinPWM, MaxPWM] range from the fan's config entry before the write reaches
the sysfs PWM file. A curve bug or a floating-point edge case must not be
able to stall a fan below its configured floor or overdrive it above its
configured ceiling.

Bound: internal/controller/safety_test.go:clamp/below_min_pwm
Bound: internal/controller/safety_test.go:clamp/above_max_pwm

## RULE-HWMON-ENABLE-MODE: pwm_enable set to 1 (manual) before first PWM write

hwmon drivers default to pwm_enable=2 (BIOS/auto control). Writing a PWM
value while the driver is in auto mode has no lasting effect -- the firmware
override loop re-asserts its own speed within milliseconds. The controller
must write pwm_enable=1 before the first PWM write of a Run session.
Drivers that do not expose pwm_enable (e.g. nct6683) must be treated as
already in manual mode and proceed without error.

Bound: internal/controller/safety_test.go:pwm_enable/manual_mode_set_on_run_start
Bound: internal/controller/safety_test.go:pwm_enable/unsupported_driver_proceeds

## RULE-HWMON-MODE-REACQUIRE: EBUSY on PWM write triggers single re-acquire + retry

RULE-HWMON-ENABLE-MODE covers the FIRST write contract. This rule covers
the SUSTAIN contract: some BIOSes — Gigabyte Q-Fan / Smart Fan Control on
IT8xxx chips is the canonical case (see issue #904) — periodically reassert
pwm_enable=2 on channels ventd has already acquired. The next duty-cycle
write returns EBUSY because the chip is back under firmware control,
exactly as if no acquire had ever happened.

`Backend.Write` MUST detect `errors.Is(err, syscall.EBUSY)` on the
duty-cycle write, drop the cached acquired-state for the channel
(`b.acquired.Delete(pwmPath)`), re-write `pwm_enable=1`, and retry the
original duty-cycle write **exactly once**. A second EBUSY surfaces the
wrapped failure to the caller so the controller logs it against the fan
and the calibration / control loop triggers the fan-aborted path.

Single retry only — never spin. If the BIOS is reasserting on a tighter
timer than this primitive can absorb, that's a heartbeat-class problem
worth its own fix (probably a periodic re-write of pwm_enable=1 from the
control loop) and a separate rule. This rule documents the recovery
primitive only; it never converts a real EBUSY-storm into a hung daemon.

Bound: internal/hal/hwmon/backend_test.go:TestWrite_EBUSY_ReacquiresAndRetries
Bound: internal/hal/hwmon/backend_test.go:TestWrite_PersistentEBUSY_FailsAfterOneRetry

## RULE-HWMON-RESTORE-EXIT: Watchdog.Restore() fires on every documented exit path

The controller's Run method must call Watchdog.Restore() on every exit:
context cancellation (normal daemon shutdown), tick-level panic (hardware
driver crash), and any error return. Restore writes pwm_enable back to the
pre-daemon value for every registered channel. A Run that returns without
triggering Restore leaves fans at whatever PWM the daemon last wrote --
often zero, always wrong.

Bound: internal/controller/safety_test.go:watchdog/restore_on_context_cancel
Bound: internal/controller/safety_test.go:watchdog/restore_on_tick_panic

## RULE-HWMON-SYSFS-ENOENT: ENOENT and EIO on sysfs reads are logged and skipped

A sensor file that disappears at runtime (device hot-removed, driver
unloaded) returns ENOENT; a transient driver error returns EIO. Neither
must crash the controller or produce a panic. The tick must log the error,
skip the affected sensor for this cycle, and continue writing to all fans
that still have valid readings. An erring sensor must never silently stop
all fan control.

Bound: internal/controller/safety_test.go:sensor_read/enoent_skip
Bound: internal/controller/safety_test.go:sensor_read/eio_skip

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

## RULE-HWMON-SENTINEL-TEMP: temperature sentinel rejected at the backend read boundary

Raw sysfs temperature reads in millidegrees that match the 0xFFFF sentinel
(255500 millidegrees = 255.5°C) or exceed the 150°C plausibility cap MUST
be rejected by `IsSentinelSensorVal` before reaching the controller's sensor
map. A curve bound to a sensor returning 255.5°C would drive PWM to MaxPWM
on every tick — a safety bug on hardware that has no thermal runaway
protection.

Bound: internal/hal/hwmon/safety_test.go:sentinel/temp_rejects_255_5_degrees
Bound: internal/controller/safety_test.go:temp_sentinel_skipped_in_readAllSensors

## RULE-HWMON-SENTINEL-FAN: fan RPM sentinel rejected at the backend read boundary

Raw sysfs fan*_input reads of exactly 65535 RPM (the 0xFFFF nct6687 sentinel)
or any value above 25 000 RPM MUST be rejected by `IsSentinelRPM` in the hwmon
backend's Read() method and marked as an invalid reading (OK=false). The cap
is set above any real-world fan (consumer ≤ 4k, AIO pump ≤ 6.5k, server-class
Delta/Sanyo Denki 12–22k) and below the chip-glitch sentinels. A calibration
sweep that records 65535 RPM as a curve point would produce a wildly incorrect
fan-speed model that misbehaves in closed-loop control. Pre-2026-05-03 the
cap was 10 000, which silently rejected legitimate server-fan readings.

Bound: internal/hal/hwmon/safety_test.go:sentinel/fan_rejects_65535_rpm

## RULE-HWMON-SENTINEL-VOLTAGE: voltage sentinel rejected at the backend read boundary

Raw sysfs in*_input reads that exceed 20 V after the millivolts-to-volts
scale (÷1000) MUST be rejected by `IsSentinelSensorVal`. The 0xFFFF sentinel
at 65535 mV = 65.535 V exceeds every standard PSU rail. A control loop
driven by a 65 V "voltage" reading would produce garbage PWM outputs.

Bound: internal/hal/hwmon/safety_test.go:sentinel/voltage_rejects_implausible

## RULE-HWMON-INVALID-CURVE-SKIP: a curve tick with an invalid sentinel reading carries forward the last good PWM

When the sensor bound to a curve returns a sentinel or implausible value
(recorded in sentinelBuf by readAllSensors), the controller tick MUST NOT
evaluate the curve. Instead, it must write the last known good PWM value
(c.lastPWM) and return. This prevents a 255.5°C sentinel from driving PWM
to MaxPWM — the "loud-on-data-loss" fallback used for ENOENT/EIO is NOT
appropriate here because the chip is alive but glitching.

Bound: internal/controller/safety_test.go:sentinel/invalid_reading_carries_forward_pwm

## RULE-HWMON-PROLONGED-INVALID-RESTORE: after 30s of consecutive sentinel readings call watchdog.RestoreOne

If a sensor bound to a fan's control curve has returned sentinel or
implausible values for a continuous period of 30 seconds (tracked in
sensorInvalidSince), the controller MUST call watchdog.RestoreOne(pwmPath)
to hand the fan back to firmware auto-control. Staying on a frozen lastPWM
indefinitely when the sensor chip appears to be dead is a latent thermal
risk; firmware auto is safer than any daemon-chosen value under those
conditions.

Bound: internal/controller/safety_test.go:sentinel/prolonged_invalid_triggers_restore

## RULE-HWMON-SENTINEL-FIRST-TICK-IMMEDIATE-RESTORE: sentinel on the first tick before any valid reading calls watchdog.RestoreOne immediately

When the sentinel gate fires on the very first tick after daemon startup
(hasLastPWM is false -- no successful write has ever completed for this
channel), the controller MUST call watchdog.RestoreOne(pwmPath) immediately
rather than entering the 30s carry-forward window. With no last-known-good
PWM to carry forward, the 30s window would leave the fan in an operationally
ambiguous state at whatever duty cycle the firmware left it. Firmware auto is
the correct and immediate fallback when the sensor glitches before the first
valid reading settles.

Bound: internal/controller/safety_test.go:sentinel/first_tick_no_lastPWM_restores_immediately

## RULE-HWMON-SENTINEL-STATUS-BOUNDARY: sentinel values rejected at every serialization boundary, not only at the read source

The nct6687 (and similar super-I/O chips) can transiently return 0xFFFF from
registers in mid-latch. After scaling, these appear as 255.5°C (temp*_input),
65535 RPM (fan*_input), or 65.535 V (in*_input). The filter must be applied at
EVERY code path that reads hwmon values and serialises them into JSON or
persists them to in-memory state — not only at the primary read source.

Specifically, monitor.Scan() (which feeds GET /api/hardware) must call
isSentinelMonitorVal and skip sentinel / implausible readings before
appending them to the result slice. A reading suppressed at the scan boundary
must not appear in the Device.Readings slice at all. Valid readings on the
same chip must still appear.

Bound: internal/monitor/monitor_test.go:TestRegression_Issue460v2_SentinelSuppressedAtScanBoundary

## RULE-HWMON-READALLSENSORS-PASSTHROUGH: a valid sensor reading must not be filtered by the sentinel gate

`readAllSensors` must place valid sensor values into the sensor map and must
NOT record them in the sentinel buffer. A sentinel filter that produces false
positives — rejecting a real 45°C reading as a sentinel — would cause the
controller to carry forward a stale PWM value and sever the thermal control
loop on otherwise healthy hardware. The acceptance contract is tested
symmetrically alongside the rejection contract so that a change to the
plausibility thresholds that creates false positives fails immediately.

Bound: internal/controller/safety_test.go:temp_valid_passes_through_readAllSensors
