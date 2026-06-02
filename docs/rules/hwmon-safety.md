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

## RULE-HWMON-MODE-REASSERT-READBACK: throttled pwm_enable read-back re-asserts manual mode after a silent revert

RULE-HWMON-MODE-REACQUIRE handles the revert that surfaces as EBUSY on the
duty write. This rule handles the SILENT revert: a chip/BIOS that flips
pwm_enable back to firmware/auto WITHOUT erroring subsequent duty writes, and
resume-from-suspend, which commonly resets pwm_enable. In those cases EBUSY
never fires, so ventd would keep writing duty bytes the firmware silently
ignores until something else trips EBUSY — the fan drifts back to the BIOS
curve (best case) or stops (worst case) while the daemon reports itself
healthy. This is the real-world suspend/resume failure most Linux fan tools hit.

On every already-acquired channel, `ensureManualMode` MUST, at most once per
`ReassertReadbackInterval` per channel, read pwm_enable back and — if it is no
longer the manual value (1) — re-write `pwm_enable=1` and log at INFO. The
read-back is throttled (seeded at acquire time) so it costs one extra sysfs
read per channel per interval, not per tick. A read error (driver does not
expose pwm_enable — e.g. in-tree nct6683 for the NCT6687D) is "can't verify"
and skipped: those channels were acquired via the not-supported branch and
write duty directly regardless. This is the control-loop periodic re-write
foreshadowed by RULE-HWMON-MODE-REACQUIRE, and it works on every init system
(no systemd-sleep hook required), self-healing resume, BIOS-reassert-without-
EBUSY, and external `echo > pwm_enable` interference alike.

Bound: internal/hal/hwmon/backend_reassert_test.go:TestReassertIfReverted_SilentRevertReacquiresManualMode
Bound: internal/hal/hwmon/backend_reassert_test.go:TestReassertIfReverted_NoEnableFileIsSkipped

## RULE-HWMON-EBUSY-RATE-OBSERVABILITY: Backend tracks per-channel EBUSY rate in a 60s rolling window and emits escalating log levels at 5/min (WARN-storm) and 20/min (ERROR-escalation); EBUSYRates() exposes the snapshot and SetEBUSYObserver pushes each event to the doctor's ebusy_storm detector.

The observability ladder on top of RULE-HWMON-MODE-REACQUIRE: the
per-event re-acquire succeeds invisibly during a BIOS-reassertion
storm, so the daemon also has to surface the *rate*.

`Backend.recordEBUSY(pwmPath, writeErr)` is called inside
`Backend.Write` BEFORE the existing re-acquire retry, so the storm
shape is recorded even when the retry succeeds.

Constants (locked by `TestEBUSYRate_ThresholdConstantsLocked`):
- `EBUSYWindow = 60 * time.Second` — counter-reset window (resets to
  one when 60s elapses since the first event in the burst).
- `EBUSYWarnThreshold = 5` — escalates from one-off WARN to
  "storm detected" WARN.
- `EBUSYEscalateThreshold = 20` — escalates to ERROR with the
  operator-actionable BIOS-disable hint.

Log emission is debounced by `lastWarnedCount`: each threshold crossing
emits exactly one log line per window. Per-channel isolation via
`sync.Map[pwmPath]*ebusyStats` is load-bearing — one storming channel
must not pollute another's counter. A clock seam
(`Backend.SetClockForTest`) drives tests deterministically.

`Backend.EBUSYRates()` returns a per-channel snapshot, and
`Backend.SetEBUSYObserver(fn)` pushes the current snapshot to `fn` on every
recorded event. Because each controller constructs its own `Backend`, the
per-backend stats are unreachable from the aggregate doctor surface; the
daemon points every controller's observer at a single `internal/ebusy.Collector`
(via `controller.WithEBUSYObserver`), and the doctor's `ebusy_storm` detector
reads the collector's currently-active storms (RULE-DOCTOR-DETECTOR-EBUSY-STORM).
The collector ages out a channel once its rolling window closes, so a storm that
stopped no longer surfaces.

