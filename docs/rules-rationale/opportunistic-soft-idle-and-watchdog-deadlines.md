# Soft-idle gate + watchdog per-syscall deadlines — rationale

This document carries the historical context + design exposition for:
- RULE-OPP-IDLE-SOFT-MODE (in `docs/rules/opportunistic.md`)
- RULE-WD-PER-SYSCALL-DEADLINE (in `docs/rules/watchdog-safety.md`)
- RULE-WD-PRIOR-CRASH-FALLBACK (in `docs/rules/watchdog-safety.md`)

## Why the soft-idle gate exists (RULE-OPP-IDLE-SOFT-MODE)

### The v0.5.x structural lock-out

Phase C5 HIL field-validation (issue #1024, desktop + Proxmox soak
verdicts) confirmed the v0.5.x strict evaluator structurally prevented
smart-mode from advancing under realistic workload. The 600 s sustained-
idle durability requirement plus the calibration-grade PSI thresholds
(`cpu.some avg60 > 1.0 %`) closed the gate > 99 % of ticks on hosts
running Tdarr (desktop) or LXC containers (Proxmox), producing zero
Layer-B RLS updates over ~36 hours of cumulative observation.

Hypothesis chain:
```
realistic workload (any class)
  → RULE-OPP-IDLE-01..04 closed > 99 % of ticks
  → opportunistic probes never fire (RULE-OPP-PROBE-01)
  → no Δpwm-on-i-while-zero-on-j events
  → RULE-CMB-OAT-01 admits zero samples to Layer-B RLS
  → Snapshot.WarmingUp stays true forever
  → predictive controller path is structurally locked out
```

### The relaxed-threshold rationale

The soft thresholds are calibrated against the v0.6 RFC #1024 desktop
trace: cpu.some avg60 spent the majority of every 60 s window between
2-8 % during Tdarr transcoding lulls (strict refuses above 1.0 %; soft
admits up to 10.0 %). 10 % is operationally meaningful — a system where
10 % of tasks stalled on CPU in the last 60 s is genuinely busy; 5-8 %
is the "between transcoding tasks" window the v0.6 ship plan targets.

Memory ceiling unchanged from strict (0.5 %) — memory pressure is a
physical signal workload lulls don't change. Loadavg fallback scales
from `0.10 × ncpus` (strict) to `0.5 × ncpus` (soft) for kernels
without PSI.

### Hard guards preserved across modes

All four RULE-OPP-IDLE-01..04 hard preconditions remain checked first
regardless of Mode: battery (RULE-IDLE-02), container (RULE-IDLE-03),
scrub, blocked-process (RULE-IDLE-06), post-resume warmup. Process
blocklist (rsync / ffmpeg / make / apt) closes the gate. Input IRQ
delta (RULE-OPP-IDLE-02) fires in both modes; soft uses caller-owned
`IRQBaseline` for cross-tick state. Active SSH session
(RULE-OPP-IDLE-03) is single-shot loginctl parse, same in both modes.

### Operator escape hatch

`--strict-idle-gate` reverts the daemon to the v0.5.x evaluator for the
process lifetime. Mode is logged at construction time so journald audit
trails show which evaluator is active. `opportunisticDurability` (600s)
and the strict PSI constants remain in place as the strict-mode
contract.

### Single-shot timing contract

The soft evaluator's single-shot guarantee is tested directly: elapsed
wall-clock < 500 ms from gate entry to gate return on a clean fixture
(vs ~600 s for the strict loop). A regression that re-introduces a
durability loop in the soft path fails the bound subtest's elapsed-time
assertion.

### Zero-value-is-soft

Load-bearing for test construction: tests that instantiate an
`OpportunisticGateConfig` literal without setting `Mode` exercise the
soft evaluator. The bound subtest pins `ModeSoftIdle = 0` and
`ModeStrictIdle = 1` so a future regression that flips the enum cannot
silently revert the default.

## Watchdog per-syscall deadlines (RULE-WD-PER-SYSCALL-DEADLINE)

### Why per-syscall, not per-channel

RULE-WD-RESTORE-BUDGET applies the deadline at the channel level
(1.8 s parallel-restore budget). The per-syscall rule (audit pass-6
issues #1038, #1040-#1042) tightens the bound to individual sysfs reads
and NVML calls so a single wedged driver can't burn the entire
channel-level budget.

### Three call sites

- **Register-time pwm_enable read** (`readPWMEnableWithDeadline` in
  `internal/watchdog/deadline.go`): 750 ms cap on the per-channel
  `os.ReadFile` so a hot-plug or hung chip cannot block daemon startup
  indefinitely. On deadline the goroutine is abandoned and origEnable
  falls back to `SafePreDaemonEnable`. systemd's `KillMode=process`
  reaps the orphan at shutdown.

- **Restore-path ctx-cancel** (`restoreOneCtx`): pre-checks ctx before
  dispatching to backend; the per-syscall write happens inside the
  backend's restore path; on budget overrun `RestoreCtx`'s select
  observes `ctx.Done` and returns regardless of inner-goroutine state.

- **NVML reset wrapper** (`nvmlResetWithDeadline` in
  `internal/hal/nvml/backend.go`): 500 ms cap on
  `nvmlDeviceSetDefaultFanSpeed_v2`. NVML is dlopen'd at process start
  and exposes no per-call cancellation primitive; the wrapper is the
  only safe way to bound a hung-driver blast radius without crashing
  the daemon.

### Abandonment model

The pattern is `select { case res := <-done: case <-ctx.Done(): }` in
every helper. On `<-ctx.Done()` the caller returns
`context.DeadlineExceeded` (wrapped); the inner goroutine continues to
run until the kernel returns from its syscall. Same abandonment model
as RULE-WD-RESTORE-BUDGET, applied per-syscall instead of per-channel.

## Prior-crash fallback (RULE-WD-PRIOR-CRASH-FALLBACK)

### Why this exists

Audit pass-6 issue #1039: pre-fix Register captured the live
`pwm_enable` value verbatim. After a prior daemon crash the chip might
still be in manual mode (pwm_enable=1) — the crashed daemon never
restored — so the next daemon's Register captures origEnable=1 and on
subsequent Restore writes 1 back, leaving the fan in manual mode with
whatever PWM byte the crashed daemon last wrote (often 0, always wrong).

### `applyPriorCrashFallback` logic

1. **Live read = 1 (manual)**: treat as prior-crash residual. Consult
   `LastKnownStore` for the persisted pre-daemon value
   (`PreDaemonEnableKey(pwmPath) = "watchdog.<pwmPath>.preDaemonEnable"`);
   fall back to `SafePreDaemonEnable = 2` (BIOS auto) if no store. One
   operator-facing WARN identifies the path taken.
2. **Live read = legitimate (any non-1 value)**: capture verbatim AND
   persist to the store for future prior-crash recovery.
3. **Read failed (-1)**: unchanged — RULE-WD-FALLBACK-MISSING-PWMENABLE
   covers this case at restore time.

The `LastKnownStore` interface is narrow (two methods) so production
callers wrap `state.KVDB` without exposing the wider KV surface to the
watchdog package. A nil store is equivalent to "no persistence" — the
`SafePreDaemonEnable` fallback path covers all prior-crash cases.
