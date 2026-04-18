# Audit checklist: `internal/calibrate/`

Calibrate sweeps ramp PWM from 0 to 255 to learn the RPM response curve for a given fan. Any bug here either (a) leaves the fan at 0 for longer than `ZeroPWMSentinel` allows, or (b) corrupts the controller's cooperation contract and causes double-writes per tick.

Cross-reference: `.claude/rules/hwmon-safety.md` (the PWM=0 gate applies here too), and the controller↔calibrate cooperation contract documented in `internal/controller/controller.go:applyTick`.

---

## Scope

PRs touching `internal/calibrate/manager.go`, `internal/calibrate/run.go`, `internal/calibrate/results.go`, `internal/calibrate/detect.go`, or the HTTP handlers that drive calibration (`internal/web/calibrate.go`).

## Always-run checks (priority order)

### 1. `ZeroPWMSentinel` still enforced on every sweep path?

Contract: if a sweep commands `PWM=0` for more than 2 seconds, escalate back to a safe floor. This catches the case where a hung sweep (slow hwmon, dead fan tach) leaves a fan stopped under load.

**How:** find every code path that writes `PWM=0` — in `run.go` ramp steps, in `detect.go` RPM-sensor discovery. For each, verify the `ZeroPWMSentinel` timeout is armed AROUND the zero-write, not after a return.

**Fail if:** new sweep path writes PWM=0 without arming ZeroPWMSentinel.

### 2. `Abort` is idempotent? Restores PWM on exit?

`POST /api/calibrate/abort` must be safe to call whether or not a sweep is in flight. Its contract: signal the sweep goroutine to exit; the sweep's deferred `runSync` restores the original PWM.

**How:** trace `Manager.Abort` → `run.cancelCh` → goroutine exits → deferred `restoreFanPWM`. Verify:

- Abort-Abort on the same fan is safe (second call returns cleanly, doesn't panic)
- Abort with no active run is safe (no goroutine to cancel)
- Sweep goroutine's deferred restore fires on ctx cancel, on error return, on panic

**Fail if:** any exit path from the sweep goroutine skips restoring PWM.

### 3. HAL backend is used for writes, not direct `hwmon.WritePWM`?

Per #247 and the P1-HAL-02 migration: calibrate MUST drive fans through `hal.FanBackend.Write`, not directly through the `internal/hwmon` package. This is how calibrate inherits the clamp + mode-acquire logic that lives in the HAL backend.

**How:** `grep -rn 'hwmon\.WritePWM\|hwmon\.WritePWMEnable' internal/calibrate/` — should return zero hits (except perhaps legacy test fixture).

**Fail if:** new direct `hwmon.WritePWM` call appears in calibrate. Even for "test" reasons — tests should use fakehwmon / fakepwmsys.

### 4. Controller yields during active calibration

The controller checks `cal.Active(pwmPath)` before writing a channel. If a new controller code path writes fans, it must respect this gate. (This check duplicates controller check 7, listed here because calibrate PRs sometimes add controller-side hooks.)

**How:** if the PR adds any new `backend.Write` call site outside of `internal/calibrate/` that targets a calibrate-managed channel, verify it gates on `cal.Active`.

**Fail if:** new code bypasses the gate.

### 5. `DetectRPMSensor` doesn't leak acquired-mode state

`DetectRPMSensor` ramps the fan briefly (~5s) to find the correlated RPM sensor. Before #262 it called `hwmon.WritePWMEnable(pwmPath, 1)` directly; post-#262 it delegates to `hal.FanBackend.Write`, which handles mode acquire lazily.

**How:** verify the detect path doesn't assume a specific `pwm_enable` value on entry or exit — it should be backend-agnostic (some backends don't have pwm_enable at all).

**Fail if:** detect path writes to hwmon paths directly.

### 6. Calibration results persistence is safe

Results are stored in `internal/calibrate/results.go`; a crash mid-sweep shouldn't corrupt prior results. Results writes should follow atomic-rename + fsync (see TEMPLATES.md #6).

**How:** if the PR changes how results are written, verify `os.WriteFile(tmp); f.Sync(); os.Rename` pattern. Or verify results live in memory only (acceptable if not persisted across restarts).

**Fail if:** new persistence path lacks fsync.

### 7. Progress reporting doesn't race with sweep mutation

`GET /api/calibrate/status` reads `runs[pwmPath]`; the sweep goroutine writes to it. Must be mutex-protected.

**How:** find the `runs` map access sites. Verify `Manager.mu` wraps every access (not just writes).

**Fail if:** new reader on `runs` skips the lock.

### 8. Setup wizard's calibration path honours the abort signal

The setup wizard kicks off parallel per-fan sweeps. A single abort must cancel all in-flight sweeps, not just one.

**How:** verify `Manager.Abort()` (no args) cancels all active runs, not just the first. Verify wizard's `setup.Manager.Abort()` chains to `cal.Manager.Abort()`.

**Fail if:** setup wizard abort leaves a calibration running.

---

## Skim-pass (low budget)

1. Any new `PWM=0` write without ZeroPWMSentinel arm? (check 1)
2. Any direct `hwmon.WritePWM` in calibrate? (check 3)
3. Any sweep exit path that skips restore? (check 2)

---

## Not-audited

- Ramp timing / dwell durations (physics-tuning; defer to the CC-session author's call)
- Acoustic / vibration heuristics in detect (hard to review statically; empirical)
- Curve fit math (unit tests cover this)

## Related files

- `.claude/rules/hwmon-safety.md` — `pwm==0` gate applies here
- `internal/hal/backend.go` — FanBackend interface that calibrate drives through
- `internal/controller/controller.go:applyTick` — cooperation contract
- `internal/testfixture/fakehwmon` / `fakepwmsys` — what tests should be using instead of real hwmon