See `docs/rules-rationale/hwmon-runtime-monitors.md` for the audit-M17
recommendation and the deferred 100/min auto-fallback.

Bound: internal/hal/hwmon/backend_test.go:TestEBUSYRate_TracksWithinWindow
Bound: internal/hal/hwmon/backend_test.go:TestEBUSYRate_WindowResetAfterExpiry
Bound: internal/hal/hwmon/backend_test.go:TestEBUSYRate_NoEventsReturnsEmpty
Bound: internal/hal/hwmon/backend_test.go:TestEBUSYRate_PerChannelIsolation
Bound: internal/hal/hwmon/backend_test.go:TestEBUSYRate_ThresholdConstantsLocked
Bound: internal/hal/hwmon/backend_test.go:TestEBUSYObserver_NotifiedOnEachEvent
Bound: internal/ebusy/collector_test.go:TestCollector_ActiveStormsFiltersStaleAndSorts

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

## RULE-CTRL-LOWTEMP-DISCONNECT: an implausibly-low temp reading is treated as data-loss, never trusted as a cold chip

A disconnected thermistor / tach pin reads a real-looking but bogus low value
(~0–8 °C), which the sentinel filter (≥150 °C / ≤−273 °C) does NOT reject. If
the controller trusts it, the curve evaluates against a "very cold" chip and
computes minimum PWM — under-cooling a chip whose real temperature is unknown
and possibly hot. A running CPU / GPU / VRM / board sensor never legitimately
sits below the ambient floor (`hwmon.LowTempAmbientFloorCelsius`, 10 °C), so a
sub-floor reading on a **hwmon temp** sensor is almost certainly a dead pin.

