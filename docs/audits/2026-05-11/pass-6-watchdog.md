# Pass 6: internal/watchdog/ deep read

**Files audited:** `internal/watchdog/watchdog.go` (the only non-test .go in the package)
**LOC:** 341 (non-test); 1168 incl. tests
**Time on task:** ~30 min
**Baseline commit:** `b46c1a5` (docs(audit): comprehensive ghost-code sweep)

The package has a single non-test source file. `restore.go` does not exist —
all state, dispatch, and per-channel logic lives in `watchdog.go`. The bound
subtests are in `safety_test.go`; `watchdog_test.go`, `watchdog_restore_test.go`,
and `restore_matrix_test.go` provide adjunct coverage and were read for context
but not audited.

The audit focused on the senior review's T1 concerns: RestoreCtx coverage,
per-channel goroutine bounding, the 1.8 s budget's actual mechanism, per-write
deadlines on `os.WriteFile`, NVML/IPMI hang exposure, and the `restoreOneImpl`
seam. Backends (`internal/hal/hwmon/backend.go`, `internal/hal/nvml/backend.go`)
were also opened where they're load-bearing for watchdog behaviour.

## Critical findings  (safety, restore-promise violation)

### C1. Abandoned restore goroutines leak past daemon exit; the 1.8 s budget bounds the *wait*, not the syscall

**File:** `internal/watchdog/watchdog.go:195-263`, in conjunction with
`internal/hwmon/hwmon.go:54-61` (`WritePWM`), `:86-100` (`WritePWMEnable`),
and `internal/hal/nvml/backend.go:129` (`nvidia.ResetFanSpeed`).

`RestoreCtx` correctly fans out one goroutine per entry, then waits with
`select { <-done; <-ctx.Done() }`. On budget timeout it logs the abandoned
channels and returns — but the abandoned goroutine is **still running**,
blocked inside `os.WriteFile` against a wedged sysfs driver (no `O_NONBLOCK`,
no per-fd deadline). The rule text (`watchdog-safety.md`,
RULE-WD-RESTORE-BUDGET) acknowledges this: "The abandoned goroutines continue
to run until the kernel returns from the underlying syscall — but the daemon
proceeds with its exit regardless. systemd's `KillMode=process` reaps them on
shutdown."

The danger: when `cmd/ventd/main.go:800` runs `defer wd.Restore()` during a
SIGTERM handler that ALSO closes other resources (state DB, log handles), an
abandoned goroutine can race against the `defer` ordering and write into a
half-torn-down logger. Look at line 256–260: the WARN that names the
abandoned channels is emitted on `w.logger`, which the abandoned goroutine
may also reach via `restoreOne`'s recovery path or the backend's own Info
log. Concurrent slog handler writes after `RestoreCtx` returns is a real
hazard that the test `wd_restore_budget_exceeded_logs_abandoned_continues_others`
hand-waves with the `restoreGracePeriod` (100 ms grace at line 270).

The grace covers the *cooperative* abandonment (goroutine completes a few
ms late). It does **not** cover the genuine driver-wedge case the rule was
written for. RULE-WD-RESTORE-EXIT ("every documented exit path restores")
is structurally violated for any channel whose sysfs write doesn't return —
that channel never gets its pwm_enable restored, and the rule's bound subtest
only verifies the bookkeeping (`abandoned_channels` log line), not the
hardware-state outcome.

**Suggested action:** Document explicitly in the user-facing docs that the
1.8 s budget does NOT guarantee the *fan* is restored on a wedged driver —
only that the daemon's exit is bounded. Consider a follow-up using
`os.OpenFile(path, O_WRONLY|O_NONBLOCK, 0)` + `unix.SetDeadline` /
`syscall.Write` so the syscall itself can be aborted. NVML offers no such
hook; document that as accepted limitation.

### C2. `origEnable` captured at Register time uses the live current value — a prior-crash residual leaves the fan in manual mode after Restore

