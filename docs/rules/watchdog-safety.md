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

## RULE-WD-RESTORE-BUDGET: RestoreCtx parallelizes per-channel restore and honours a context deadline

`Restore()` calls `RestoreCtx(ctx)` with `context.WithTimeout(_,
DefaultRestoreBudget)` (1.8 s). `RestoreCtx` launches one goroutine per
registered entry, each calling `restoreOneCtx(ctx, e)` which pre-checks
`ctx.Err()` then dispatches to the backend via the `restoreOneImpl`
package-level seam (production: `(*Watchdog).restoreOne` — the existing
panic-recovery + per-entry restore wrapper, unchanged in behaviour).

Two return paths:

- **All goroutines complete within budget**: function returns as soon as
  the WaitGroup drains. Per-entry restores ran in parallel — a slow sysfs
  write on one fan does not stall the others. RULE-WD-RESTORE-EXIT and
  RULE-WD-RESTORE-PANIC continue to hold (every entry's goroutine reached
  its backend dispatch; per-entry panic-recover is unchanged).
- **Budget fires before all goroutines complete**: function applies a
  bounded `restoreGracePeriod` (100 ms) so abandoned goroutines have a
  chance to finish their log emit / microsecond-scale syscall returns,
  then snapshots the still-incomplete set, sorts it deterministically,
  emits one WARN ("watchdog: restore budget exceeded; abandoning in-flight
  goroutines") naming the abandoned channels + the cancellation cause,
  and returns. The abandoned goroutines continue to run until the kernel
  returns from the underlying syscall — but the daemon proceeds with its
  exit regardless. systemd's `KillMode=process` reaps them on shutdown.

This pattern means the daemon's own restore path is the load-bearing
2-second-promise primitive in the README's "every exit path restores
firmware control within two seconds" guarantee, rather than the daemon
relying on systemd's `WatchdogSec=2s` SIGKILL + `OnFailure=ventd-recover`
belt-and-braces. A hung sysfs write on one fan no longer blocks the
restore loop indefinitely — that was the failure mode where the heartbeat
goroutine would keep pinging while the restore goroutine was wedged,
defeating the SIGKILL backstop.

`DefaultRestoreBudget = 1800 ms` leaves 200 ms of headroom under typical
systemd `TimeoutStopSec=2s`; the budget is exposed as a public constant
so callers can tighten or relax it if their unit configuration differs.

Bound: internal/watchdog/safety_test.go:wd_restore_completes_within_budget
Bound: internal/watchdog/safety_test.go:wd_restore_budget_exceeded_logs_abandoned_continues_others
Bound: internal/watchdog/safety_test.go:wd_restore_pre_cancelled_ctx_skips_backend

## RULE-WD-HEARTBEAT-LIVENESS: the systemd watchdog ping is gated on control-loop progress, not free-running

`WatchdogSec=2s` is only a backstop if the heartbeat actually *stops* when the
daemon stops doing its job. A free-running `sdnotify.StartHeartbeat` pings
`WATCHDOG=1` unconditionally, so systemd sees a healthy daemon even if every
controller goroutine has wedged (e.g. a logic deadlock, or all controllers
blocked) — the fans sit at their last PWM forever and the SIGKILL backstop
never fires. This is the failure mode RULE-WD-RESTORE-BUDGET's per-channel
parallelism mitigates for a *single* hung sysfs write; this rule covers a
*total* control-loop stall.

`StartHeartbeat` takes an `alive func() bool` and pings only when it returns
true (nil ⇒ always ping, for tests / callers with no loop to gate on). The
daemon wires `alive` to `controlLoopAlive(readyState.LastSensorRead(), now,
livenessWindow, …)`:

- **`LastSensorRead().IsZero()` ⇒ alive.** Only the control tick calls
  `MarkSensorRead`, so a zero timestamp means monitor-only mode (no
  controllers) or pre-first-tick startup — neither has a control loop that can
  stall, and a startup hang is covered by systemd's start timeout, not the
  watchdog. The daemon must keep pinging in these states or it would be killed
  for having nothing to do.
- **a controller ticked within `livenessWindow` ⇒ alive.**
- **otherwise the loop has stalled ⇒ not alive:** the ping is withheld, so
  `WatchdogSec` elapses → SIGKILL → `OnFailure=ventd-recover` hands fans back
  to firmware (and, with the recover binary's `{2,99,0}` handback, to the BIOS
  curve rather than manual mode).

`livenessWindow = max(5 × PollInterval, watchdogStallFloor)` so a fast poll
interval can't make the watchdog trigger-happy on one slow tick, and a long
poll interval scales the window up. The window bounds the worst-case
stall-to-restart latency rather than leaving it unbounded.

Bound: internal/sdnotify/sdnotify_test.go:TestStartHeartbeat_WithholdsPingWhenNotAlive
Bound: cmd/ventd/watchdog_liveness_test.go:TestControlLoopAlive

## RULE-WD-REGISTER-IDEMPOTENT: startup origEnable survives re-registration

A Register call stacks on top of the startup registration for the same
pwmPath (per-sweep registration pattern). The startup entry's origEnable
— captured once at first Register from the pre-daemon pwm_enable — must
be preserved across any subsequent Register + Deregister cycle for the
same path. The daemon-exit Restore reads the startup entry, not a sweep
entry, so the original value must still be there when the daemon shuts
down.

Bound: internal/watchdog/safety_test.go:wd_register_preserves_startup_origenable

## RULE-WD-PER-SYSCALL-DEADLINE: register reads + NVML resets are bounded by per-syscall deadlines so a hung driver cannot block daemon startup or restore beyond budget

Three call sites use the abandon-on-deadline pattern:

- **Register-time pwm_enable read** (`readPWMEnableWithDeadline` in
  `internal/watchdog/deadline.go`): caps `os.ReadFile` at
  `DefaultRegisterDeadline = 750 ms`; on deadline the goroutine is
  abandoned and origEnable falls back to `SafePreDaemonEnable`.
- **Restore-path ctx-cancel** (`restoreOneCtx` + `RestoreCtx`
  select): pre-checks ctx before backend dispatch; on budget
  overrun the parent select returns regardless of inner-goroutine
  state.
- **NVML reset wrapper** (`nvmlResetWithDeadline` in
  `internal/hal/nvml/backend.go`): caps
  `nvmlDeviceSetDefaultFanSpeed_v2` at `NVMLResetDeadline = 500 ms`.
  NVML exposes no per-call cancellation primitive; the wrapper is
  the only safe way to bound a hung-driver blast radius.

Pattern: `select { case res := <-done: case <-ctx.Done(): }`. On
`<-ctx.Done()` the caller returns wrapped `context.DeadlineExceeded`;
the inner goroutine continues until its kernel syscall returns and
is then reaped by systemd's `KillMode=process` at daemon shutdown.
Same abandonment model as RULE-WD-RESTORE-BUDGET, per-syscall
instead of per-channel.

See `docs/rules-rationale/opportunistic-soft-idle-and-watchdog-deadlines.md`
for the audit pass-6 motivation (#1038, #1040-#1042).

Bound: internal/watchdog/safety_test.go:wd_per_syscall_deadline_register_read_abandoned
Bound: internal/watchdog/safety_test.go:wd_per_syscall_deadline_write_does_not_leak_past_parent
Bound: internal/hal/nvml/backend_test.go:TestRULE_WD_PER_SYSCALL_DEADLINE_NVMLResetAbandoned
Bound: internal/hal/nvml/backend_test.go:TestRULE_WD_PER_SYSCALL_DEADLINE_NVMLResetSuccessPassthrough
Bound: internal/hal/nvml/backend_test.go:TestRULE_WD_PER_SYSCALL_DEADLINE_NVMLResetBackendIntegration

## RULE-WD-PRIOR-CRASH-FALLBACK: Register treats live pwm_enable=1 as a prior-crash residual and overrides origEnable to BIOS auto

Per audit-pass-6 issue #1039, the pre-fix Register captured the
live `pwm_enable` value verbatim at startup. After a prior daemon
crash the chip might still be in manual mode (pwm_enable=1) —
the crashed daemon never restored — so the next daemon's Register
captures origEnable=1 and on subsequent Restore writes 1 back,
leaving the fan in manual mode with whatever PWM byte the crashed
daemon last wrote (often 0, always wrong).

`applyPriorCrashFallback` rewrites the value at capture time:

1. **Live read = 1 (manual)**: treat as prior-crash residual. If
   the watchdog has a `LastKnownStore` (production wires this to
   `state.KVDB`), consult the persisted pre-daemon value under the
   channel's stable-identity key — `watchdog.<chipName>.<busAddr>.pwm<idx>.preDaemonEnable`
   (#1331). The legacy `watchdog.<pwmPath>.preDaemonEnable` key is
   still honoured on first read so pre-upgrade entries are picked
   up and migrated forward by the next `SetPreDaemonEnable`. If
   neither key resolves, install `SafePreDaemonEnableSequence` on
   the entry as the prior-crash fallback walker — Restore will
   walk it on EINVAL and persist the winner back (#1332). One
   operator-facing WARN identifies the path taken.

2. **Live read = legitimate (any non-1 value)**: capture verbatim
   AND persist to the `LastKnownStore` under the stable-identity
   key for future prior-crash recovery.

3. **Read failed (-1)**: unchanged — the restore-time PWM=255
   fallback covers this case (RULE-WD-FALLBACK-MISSING-PWMENABLE).

The `LastKnownStore` interface is narrow (two methods, both keyed
by `ChannelIdentity`) so production callers wrap `state.KVDB`
without exposing the wider KV surface to the watchdog package. A
nil store is equivalent to "no persistence" — every prior-crash
path then routes through `SafePreDaemonEnableSequence` and Restore
walks it inside the backend's EINVAL handler.

**`SafePreDaemonEnableSequence = []int{2, 99, 0}`**: ordered list
Restore tries when the prior-crash branch had no persisted value.
`2` is the de-facto userspace convention (hits ~all in-tree
drivers on the first write); `99` is the historic SuperIO
placeholder used by NCT6687D pre-#169 and other vendor drivers
that pick a "deliberately weird" auto value (the kernel ABI
defines `2+` as a range, not a single value); `0` is the ABI
"no fan speed control / full speed" last-resort safe stop. Do not
reorder per-chip — the same sequence everywhere keeps the
prior-crash fallback fragility-free. The first non-EINVAL write
wins and is persisted back via `OnEINVALRecovery`, so the next
prior-crash recovery skips the walk.

**Stable-identity migration**: pre-#1331 daemons keyed the store
by the full `/sys/class/hwmon/hwmonN/pwmM` path. `hwmonN` is
reallocated across rmmod+modprobe and any persisted value became
unreachable. Post-#1331 entries are keyed by `chip name` + bus
suffix + pwm index (e.g. `nct6687.2592.pwm1`) so the value
survives module reload. The KV store wrapper falls back to the
legacy key on first read and deletes it on first write.

Bound: internal/watchdog/safety_test.go:wd_register_live_enable_1_falls_back_to_bios_auto
Bound: internal/watchdog/safety_test.go:wd_register_with_store_recovers_last_known_good
Bound: internal/watchdog/safety_test.go:wd_register_legitimate_value_persists_to_store

## RULE-WD-IPMI-ROUTING: IPMI channels register through the watchdog so the cross-cutting Restore-on-exit contract covers them

Per audit-pass-6 issue #1043, IPMI fans were previously restored
only by the IPMI backend's own `Close` path — but the watchdog's
canonical exit defer (`defer wd.Restore()` in `cmd/ventd/main.go`)
never visited IPMI channels because they were not in the
watchdog's `entries` slice. This left the daemon's exit-path
promise ("every fan restored to firmware auto") silently untrue
for IPMI hosts.

`(*Watchdog).RegisterIPMI(channelID, restoreFn IPMIRestoreFn)`
binds a vendor-specific restore primitive to a channel ID. The
watchdog's existing per-entry envelope (panic-recover, budget,
ctx-cancel skip) wraps the call identically to hwmon and NVML
entries. The IPMI backend's `Restore` method remains the canonical
implementation; the watchdog routes the call from its canonical
exit path.

`cmd/ventd/ipmi_watchdog.go::registerIPMIWatchdogEntries`
enumerates IPMI channels via the HAL registry at daemon start and
registers each one with the watchdog. A future IPMI vendor (HPE
iLO Advanced licence, custom OEM) automatically picks up the
watchdog's safety guarantees by extending the IPMI backend's
restore dispatch — no change to the watchdog package required.

Bound: internal/watchdog/safety_test.go:wd_register_ipmi_routes_restore_through_watchdog

## RULE-WD-RECOVER-HANDBACK: the standalone crash-recovery path hands fans to firmware auto, never to manual mode

`ventd-recover` (the `OnFailure=ventd-recover.service` oneshot,
`cmd/ventd-recover`) and `hwmon.RecoverAllPWM` (the in-process
`ventd -recover` flag) fire *after* the daemon has already died on an
uncatchable exit (SIGKILL, OOM, escaped panic, hardware-watchdog timeout) —
the in-daemon watchdog `Restore` never ran. These paths have no access to the
per-channel captured `origEnable` (it died with the daemon) and must make a
blind, best-effort hand-back.

They MUST NOT write `pwm_enable=1`. Per RULE-WD-PRIOR-CRASH-FALLBACK, `1` is
**manual** mode: on most super-I/O chips (NCT6687, ITE IT87xx, Nuvoton) it
pins the fan at whatever PWM byte the crashed daemon last wrote — often
near-zero mid-spin-down or mid-calibration — instead of returning control to
firmware. Writing `1` here reintroduces, on the emergency path, exactly the
residual-manual bug #1039 fixed on the graceful `Register` path (#1434).

Both paths walk `{2, 99, 0}` — the same ordering as
`watchdog.SafePreDaemonEnableSequence` (`2` = automatic, the de-facto
convention; `99` = SuperIO placeholder some vendor drivers use for auto incl.
NCT6687D pre-#169; `0` = ABI full-speed last-resort). The first value the chip
accepts without `EINVAL` wins; a non-`EINVAL` error (EACCES/EIO/device-gone)
aborts the channel because no other value would land either. The standalone
binary keeps a *local copy* of the sequence — it must not import
`internal/watchdog` (binary-size budget, `TestVentdRecover_BinarySize`); a
guard test asserts the copy never contains `1`. Reading the persisted
`LastKnownStore` value from the standalone binary so it can prefer the captured
pre-daemon value over the blind `{2, 99, 0}` walk is a future enhancement;
`{2, 99, 0}` is exactly the documented fallback when no stored value resolves.

Bound: internal/hwmon/recover_test.go:TestRecoverAllPWM_HandsBackToFirmwareAutoNotManual
Bound: cmd/ventd-recover/recover_test.go:TestRestoreAll_NeverWritesManualMode

## RULE-WD-RESTORE-REGISTRY-BACKEND: non-hwmon registry backends restore through their own HAL backend, not hwmon

`restoreOne` special-cases nvidia, msi-ec, and IPMI, then falls to a default
branch. Before this rule the default branch ALWAYS routed to the hwmon backend
— which is correct only for true hwmon fans. Every other registry backend
(amdgpu, corsair, thinkpad, pwmsys, legion, crosec, asahi, lenovoideapad, nbfc)
carries a backend-private channel ID as its `pwmPath` — a DRM card path, a USB
HID index, a procfs node — not a hwmon `pwm<N>` file. The hwmon restore then
wrote `pwm_enable=2` to a nonexistent `<id>_enable` path, the error was
swallowed, and the channel was **silently left in manual mode on exit**. For a
GPU that means the card stays in manual after the daemon dies, with no firmware
thermal fallback until something resets it — a real overheat risk.

The default branch now, for any `fanType` other than `"hwmon"`/`""`, looks the
backend up in the HAL registry (`hal.Backend`), rebuilds the channel by
matching `pwmPath` against the backend's `Enumerate` output, and restores
through that backend's own `Restore` (amdgpu → `pwm_enable=2` / fan_curve reset;
corsair → its HID restore; etc.). A channel that can't be found is logged, not
silently mis-restored. Only true hwmon fans, and types whose backend failed to
register, fall through to the hwmon path. This is what makes the amdgpu backend
(and every other registry backend) safe to leave under ventd control: the
exit-restore contract (RULE-WD-RESTORE-EXIT) now actually reaches them.

Bound: internal/watchdog/registry_restore_test.go:TestRestore_RegistryBackendRestoredViaOwnBackend
