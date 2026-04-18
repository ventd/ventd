# Audit checklist: `internal/controller/`

The controller is the hot loop — runs every poll interval (2s default), reads sensors, evaluates curves, writes PWM. Any allocation per tick at N fans × M curves will show up in pprof. Any missing clamp or missing watchdog ping has user-visible safety impact.

Cross-reference: CAUGHT.md #1 (scheduler↔override race), #3 (maxRPM cache poisoning). Rule files: `.claude/rules/hwmon-safety.md` (operator-facing invariants), `.claude/rules/watchdog-safety.md` (RESTORE-EXIT bindings).

---

## Scope

PRs touching `internal/controller/*.go`, `internal/controller/controller.go`, `internal/controller/panic.go`, `internal/controller/schedule_integration.go` (if added), or the curve-evaluation call sites.

## Always-run checks (priority order)

### 1. Every PWM write path clamps? (HWMON-SAFETY — file via TEMPLATES.md #2/#3 if not)

Three layers of defence, all MUST be present:

- `validate()` at config load time rejects invalid min/max combinations
- Controller refuses `pwm==0` writes unless `AllowStop=true` AND `MinPWM=0`
- HAL backend's Write also clamps (defence in depth)

**How:** find every call site of `backend.Write(ch, pwm)`. For each, trace where `pwm` comes from — curve evaluation, manual override, panic button. At each, verify the value was clamped to `[fan.MinPWM, fan.MaxPWM]` before Write.

**Fail if:** any path writes a raw curve output without clamp, or skips the `AllowStop` gate.

### 2. Does every exit path call watchdog restore?

Covered by `RULE-WD-RESTORE-EXIT` in `.claude/rules/watchdog-safety.md`. Bound subtest must exercise ALL exit paths: `ctx.Done`, early error, panic-recover.

**How:** read `Controller.Run` top-to-bottom. For every `return` statement, every `case <-ctx.Done()`, every `defer` with recover, trace whether `wd.Restore` or `wd.RestoreOne` gets called.

**Fail if:** a new exit path landed without Restore — or if the panic-recovery envelope got moved and now a panic in a helper skips Restore.

### 3. Is tick-loop allocation bounded? (CACHE-POISONING — CAUGHT.md #3)

Opt-1 through Opt-6 from #260 each have an invariant. When a PR touches the hot loop:

- Opt-1 (sensors map reuse): no `make(map, ...)` inside the tick — use pre-allocated, `clear()` between ticks
- Opt-2 (curveSig fingerprint): new curve config fields must be added to `curveSig` or be explicitly documented as "mutated in place not supported" (CAUGHT.md would add an entry)
- Opt-4 (maxRPM cache): any cache populated from a helper that can silently fall back MUST track real-vs-fallback explicitly (CAUGHT.md #3 directly)
- Opt-5 (binary search): slice being searched must be strictly increasing, enforced by `validate()`
- Opt-6 (sync.Pool for Mix.Evaluate): `*vp = (*vp)[:0]` must run on both Get and Put paths (belt + braces)

**How:** `git diff origin/main..HEAD -- internal/controller/` — look for any new call into `config.Load()` inside the tick loop (should be zero — snapshot once per tick).

**Fail if:** per-tick allocation count increases OR a new cached value lacks a "real vs fallback" distinction.

### 4. Curve engine contracts preserved?

Current implicit contract: `Curve.Evaluate(sensors map[string]float64)` — callee MUST NOT retain the map across ticks. Production code always passes a fresh snapshot; tests sometimes mutate live.Curves in place.

**How:** if the PR adds a new curve type (like #223's hypothetical future "points-with-hysteresis"), verify the new implementation's Evaluate doesn't stash the map in a field.

**Fail if:** any curve implementation holds a reference to the sensors map.

### 5. Manual-override / scheduler / panic integration correct? (TOCTOU — CAUGHT.md #1)

This is where #289 lives. Three mutation sources compete for `cfg.ActiveProfile`:

- `handleProfileActive` (user clicks in UI)
- `runScheduler` tick (schedule fires)
- `handlePanic` (panic button)

Ordering invariants:

- Setting the sidecar flag (manualOverride, panicActive) MUST happen BEFORE the atomic.Pointer swap, not after
- Scheduler's `computeActiveProfile` reads manualOverride + lastScheduled, both atomic or mutex-protected
- Panic state is process-local (not persisted), so stale-tab cannot un-panic a rig on restart

**How:** for any new mutation path, trace A-then-B ordering; verify the observable swap (cfg.Store) happens LAST.

**Fail if:** new mutation path adds a swap-before-flag-set window, or adds a new flag that's persisted when it shouldn't be.

### 6. Retry + RestoreOne symmetry on PWM write failure

Per #263: a PWM write failure triggers retry at 50ms, then RestoreOne on second failure. Both paths must go through the same `restoreOne` helper (not duplicate code), and both must end with `pwm_enable = 1` (or captured origEnable) written back.

**How:** verify any new error path in `writeFan` / `applyTick` calls `restoreOne`, not `Restore` (the loop) and not a fresh inline restore.

**Fail if:** a new error path forgets RestoreOne, or restores via a different path that skips the bound rule's behaviour.

### 7. Controller yields to calibrate when calibration is in flight

Calibrate's `ZeroPWMSentinel` + controller's cooperation contract. When a calibration sweep owns a channel, the controller tick skips writes to that channel.

**How:** look for any new code that writes to a channel without checking `cal.Active(pwmPath)` first. The existing contract is in `controller.go`'s `applyTick`; new fan-write paths must respect the same gate.

**Fail if:** new write path bypasses the calibrate active check.

### 8. No new `log.Fatal` / `os.Exit` / `panic(` outside cmd/

Controller runs in the daemon; panicking takes out the whole process. Any unrecoverable error should return through `Controller.Run`'s error channel, not exit.

**How:** `grep -rn 'log\.Fatal\|os\.Exit\|panic(' internal/controller/` — should match only the `recover()` path, which is allowed.

**Fail if:** new `log.Fatal` or `panic(` appears in controller code.

---

## Skim-pass (low budget)

1. Does every PWM write path clamp? (check 1)
2. Does every exit path call watchdog restore? (check 2)
3. Any new allocation per tick? (check 3)

Three checks, 5 minutes, covers the top failure modes.

---

## Not-audited

- Specific coverage percentages (CI reports; only verify if PR claims a target)
- Benchmark deltas (nice to have; not blocking)
- Log format / slog keys (only if operator-facing)