**File:** `internal/watchdog/watchdog.go:106, :116`.

On startup, `Register` calls `hwmon.ReadPWMEnable(pwmPath)` to capture
`origEnable`. If the previous daemon instance crashed without restoring
(SIGKILL, OOM, kernel panic — exactly the failure modes the watchdog is
defence against), the chip is still in `pwm_enable=1` (manual). The new
daemon captures `origEnable=1` as the "original". On exit, Restore writes
`pwm_enable=1` back — the fan stays in manual mode at whatever last PWM the
new daemon wrote, instead of being handed back to BIOS auto (`pwm_enable=2`,
the actual pre-ventd state).

This violates the user-facing promise in `watchdog.go:36-49`: "Restore...
runs from the defer chain... fan stays at the last-written PWM" — which is
described as the SIGKILL non-cover, but here it leaks into the *graceful*
exit path too because of stale state at startup.

The fix is non-obvious: there's no on-disk record of the *true* pre-ventd
`pwm_enable` value. Two viable options: (a) persist the very-first
`origEnable` capture to state KV under namespace `watchdog/orig-enable/<path>`
and refuse to overwrite it on subsequent boots (RULE-WD-REGISTER-IDEMPOTENT
already preserves this in-memory, but not across crashes); (b) on startup,
if `origEnable == 1` (manual), assume crash-recovery and force-restore to
`pwm_enable=2` (BIOS auto) — the "fail loud, fail safe" approach the
fallback branch at line 269 already takes for `origEnable < 0`.

**Suggested action:** File issue. Option (a) is the more correct fix; the
KV namespace already exists in the state package. The bound subtest
`wd_register_preserves_startup_origenable` would extend cleanly to cover the
"second Register after a simulated crash uses persisted origEnable, not the
stale live value" case.

## High findings      (correctness bug, hang risk)

### H1. NVML restore has no deadline OR cancellation hook — driver hang is unbounded inside the goroutine

**File:** `internal/hal/nvml/backend.go:129` (`nvidia.ResetFanSpeed(idx)`),
called from `internal/watchdog/watchdog.go:340` via `be.Restore(ch)`.

`ResetFanSpeed` is a synchronous purego dlopen-style call into
`libnvidia-ml.so.1`'s `nvmlDeviceSetDefaultFanSpeed_v2`. The library function
has no context parameter and cannot be cancelled. A wedged NVIDIA driver (a
documented failure mode after `Xid` errors) blocks the abandoned goroutine
indefinitely. The 1.8 s budget unblocks the daemon's *wait*, but per C1
above, the goroutine is alive and can race writes to `w.logger` after exit.

The NVML lib's `Init` path (`nvidia.InitWithDeadline`,
`internal/nvidia/init_deadline_test.go`) DOES bound the dlopen — but that's
init only, not the per-call surface. The watchdog has no equivalent for
Restore.

**Suggested action:** Either accept and document, or add a wrapper that runs
ResetFanSpeed inside a goroutine with a per-call timeout (the same trick
InitWithDeadline uses), surfacing `ErrNVMLTimeout` on overrun. The NVML
call's typical latency is single-digit ms; a 1 s per-call cap is generous.

### H2. `restoreOneCtx`'s ctx-precheck guards only the *dispatch*, not the *call* — a goroutine that passes the precheck races the deadline with no further checks

**File:** `internal/watchdog/watchdog.go:277-285`.

```go
func (w *Watchdog) restoreOneCtx(ctx context.Context, e entry) {
    if err := ctx.Err(); err != nil { ... return }
    restoreOneImpl(w, e)
}
```