`readAllSensors` therefore classifies a hwmon temp reading for which
`hwmon.IsLowTempLikelyDisconnected` holds as **data-loss** — the same class as
a sentinel — so it is dropped from the curve's sensor map and recorded in the
sentinel set. The existing sentinel path then carries forward the last good
PWM and, after the sentinel grace period, hands the fan to firmware (BIOS
curve, which reads the chip's own working sensor) rather than trusting the
bogus low value. The check is gated to temp paths (`strings.Contains(path,
"temp")`) so a legitimately low voltage / RPM reading is not misclassified, and
to hwmon types (nvidia/msiec self-validate and are not disconnected-pin prone).

Stuck-but-plausible sensors (a temp frozen mid-range while the chip heats) are
a separate, harder problem — they pass every per-sample check, so they can only
be caught across time and across sensors (variance/correlation detection,
false-positive prone). That is handled out-of-band, observability-only, by the
`stuck_sensor` doctor detector: see RULE-DOCTOR-DETECTOR-STUCK-SENSOR in
docs/rules/doctor.md.

Bound: internal/controller/lowtemp_disconnect_test.go:TestReadAllSensors_LowTempDisconnectedFlaggedAsSentinel
Bound: internal/controller/lowtemp_disconnect_e2e_test.go:TestTick_LowTempDisconnectCarriesForwardThenHandsBack

## RULE-CTRL-RECONCILE-STRANDED: on startup, hand back to firmware any fan left in manual mode that the current config no longer controls

ventd takes manual control of a fan by writing `pwm_enable=1`. If a later config
controls FEWER fans than an earlier one — a re-setup that admits a different
subset, an operator who removes a control, an upgrade that regenerates the
config — the dropped fans are left at `pwm_enable=1` from the earlier run. No
controller drives them, the watchdog never registered them (so even exit-restore
misses them), and they sit **frozen at the dead config's last PWM**, unresponsive
to temperature. This is the root cause behind "after re-running setup, some of my
fans stopped responding": the wizard admits a subset and the rest are stranded
in manual mode, indistinguishable from BIOS-curve fans until the chip cooks.

On startup (after the watchdog registers the controlled channels, before the
controllers spawn) the daemon calls `hwmon.ReconcileUnmanagedManual` with the
set of hwmon pwm paths it controls this run. For every hwmon chip that contains
at least one controlled channel — and ONLY those chips, never one ventd has
nothing to do with — it scans the chip's `pwm<N>_enable` files and, for any
channel reading manual (1) that is not in the controlled set, hands it back to
firmware auto via the `{2, 99, 0}` sequence (the same handback as the
crash-recovery path; never the manual value 1). The result: a fan ventd no
longer controls is returned to the BIOS curve instead of frozen. Controlled
channels, already-auto channels, and channels on unmanaged chips are untouched.

The same stranding can happen on a **live** reload, not just across a restart.
The reload path (SIGHUP / in-process) does not tear controllers down — it swaps
`liveCfg` and existing controllers pick up new parameters on their next tick. A
controller whose fan or curve is removed from the new config would otherwise
skip-tick forever (`controller.tick` logs "fan/curve not found in live config,
skipping tick"), leaving its fan frozen in manual mode at the dead config's last
PWM. So when a controller detects its binding has gone, `restoreOnUnbind` hands
that fan back to firmware auto **once** (via `watchdog.RestoreOne`, same `{2, 99,
0}` handback). The controller owns its channel, so this can't fight another
writer; `markTickCompleted` re-arms the one-shot guard when the binding returns,
so a fan removed, re-added, then removed again is handed back each time.

Bound: internal/hwmon/reconcile_test.go:TestReconcileUnmanagedManual
Bound: internal/controller/unbind_restore_test.go:TestRestoreOnUnbind

## RULE-HWMON-PUMP-FLOOR: pump fans never written below pump_minimum

Fans marked `is_pump: true` circulate coolant; spinning below a threshold
risks coolant stall and thermal damage. The controller must enforce a hard
floor at `pump_minimum` even when the configured MinPWM is lower or the
curve output is lower. This floor is applied as part of the clamp step and
takes precedence over every other limit.

Bound: internal/controller/safety_test.go:clamp/pump_floor_beats_curve

## RULE-CTRL-OVERTEMP-FAILSAFE: a curve-independent over-temperature backstop forces full speed near the critical limit

The curve, smart-mode blend, schedule, and operator `max_pwm` are all
trusted to keep a chip cool — but a mis-set curve, a sensor that under-reads,
a smart-mode under-cool, or a wrong schedule can leave a chip overheating with
the daemon happily holding a low PWM. There must be a backstop that does not
depend on any of those being correct.

After `computePWM` (so it overrides curve **and** smart blend **and** the
`max_pwm` clamp), the controller checks the control sensor against a
**curve-independent** engage temperature and, when crossed, forces `pwm=255`
(true full speed, bypassing the operator's `max_pwm` cap — at a genuine
thermal emergency, hardware survival beats the noise budget).

The engage temperature is resolved **once per control sensor** (cached) by
`resolveEmergencyEngageC`, from a source independent of the curve:

1. hwmon `tempN_crit` (the chip's throttle point), if a plausible value
   (`emergencyMinPlausibleC`..`emergencyMaxPlausibleC`) — rejects garbage
   registers (nct6687 thermistors reading 0 / −128 / 16);
2. else, for a **CPU-labelled** sensor with no crit (common on super-I/O CPU
   temps — the NCT6687D HIL host has no `coretemp`, so its CPU sensor exposes
   no crit), the **CPU-model Tjmax** (`sysclass.TjmaxFromCPUInfo`).

Engage temp = that base limit **+ `emergencyMarginC`**, capped at the chip's
`tempN_emergency` (shutdown line) when present. The margin above the throttle
point is what stops a **thermally-limited chip that operates at Tjmax by
design** (e.g. a power-limited desktop CPU that throttles to hold Tjmax under
load) from false-firing on every heavy workload — the failsafe engages only
when cooling has actually failed and temp climbs *past* the throttle point.

When no plausible threshold can be resolved the failsafe stays **disabled**
(returns 0) rather than inventing a low absolute that would false-fire — so on
hardware where neither crit nor a CPU-model Tjmax is available the behaviour is
exactly as before, never worse.

Engage and release are debounced by `emergencyDebounce` (reject transient
single-tick spikes) and separated by `emergencyReleaseC` hysteresis (no
flapping at the boundary), tracked **per sensor** (`emergencyState`) so each
sensor a fan watches keeps its own engage/release state.

The failsafe watches **every control sensor the bound curve reads**
(`failsafeSensorNames`): a single-sensor curve watches its one sensor; a **mix**
curve watches the union of the leaf sensors reachable through its sources
(recursively, deduped, cycle-guarded). The fan is forced to full speed while
**any** of those sensors is engaged — so a case fan on `max(cpu, gpu)` is forced
to full speed if **either** the CPU or the GPU crits, where before mix-curve
fans got no failsafe at all (#1442 follow-up). A sensorless, sourceless curve (a
fixed/manual-mode fan) watches no sensors and stays outside the failsafe by
design; a non-hwmon sensor (e.g. an NVML GPU temp) resolves no threshold and is
silently disabled there — neither path ever worse than before.

Bound: internal/controller/overtemp_test.go:TestOvertempForce_DebounceAndHysteresis
Bound: internal/controller/overtemp_test.go:TestResolveEmergencyEngageC_TjmaxFallbackForCPULabel
Bound: internal/controller/overtemp_test.go:TestTick_OvertempFailsafeEndToEnd
Bound: internal/controller/overtemp_test.go:TestTick_OvertempFailsafeNoFalseFireAtThrottlePoint
Bound: internal/controller/overtemp_mix_test.go:TestFailsafeSensorNames
Bound: internal/controller/overtemp_mix_test.go:TestTick_OvertempFailsafeFiresOnMixCurveLeafSensor
Bound: internal/controller/overtemp_shadow_test.go:TestTick_OvertempFailsafeObservedButSuppressedInShadowMode

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

## RULE-HWMON-SWAP-MONITOR: A long-running goroutine periodically re-resolves stored hwmon paths against their stable-device anchors; mismatches are surfaced at WARN level and dispatched to an optional SwapHandler.

The runtime complement to RULE-HWMON-INDEX-UNSTABLE's startup-time
rebase. hwmonN indices can shift mid-lifetime (USB GPU hotplug,
`modprobe -r/-i`, udev re-numbering post-suspend); without runtime
re-resolution the controller silently writes to a stale path until
daemon restart.

`hwmon.MonitorSwap(ctx, inputs, interval, logger, onSwap)` is the
goroutine entry point. Every `interval` it calls `ReResolveAll(inputs)`
and dispatches the optional `SwapHandler` per `Changed=true`
detection. Every detection is logged at WARN regardless of handler
wiring. `hwmon.ReResolveAll(inputs []ChannelInput) []SwapDetection`
is the pure helper; each `ChannelInput` carries `StoredPath`
(daemon-start sysfs path) and `StableDevice`
(boot-persistent parent from `hwmon.StableDevice`).

`DefaultSwapMonitorInterval = 10 * time.Minute`. Daemon-side wiring
is `startHwmonSwapMonitor(ctx, wg, channels, logger)` in
`cmd/ventd/smart_builders.go`; it skips channels with empty `PWMPath`
or empty `StableDevice` (NVML / IPMI / virtual). Zero eligible
channels = clean no-op, no goroutine registered.

The `SwapHandler` is wired in production (default-on via
`cfg.Hwmon.DynamicRebindEnabled()`, #1265): a detection signals an
in-process reload (`restartCh`) — the same path the uevent-driven
`rebindTrigger` uses — which re-resolves the config paths and respawns
the affected controllers at their new sysfs path (RULE-CTRL-REBIND-FOLLOW).
`SwapHandler` stays nil only when `dynamic_rebind=false` is set
explicitly, reverting to WARN-only observability.

See `docs/rules-rationale/hwmon-runtime-monitors.md` for the audit-M21
motivation, the interval-choice trade-offs, and the remap-dispatch
design.

Bound: internal/hwmon/swap_monitor_test.go:TestReResolveAll_NoSwapReportsUnchanged
Bound: internal/hwmon/swap_monitor_test.go:TestReResolveAll_SwapDetectedAndRebased
Bound: internal/hwmon/swap_monitor_test.go:TestReResolveAll_NoStableDeviceLeavesUnchanged
Bound: internal/hwmon/swap_monitor_test.go:TestMonitorSwap_StopsOnContextCancel
Bound: internal/hwmon/swap_monitor_test.go:TestMonitorSwap_FiresHandlerOnDetection
Bound: internal/hwmon/swap_monitor_test.go:TestMonitorSwap_EmptyInputsExitsCleanly
Bound: internal/hwmon/swap_monitor_test.go:TestMonitorSwap_NilHandlerStillLogs
Bound: internal/hwmon/swap_monitor_test.go:TestDefaultSwapMonitorInterval_Is10Min
Bound: cmd/ventd/main_hwmon_swap_monitor_test.go:TestStartHwmonSwapMonitor_SkipsWhenNoEligibleChannels
Bound: cmd/ventd/main_hwmon_swap_monitor_test.go:TestStartHwmonSwapMonitor_RegistersGoroutineForEligibleChannel

## RULE-CTRL-REBIND-FOLLOW: when a controlled fan's hwmon path moves at runtime, the daemon respawns its controllers at the new path instead of stranding the fan.

A running controller binds its fan by an immutable `pwmPath` captured at
construction (`findFanByPath(live, c.pwmPath, c.fanType)`). So when a renumber
rebases the config's `Fan.PWMPath` (RULE-HWMON-SWAP-MONITOR /
RULE-HWMON-INDEX-UNSTABLE), the live controller's `pwmPath` no longer matches any
fan — without intervention it would hit the RULE-CTRL-RECONCILE-STRANDED handback
and go inert, leaving the renumbered fan on the BIOS curve until a daemon restart.

The controllers run as a **cohort** under a context + waitgroup
(`controllerSpawner.ctx/cancel/wg`) derived from the daemon context but
cancellable on their own, so the web server, hwmon watcher, swap monitor, and
scheduler (on the daemon ctx/wg) are undisturbed. On a reload where
`anyControlledFanPWMChanged` reports a controlled fan's PWM path moved, the
`restartCh` handler:

1. drains the cohort (`drainCohort`: cancel + wait) — each controller runs
   `wd.Restore()` in its defer, so its fan is handed back to firmware before the
   controller is gone, and the old controller has fully exited before any
   respawn (the race guard: old and new never both write the channel);
2. for each moved fan, `wd.Deregister(oldPWM)` + `wd.Register(newPWM)` swaps the
   watchdog entry to the live path, and `cal.Abort(oldPWM)` cancels any in-flight
   calibration keyed by the vanished path (unmoved fans keep their startup
   entry — re-registering would stack a duplicate, since the watchdog uses LIFO
   duplicate entries for the calibration sweep lifecycle);
3. creates a fresh cohort (`newCohort`) and respawns every controller against the
   rebased config (`reconcile`).

A SIGTERM arriving mid-drain short-circuits the respawn (`ctx.Err()` guard); the
deferred `drainCohort` and the daemon's own `wd.Restore()` cover shutdown. A fan
with no `HwmonDevice` anchor can't be rebased: no respawn happens and a WARN tells
the operator to re-run setup (the residual RULE-CTRL-RECONCILE-STRANDED case).

Bound: cmd/ventd/rebind_follow_test.go:TestAnyControlledFanPWMChanged
Bound: cmd/ventd/rebind_follow_test.go:TestResolveHwmonPaths_ReportsFanRenumber
Bound: cmd/ventd/rebind_follow_test.go:TestResolveHwmonPaths_NoAnchorWarnsCannotFollow
Bound: cmd/ventd/rebind_follow_test.go:TestRebindFollow_ControllerFollowsRenumber

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
