# hwmon runtime-monitor rules — rationale

This document carries the historical context + design exposition for two
high-touch hwmon runtime monitors: the EBUSY rate-tracking ladder
(RULE-HWMON-EBUSY-RATE-OBSERVABILITY) and the hwmon-swap watchdog
(RULE-HWMON-SWAP-MONITOR). The invariants themselves live in
`docs/rules/hwmon-safety.md`.

## EBUSY rate ladder (RULE-HWMON-EBUSY-RATE-OBSERVABILITY)

### Why a ladder on top of MODE-REACQUIRE

RULE-HWMON-MODE-REACQUIRE is the single-event recovery primitive (one
re-acquire + retry on EBUSY). When the BIOS reassertion timer is tighter
than the controller's tick — Gigabyte Q-Fan / Smart Fan Control on
IT8xxx chips is the canonical case (issue #904) — the recovery succeeds
on every event but the underlying storm is invisible. Operators need to
see the rate, not just the per-event success.

### Audit recommendation (v0.5.26 senior review, M17)

> Add a per-channel `consecutiveEBUSY` counter; emit WARN at 5/min,
> doctor card at 20/min, fall back to firmware-auto at 100/min.

v0.5.40 shipped the rate-tracking + escalating logs + the `EBUSYRates()`
accessor seam. The "100/min firmware-auto" auto-fallback is intentionally
deferred — it introduces a state-machine surface (silenced channels,
cooldown re-entry, watchdog interaction) that needs HIL data before
shipping.

### Counter-reset vs sliding window

`EBUSYWindow = 60 * time.Second` is a counter-reset, not a true sliding
window. When `EBUSYWindow` elapses since the first event in the burst,
the counter resets to one and a fresh window begins. Cheaper than a
per-event ring buffer; adequate for "is this channel storming right now?".

### Log debouncing

Each threshold crossing emits exactly one log line per window, not one
per event. The 21st event in the same window is silent (the operator
already has the ERROR; no value in spamming). `lastWarnedCount` enforces
this.

### Per-channel isolation

Load-bearing: one channel suffering Q-Fan reassertion must not pollute
the counter for another well-behaved channel on the same chip. The
`sync.Map[pwmPath]*ebusyStats` shape gives each channel its own counter
+ window, protected by the inner `ebusyStats.mu` mutex.

### Test infrastructure

A clock seam (`Backend.SetClockForTest`) lets tests drive the rolling
window deterministically without `time.Sleep`. Production uses
`time.Now`.

### Doctor-detector consumption (future)

`Backend.EBUSYRates()` returns a snapshot keyed by `pwmPath`. A doctor
detector reading this map distinguishes:
- **Currently storming** — `WindowStart` within the last 60s AND
  `EventCount >= EBUSYWarnThreshold`. Surface as Warning recovery card.
- **Recently stormed, now quiet** — `WindowStart` older than 60s but
  counter hasn't reset. Surface as Info or skip.
- **Never stormed** — channel absent from the map.

## hwmon-swap runtime monitor (RULE-HWMON-SWAP-MONITOR)

### Why startup-time resolution isn't enough

RULE-HWMON-INDEX-UNSTABLE covers the startup-time contract: stored hwmon
paths are rebased via `hwmon.ResolvePath` once at daemon start. But
hwmonN indices can shift during a daemon's lifetime — USB GPU hotplug,
`modprobe -r/-i` reload of a Super-I/O driver, udev re-numbering chips
post-suspend. Without runtime re-resolution the controller silently
writes to a stale path until the next daemon restart.

### Doctor surface vs runtime monitor

The pre-v0.5.41 doctor surface (RULE-DOCTOR-DETECTOR-HWMONSWAP) catches
the same condition reactively — but only on the next periodic doctor
sweep, which can be minutes away. M21 from the v0.5.26 senior review
proposed a real-time runtime monitor; v0.5.41 shipped it.

### Default interval choice

`DefaultSwapMonitorInterval = 10 * time.Minute`. Module reloads and
hotplug events are rare; a 10-minute cadence keeps the syscall overhead
negligible while bounding worst-case detection latency to a real-time
remediation window. Operators that need tighter detection can pass a
shorter interval to MonitorSwap directly.

### Remap dispatch (RULE-CTRL-REBIND-FOLLOW)

The `SwapHandler` is wired in production (default-on via
`cfg.Hwmon.DynamicRebindEnabled()`, #1265). It does not surgically mutate
controller caches in place — that would mean racing live ticks while
rewriting `pwmPath`, the watchdog entry, the polarity channel, and the
calibration key under a running goroutine. Instead it reuses the existing
in-process reload: a detection signals `restartCh` (the same channel the
uevent-driven `rebindTrigger` uses), and the reload handler **respawns the
affected controllers** at the rebased paths.

The controllers run as a cohort under their own context + waitgroup
(derived from the daemon ctx, cancellable on their own). When a controlled
fan's PWM path moved, the handler drains the cohort (each controller
restores its fan to firmware on exit — so the old controller is gone before
the new one starts, the race guard), swaps the moved fans' watchdog entries
(`Deregister` old + `Register` new) and aborts any in-flight calibration on
the old path, then respawns every controller under a fresh cohort against
the rebased config. `Manager.RemapKey` (called by `resolveHwmonPaths`) moves
the stored calibration results across the same rename.

Earlier (v0.5.41) the handler was nil and the daemon kept writing to the
stale path until a manual restart — operator awareness lag. That gap is
closed: the only residual stranded case is a fan with no `HwmonDevice`
anchor, which can't be rebased; it gets a WARN to re-run setup.

### Daemon-side wiring (`startHwmonSwapMonitor`)

In `cmd/ventd/smart_builders.go`. Iterates
`[]*probe.ControllableChannel`, skipping channels with empty `PWMPath`
or empty `hwmon.StableDevice(pwmPath)` (NVML, IPMI, virtual devices that
don't expose a chip-parent symlink). Spawns one MonitorSwap goroutine
when the resulting slice is non-empty; a nil channel slice or zero
eligible channels short-circuits cleanly.