Only one ctx check, at the top. Once dispatched, the underlying backend
write does not consult ctx. So the bound subtest
`wd_restore_pre_cancelled_ctx_skips_backend` only proves that a
*pre-cancelled* ctx skips the call — it does not prove the cancellation
*during* the call is honoured (because it isn't). The other bound subtest
`wd_restore_budget_exceeded_logs_abandoned_continues_others` uses a test-only
hung-goroutine stub via the `restoreOneImpl` seam, which sleeps under its
own control, NOT a real ctx-aware syscall path. This means RULE-WD-RESTORE-BUDGET
is bound to subtests that exercise the bookkeeping but **not** the real-world
"sysfs is wedged" scenario.

**Suggested action:** The structural fix is the same as C1/H1: make the
syscall itself cancellable. Without that, the rule text should be amended
to say "budget bounds *the daemon's exit wait*; abandoned goroutines persist
until syscall returns or process death."

### H3. `Register` does sysfs I/O without holding `w.mu`, but more importantly without honouring a deadline — a hot-plug or hung chip blocks daemon startup indefinitely

**File:** `internal/watchdog/watchdog.go:106, :116`.

`ReadPWMEnable` and `ReadPWMEnablePath` use `os.ReadFile` with no deadline.
On a freshly hot-plugged or wedged chip, daemon startup blocks here.
Register is called from the startup loop in `cmd/ventd/main.go:795-797`
without a guard. systemd's `TimeoutStartSec` would kill the daemon, but the
diagnostic is worse than the runtime case because the operator just sees
"systemd timeout" with no actionable log line about *which* chip is hanging.

**Suggested action:** Wrap initial-state captures with a per-call 250 ms
timeout (goroutine + select pattern). On timeout, log "watchdog: initial
pwm_enable read timed out for <path>; using full-speed fallback on restore"
and set `origEnable = -1` (already the well-tested fallback). Low risk
because the fallback path is exercised by
`wd_fallback_missing_pwm_enable_continues`.

### H4. IPMI fans are not routed through the watchdog at all — restore is owned entirely by the IPMI backend, with no integration test of the cross-cutting contract

**File:** `internal/watchdog/watchdog.go:308-316`.

`restoreOne` only branches on `e.fanType == "nvidia"`; everything else
falls into the hwmon branch. The IPMI backend has its own RULE-IPMI-7
restore contract ("Restore for every channel sends the vendor-specific
auto-mode command"), but it is NOT invoked from this watchdog. Pass-1's
hardware-path-unwired Category B already flagged IPMI vendor probes as
dead; the same wiring gap exists for IPMI restore — though there's no
evidence IPMI fans are currently emitted as `fan.Type = "ipmi"` by the
setup wizard (search of cmd/ventd, internal/setup confirms only `"hwmon"`
and `"nvidia"` are produced).

Today this is latent (no production fans use `Type: "ipmi"`), but if a
future PR adds IPMI as a routable fan type, registering it with the
watchdog would dispatch through the hwmon branch, attempt
`ReadPWMEnable("/dev/ipmi0")`, and silently fail with a stat error. The
rule catalogue says nothing about which fan types are routable through
the watchdog.

**Suggested action:** Add a `default:` case to the switch in
`restoreOne` that returns early with an error log naming the unsupported
fanType. Currently the function silently takes the hwmon path. Same
defensive change for `Register`'s switch at line 100. Or — cleaner —
document the closed set of supported fanTypes explicitly in the
`Register` doc-comment and the safety rules.

## Medium findings    (robustness issue, dead branch)

### M1. The `restoreOneImpl` test seam is a package-level `var` mutated without synchronisation

**File:** `internal/watchdog/watchdog.go:34`.

```go
var restoreOneImpl = func(w *Watchdog, e entry) { w.restoreOne(e) }
```

Tests swap this via direct assignment (`safety_test.go:498-509`).
Production never reassigns it. There's no `sync.Mutex` or `atomic.Value`
protecting it. If a test forgot `t.Cleanup`, subsequent tests in the same
binary could see the stub. Today only `wd_restore_budget_exceeded...`
mutates the seam and it uses `t.Cleanup` correctly — but a future test
could introduce a race when `-parallel` runs against this package. Given
the package's safety-critical nature this is worth a `sync.Once`-guarded
override or a per-Watchdog hook field rather than a global.

**Suggested action:** Promote to a `Watchdog` field
(`w.restoreOneImpl func(*Watchdog, entry)`); tests construct a Watchdog
with the override directly. Production constructor sets the default. The
seam stops being process-global.

### M2. `Restore()` always creates a fresh `context.Background()` — it does not honour the daemon's shutdown ctx

**File:** `internal/watchdog/watchdog.go:173-177`.

```go
func (w *Watchdog) Restore() {
    ctx, cancel := context.WithTimeout(context.Background(), DefaultRestoreBudget)
    defer cancel()
    w.RestoreCtx(ctx)
}
```

`cmd/ventd/main.go:800` calls `defer wd.Restore()`. The daemon's
shutdown context is already cancelled by the time the defer runs, but
`Restore()` ignores it and uses a fresh 1.8 s budget. This is intentional
— "the budget IS the safety primitive, not the upstream ctx" — but it
means a fast-exit scenario where the operator wants `kill -TERM` to
return in <100 ms (e.g. during a soak test teardown) is bounded at 1.8 s
regardless.

Not a bug per se; the rule explicitly documents this. But the divergence
from "every goroutine tied to a context.Context" in CLAUDE.md is worth
flagging.

**Suggested action:** Consider an `Options` constructor that lets the
caller pass a parent ctx + budget; default behaviour unchanged. Low
priority — current behaviour is correct for production use.

### M3. The "grace period" after budget firing is racy against the abandoned-set snapshot

**File:** `internal/watchdog/watchdog.go:233-260`.

After ctx fires, the grace timer waits 100 ms, then takes `imu` to
snapshot the still-incomplete set. During the 100 ms grace window,
finishing goroutines call `imu.Lock(); delete(incomplete, ...); imu.Unlock()`.
A goroutine that finishes microseconds after the snapshot lock-take is NOT
in the abandoned set (correctly), but a goroutine that finishes
microseconds before is also not in the set (also correct). However, a
goroutine that finishes *exactly* during the snapshot has its delete
contend with the snapshot's iteration. Go's map iteration is undefined
under concurrent mutation; since we take `imu` to snapshot, the
iteration is safe. So this is actually OK.

The subtler issue: the WARN log lists `abandoned_channels` but those
goroutines may complete *successfully* between the WARN and `KillMode=process`.
Operators reading the journal see "abandoned: pwm2" and may report the bug,
even though pwm2 was restored fine 50 ms later. A bare "abandoned" message
overstates the situation.

**Suggested action:** Either rephrase ("budget exceeded — proceeding without
waiting; restore may complete in background") or add a deferred log when
the abandoned goroutine eventually returns. Low priority — operator
mental-model issue, not a correctness bug.

### M4. `entries` slice rewrite via `append(s[:i], s[i+1:]...)` retains a stale element at the new len

**File:** `internal/watchdog/watchdog.go:163`.

```go
w.entries = append(w.entries[:i], w.entries[i+1:]...)
```

This is the standard "remove from middle" pattern but the underlying
backing array still holds a reference to the popped `entry` at index
`len-1` (Go semantics). For `entry` structs with no pointer fields this
is harmless (current state), but if a future refactor adds a pointer
field (e.g. a `restoreFn func(...)`), the GC can't reclaim until the
slice grows. Pure code-quality nit; flagged for the audit's "future-
proofing" pass.

**Suggested action:** None required. If a pointer field lands, zero
the popped slot first: `w.entries[i] = entry{}; w.entries = append(...)`.

## Low findings       (style)

### L1. The fan-type switch in `Register` and `restoreOne` is open-coded twice

`Register` (line 100): `switch { case fanType == "nvidia": ... case IsRPMTargetPath: ... default: ... }`.
`restoreOne` (line 308): `if e.fanType == "nvidia" { ... } else { ... }`.

Two slightly-different forms; the second silently treats every non-nvidia
type as hwmon. A canonical helper `kindOf(fanType, pwmPath) Kind` returning
an enum, used by both, would make adding a new fanType impossible without
touching both sites. See H4 for the safety angle.

### L2. The "Safety envelope" doc-comment at lines 36-63 is excellent — preserve and update with every behaviour change

The doc-comment is the kind of operator-facing documentation that
RULE-* rules orbit around. Worth pinning a rule that says "modifying
the doc-comment in `watchdog.go:36-63` requires re-reading rule
catalogue alignment in the same PR" — not a bound subtest, but a
review-time discipline.

## Verified-correct   (invariants confirmed)

- **RULE-WD-RESTORE-EXIT**: every registered entry's goroutine reaches
  the backend call when the budget is not exceeded. Verified by reading
  the wg.Add(1) + defer wg.Done() pairing in `RestoreCtx`. Test
  coverage adequate for the happy path.
- **RULE-WD-RESTORE-PANIC**: per-entry panic recovery is structural
  (the `defer func() { recover() }` block at line 292 fires before any
  return path). Verified by reading the recovery's logger.Error call
  and the test's countingPanicHandler injection.
- **RULE-WD-RPM-TARGET**: the `fan*_target` branch correctly dispatches
  via `b.restoreRPMTarget` rather than the duty-cycle path; the
  `fan*_max` fallback is correctly read inside the backend.
- **RULE-WD-DEREGISTER**: the LIFO-pop semantics at line 161-167 are
  correct; the linear scan is fine for the small `len(entries)` this
  package sees in production (~10s at most).
- **RULE-WD-REGISTER-IDEMPOTENT** (in-memory): stacking on top of an
  existing entry does preserve the earlier entry's `origEnable` because
  `Register` appends rather than overwrites. Cross-crash preservation
  is the C2 finding; in-memory preservation is correctly tested by
  `wd_register_preserves_startup_origenable`.
- **No goroutine leaks under happy path**: `RestoreCtx`'s wg + done-channel
  pattern is correct. The leak only occurs under the budget-exceeded
  branch (per C1 / H1).

## Files NOT audited and why

- `safety_test.go` — explicitly out of scope (the audit prompt says
  "read for context but don't audit"). Read end-to-end for binding
  coverage analysis.
- `watchdog_test.go`, `watchdog_restore_test.go`, `restore_matrix_test.go`
  — adjunct test coverage; opened briefly to confirm no extra
  production code is hidden as test-only constants. No bound rules
  reference them.
- `internal/hal/hwmon/backend.go` — read selectively (lines 256-320,
  the Restore branch) to validate fall-through behaviour. The hwmon
  backend's broader audit is out of scope for this pass.
- `internal/hal/nvml/backend.go` — same; only the Restore method
  (lines 115-137) was load-bearing for the watchdog audit.
- `internal/hwmon/hwmon.go` — read for `WritePWM` / `WritePWMEnable`
  / `ReadPWMEnable` definitions to confirm no deadline-aware variants
  exist. Confirmed: all use plain `os.ReadFile` / `os.WriteFile`.

## Single most important finding

**C1**: The 1.8 s `DefaultRestoreBudget` bounds the daemon's *wait*, not
the underlying sysfs / NVML syscall — a wedged driver leaves an
abandoned goroutine alive past `RestoreCtx` return, and `RULE-WD-RESTORE-EXIT`
("every documented exit path restores") is structurally violated for
that channel because no fallback fires. The rule text acknowledges this
in passing ("the abandoned goroutines continue to run") but the
user-facing README's "every exit path restores firmware control within
two seconds" promise is louder than the rule's caveat — and the bound
subtests verify bookkeeping (the WARN log) rather than the actual fan
state on a real driver wedge.
