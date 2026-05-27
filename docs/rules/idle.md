# Idle-gate rules

These invariants govern `internal/idle/`, the soft / strict idle
predicate used by the wizard's StartupGate (calibration sweep
pre-condition) and the opportunistic prober's per-tick gate.
Battery, container, blocked-process, post-resume warmup, and
storage-maintenance refusals live here. PSI is the primary
workload signal; `/proc/loadavg` is the fallback.

Each rule below binds 1:1 to a subtest. If a rule text is edited,
update the binding subtest in the same PR; if a new rule lands,
it must ship with a matching subtest or `tools/rulelint` blocks
the merge.

## RULE-IDLE-01: StartupGate requires the idle predicate to be TRUE for ≥ 300 s (durability window) before returning ok=true.

`StartupGate(ctx context.Context, cfg GateConfig) (ok bool, reason Reason, snap *Snapshot)`
polls the idle predicate at `cfg.TickInterval` intervals. It
tracks a consecutive-true duration and only returns
`(true, ReasonOK, snap)` once that duration reaches
`cfg.Durability` (default 300 s). A single true tick does not
satisfy the gate — the idle state must be *sustained* to ensure
the system is genuinely quiescent rather than between bursts of
activity. On context cancellation before the durability window
is met, the gate returns `(false, reason, nil)`. A false tick
resets the consecutive-true counter to zero. Skipping
durability and returning on the first true tick would allow a
calibration to start during a workload pause, producing
incorrect fan curves.

Bound: internal/idle/idle_test.go:TestRULE_IDLE_01_StartupGate_DurabilityRequired

## RULE-IDLE-02: Battery-powered operation (AC offline or BAT discharging) is a hard refusal — AllowOverride has no effect.

`evalPredicate(snap *Snapshot, cfg GateConfig) (bool, Reason)`
returns `(false, ReasonOnBattery)` when `snap.OnBattery` is
true, regardless of `cfg.AllowOverride`. `RuntimeCheck`
propagates the same refusal. Detection reads
`/sys/class/power_supply/AC*/online` (AC offline → on battery)
and `/sys/class/power_supply/BAT*/status` (value `"Discharging"`
→ on battery). Envelope C calibration sweeps the fan PWM across
its full range and requires mains power; running it on battery
causes premature shutdown mid-sweep, partial calibration
records, and fan curves shaped by thermal transients from
battery discharge itself. The hard refusal cannot be suppressed
by any flag because the risk is physical, not operational.

Bound: internal/idle/idle_test.go:TestRULE_IDLE_02_BatteryRefusal

## RULE-IDLE-03: Container environment is a hard refusal — AllowOverride has no effect.

`evalPredicate(snap *Snapshot, cfg GateConfig) (bool, Reason)`
returns `(false, ReasonInContainer)` when `snap.InContainer` is
true, regardless of `cfg.AllowOverride`. Container detection
reads `/proc/1/cgroup` and looks for runtime keywords (`docker`,
`lxc`, `kubepods`, `garden`). Inside a container,
`/sys/class/hwmon` paths visible to ventd reflect the host
kernel but may be write-protected or inaccessible due to cgroup
device permissions; a calibration sweep that writes to a PWM
path in a container either silently no-ops or panics the host
kernel driver. The hard refusal cannot be overridden because
container isolation makes real fan-control verification
impossible from inside the container namespace.

Bound: internal/idle/idle_test.go:TestRULE_IDLE_03_ContainerRefusal

## RULE-IDLE-04: PSI is the primary load signal when /proc/pressure/ is available; /proc/loadavg is the fallback.

`Capture(deps snapshotDeps)` calls `PSIAvailable(procRoot)`
which checks whether `/proc/pressure/cpu` exists. When
available, `Snapshot.PSI` is populated from `cpu.some avg60`,
`io.some avg60`, and `memory.full avg60`; `evalPredicate` uses
the PSI fields as the primary workload signal. When
`/proc/pressure/` is absent (kernel < 4.20 or CONFIG_PSI=n),
`captureLoadAvg` reads `/proc/loadavg` and the first three
fields are used as the fallback signal. Using PSI as primary is
correct because PSI measures actual CPU, IO, and memory
pressure rather than queue length; a system with many sleeping
tasks can show high load average but zero PSI, and must not be
refused.

Bound: internal/idle/idle_test.go:TestRULE_IDLE_04_PSIPrimaryFallback
Bound: internal/idle/idle_test.go:psi_present_used_as_primary
Bound: internal/idle/idle_test.go:psi_absent_uses_loadavg_fallback

## RULE-IDLE-05: /proc/loadavg is read via direct file read, not getloadavg(3); no CGO is permitted.

`captureLoadAvg(procRoot string) [3]float64` reads
`<procRoot>/loadavg` with `os.ReadFile` and parses the first
three space-separated fields as float64. The package MUST NOT
call `getloadavg(3)` (a libc function) or import any CGO
symbol. CGO is incompatible with `CGO_ENABLED=0`, the
project-wide invariant for static binaries. The test verifies
that `captureLoadAvg` returns the correct 1min/5min/15min
values from a synthetic `/proc/loadavg` file, and asserts
`PSIAvailable` returns false when `/proc/pressure/cpu` is
absent — confirming the primary/fallback dispatch works without
real kernel state.

Bound: internal/idle/idle_test.go:TestRULE_IDLE_05_LoadAvgDirectRead

## RULE-IDLE-06: Process blocklist includes canonical R5 §7.1 entries and is extensible via SetExtraBlocklist.

`isBlockedProcess(name string) bool` returns true for every
process name in the base blocklist defined in §7.1 of
spec-v0_5_3: `rsync`, `restic`, `borg`, `ffmpeg`, `apt`, `dnf`,
and equivalent backup/transcoding/package-manager names.
`SetExtraBlocklist(names []string)` appends operator-specified
names to the base list; the extension takes effect for all
subsequent `isBlockedProcess` calls within the process
lifetime. A `nil` argument resets the extra list. The test
verifies that all canonical §7.1 names are blocked, that a
custom name added via `SetExtraBlocklist` is blocked, and that
an unrelated process name is not blocked. The blocklist gate
prevents Envelope C from starting while a backup or encode job
is running in the background.

Bound: internal/idle/idle_test.go:TestRULE_IDLE_06_ProcessBlocklist

## RULE-IDLE-07: RuntimeCheck computes a delta from the baseline snapshot; baseline-resident blocked processes do not cause refusal.

`RuntimeCheck(ctx context.Context, baseline *Snapshot, cfg GateConfig) (bool, Reason)`
captures a fresh `Snapshot` at call time and compares
`snap.Processes` against `baseline.Processes`. A process name
present in `baseline.Processes` is considered baseline-resident
and does NOT trigger a blocked-process refusal, even if the
name appears in the blocklist. Only processes that are NEW
since the baseline (present in the live snapshot but absent
from the baseline) cause a refusal. This prevents a
long-running backup job that was already underway when the
daemon started from permanently blocking Envelope C — the
baseline records pre-existing activity as acceptable, and only
new activity (user started a new workload) causes a refusal.

Bound: internal/idle/idle_test.go:TestRULE_IDLE_07_RuntimeCheckBaselineDelta

## RULE-IDLE-08: Backoff delay follows min(60×2^n, 3600) ± 20% jitter, with daily cap at n=12.

`BackoffDet(n int, randFloat func() float64) time.Duration`
computes the retry delay for the nth consecutive not-idle
report. The base interval is `min(60s × 2^n, 3600s)`
(exponential backoff capped at 1 hour). Jitter is `±20%`:
`delay × (1 + (randFloat()*2-1) × 0.2)`. At `n ≥ 12` the
function returns 0, signalling that the daily cap has been
reached and the caller should abandon the attempt for today.
The test verifies: n=0 with zero-rand gives `60s × 0.8 = 48s`;
n=6 is capped to `3600s × 0.8`; n=12 returns 0; and the upper
jitter bound at n=0 with near-max rand does not exceed
`60s × 1.2`. The daily cap prevents a permanently busy system
from hammering the process scanner on every tick.

Bound: internal/idle/idle_test.go:TestRULE_IDLE_08_BackoffFormula

## RULE-IDLE-09: AllowOverride=true skips storage-maintenance refusal but never skips battery or container refusal.

`CheckHardPreconditions(procRoot, sysRoot string, allowOverride bool) HardPreconditions`
evaluates three hard conditions: `OnBattery`, `InContainer`,
and `StorageMaintenance`. When `allowOverride` is true,
`StorageMaintenance` is set to false regardless of whether
`/proc/mdstat` shows an active RAID rebuild — the operator has
explicitly acknowledged the maintenance window risk. However,
`OnBattery` and `InContainer` are always set from the real
hardware state regardless of `allowOverride`; the `Reason()`
method returns `ReasonOnBattery` or `ReasonInContainer` for
those conditions even with the override flag active. This
asymmetry is intentional: storage maintenance is recoverable
(array rebuilds complete), but battery exhaustion and
container isolation are physical blockers with no safe
workaround.

Bound: internal/idle/idle_test.go:TestRULE_IDLE_09_OverrideNeverSkipsBatteryContainer
Bound: internal/idle/idle_test.go:override_rejected_on_battery
Bound: internal/idle/idle_test.go:override_rejected_in_container
Bound: internal/idle/idle_test.go:override_skips_storage_maintenance

## RULE-IDLE-10: StartupGate returns a non-nil, populated Snapshot on success; snap.Timestamp is non-zero.

`StartupGate(ctx, cfg)` MUST return a non-nil `*Snapshot` as
its third return value when `ok == true`. The returned snapshot
represents the system state at the moment the durability
window closed — the last successful capture before the gate
unlocked. The `Snapshot.Timestamp` field MUST be non-zero. This
snapshot is passed directly to `RuntimeCheck` as the baseline
so that any process or workload present at calibration-start
is treated as baseline-resident (per RULE-IDLE-07). A nil
snapshot returned on success would force `RuntimeCheck` to
reconstruct the baseline from a new capture, losing the
baseline-resident exclusion and potentially refusing
immediately on a process that was already running when the
system became idle.

Bound: internal/idle/idle_test.go:TestRULE_IDLE_10_StartupGateReturnsSnapshot

## RULE-IDLE-11: HardPreconditions.Ok() is the exact inverse of Any().

`Ok()` returns `!Any()` — true only when none of the six hard
preconditions (battery, container, storage-maintenance,
blocked-process, boot-warmup, post-resume-warmup) is active. It is
the spec-v0_5_9 §2.5 form consumed by the `w_pred_system` global
gate: predictive control is permitted only while `Ok()` holds, so
the gate closes (every channel falls back to reactive) on battery,
in containers, during scrubs, and for the boot/resume warmup
windows.

Bound: internal/idle/idle_test.go:TestHardPreconditions_Ok
