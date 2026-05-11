# Pass 6: internal/controller/ deep read

**Files audited:**
- `internal/controller/controller.go` (967 LOC)
- `internal/controller/blended.go` (673 LOC)
- `internal/controller/decisioncache.go` (108 LOC)

**LOC:** 1,748 (non-test)

**Files NOT audited:** `controller_test.go`, `blended_test.go`,
`decisioncache_test.go`, `helpers_test.go`, `hysteresis_test.go`,
`observation_test.go`, `run_test.go`, `safety_test.go`, `tick_test.go`
— test files used for context only, per scope.

**Time on task:** ~30 min (read + analysis + write)

**Baseline commit:** `b46c1a5` (Pass 1-5 audit doc) on top of
`da67bd1` (#1034 Layer-B/C feed). Findings here do not re-flag
#1035 smart-mode wiring gaps; out-of-scope per task brief.

## Critical findings

### C1. Sentinel carry-forward write bypasses polarity inversion + calibration guard + retry — silent wrong-direction write on inverted-polarity hwmon channels during sensor sentinel events

`internal/controller/controller.go:544`

```go
if c.hasLastPWM {
    ch, buildErr := c.channelFor(fan)
    if buildErr == nil {
        _ = c.backend.Write(ch, c.lastPWM)
    }
}
```

The sentinel-carry-forward path calls `c.backend.Write` directly,
skipping `writeWithRetry`. Three contracts get bypassed every time
this path runs:

1. **RULE-POLARITY-05 / hwdb.InvertPWM** — `c.lastPWM` is the
   logical (post-inversion) byte saved at line 647. The direct
   `backend.Write` does not re-invert. On an inverted-polarity fan
   (calCh.PolarityInverted=true) every sentinel-glitch tick writes
   the wrong direction's byte to the chip register. Cooling fan
   reverses while the operator's dashboard shows the "carrying
   forward last good PWM" warning.
2. **calibration guard** — `hwdb.ShouldApplyCurve(c.calCh)` is
   never consulted, so phantom + BIOS-overridden channels still
   receive carry-forward writes. RULE-CALIB-PR2B-07 says phantom
   channels are an *unconditional* refuse; this path violates that.
3. **ErrNotPermitted handling** — write failures are ignored
   (`_ = ...`), so a kernel revocation of manual mode during a
   sensor sentinel storm will silently swallow the error rather
   than triggering daemon restart via `signalFatal`.

The blast radius is bounded — only fires on a hwmon chip whose
configured curve-bound sensor is producing 0xFFFF latches AND for
which the channel has a non-default calibration. But this is the
exact failure mode RULE-HWMON-INVALID-CURVE-SKIP and
RULE-HWMON-PROLONGED-INVALID-RESTORE exist to handle safely;
right now the safe-path implementation has a polarity hole.

**Suggested action:** route this write through `writeWithRetry`
(or factor a `writeRawPolarityCorrected` helper that the carry-
forward and `writeWithRetry` share, since carry-forward
intentionally skips retry-on-EBUSY).

## High findings

### H1. WithCalibration's pwmUnitMax is hard-coded to 255 at every call site — InvertPWM produces garbage on step_0_N / cooling_level inverted channels

`cmd/ventd/main.go:1102,1260` (production wiring) +
`internal/controller/controller.go:204,322` (consumer).

Both wiring call sites pass `255` as the second argument:

```go
opts = append(opts, controller.WithCalibration(calCh, 255))
```

`hwdb.InvertPWM(cal, pwm, pwmUnitMax)` returns `pwmUnitMax - pwm`
when `cal.PolarityInverted`. For a step_0_N driver where
`pwm_unit_max` is e.g. 7 (thinkpad_acpi level 0..7), the live
PWM byte from the curve will already be a step index in [0, 7].
Hard-coding 255 in WithCalibration produces:
`InvertPWM(cal, 3, 255) = 252` — written to a register that only
accepts 0..7.

Out-of-range writes on step_0_N drivers commonly return EINVAL.
The controller's `writeWithRetry` will then log + RestoreOne the
channel — defensive degradation, not data corruption, but the
inverted step_0_N case is structurally non-functional. The
RULE-HWDB-PR2-04 hwdb schema enforces `pwm_unit_max` is set when
`pwm_unit` is step_0_N/cooling_level; the catalogue value never
reaches the controller.

Related to Pass 1 Category C entry `marginal.Runtime.SetPWMUnitMax`
("non-duty_0_255 channels can't override pwmUnitMax from default
255") — same root cause: the catalog-derived pwmUnitMax is
unplumbed end-to-end.

**Suggested action:** resolve `EffectiveControllerProfile.PWMUnitMax`
at the wiring layer (cmd/ventd/main.go) and thread the actual
value through `WithCalibration` and through `Runtime.SetPWMUnitMax`.

### H2. Sentinel-carry-forward path does not emit an observation record — breaks observation continuity for smart-mode Layer-B/C feed

`internal/controller/controller.go:541-547` — when carry-forward
re-writes `c.lastPWM` to the chip, no `c.emitObservation` is
called. The observation log will show a gap of (potentially) 30s
on a sentinel-glitchy chip, during which the PWM byte actually
remained committed.

Issue #1034 closed the controller→smart-mode bridge: every
controller-emitted ObsRecord drives Layer-B Update and Layer-C
OnObservation. A missing record during sentinel events means
Layer-B RLS sees `t-30s, t` rather than `t-30s, t-28s, t-26s, ...`
across a sustained sensor glitch — the `dt` between consecutive
Update calls jumps to 30s, blowing past `clampDT`'s 10s cap and
likely producing a junk autoregressive coefficient.

**Suggested action:** call `emitObservation(c.lastPWM)` after the
backend.Write inside the sentinel-carry-forward branch. Also
addresses C1's "silent failure" surface.

### H3. lastTickAt advances during calibration / panic / cfg-not-found yields — first post-yield tick computes dt against the LAST yield, not the last actual control tick

`internal/controller/controller.go:417` — `defer func() { c.lastTickAt = now }()`
runs on every return path including the early returns at lines 422
(calibrate yield), 437 (panic yield), 449 (fan-not-found), 472
(refuse PWM=0), and 484 (manual mode return).

The comment at line 413 ("dt tracks actual elapsed time rather
than only work-completing ticks") is intentional. But the
consequence: on resume after a 5-minute calibration, dt on the
first control tick is ~`interval` (typically 2s), not 5 minutes.
The PI integrator was frozen in piState during the yields, then
suddenly sees a small dt against a baseline-temperature reading
that may have drifted while ventd was hands-off. With the
integrator's stored error term carrying the *pre-calibration*
context, the first post-calibration tick can over- or under-
correct.

This isn't a hard bug — `clampDT` already caps dt to 10s so the
math stays bounded — but the documented invariant "dt tracks
actual elapsed time" is contradicted by the calibration-yield
ratchet. Either the comment is wrong, or the integrator should
be cleared on calibration entry (mirroring the panic-entry purge
at line 432).

**Suggested action:** clarify the comment + decide whether
calibration entry should also purge piState. The current state
is "documented to track real time, but ratchets during yields,
and the integrator state outlives the yield gap" — three
half-decisions that don't compose.

### H4. ObsRecord.RPM is hard-coded to -1; observation log smart-mode consumers never see real tach data

`internal/controller/controller.go:963` — `RPM: -1, // tach reads
not yet wired into controller`. R8's fallback tier ceiling at
tier-0 (real RPM tach) is 0.85 for `conf_A`; the entire 0.85→0.45
gradient up the fallback tiers depends on the smart-mode pipeline
being able to *observe* whether tach data exists.

With RPM=-1 always, the R8 classifier in `internal/fallback/`
(referenced by RULE-FALLBACK-TIER-* and consumed by Layer-A's
ceiling table per RULE-CONFA-TIER-01) can never see tach-present
channels. Every channel is structurally at tier 7 (`ConfACeiling
= 0.00`) for the tach-bound dimension. Combined with the rest of
the smart-mode wiring gaps (#1035), this is "stack of three
inert dependencies" — but tach is the load-bearing one for the
operator-visible confidence number.

This is on the boundary of #1035 scope. The comment says "tach
reads not yet wired into controller" which is honest, but the
v0.6.0 ship plan needs to include this. Not a regression — the
field has been -1 since the observation schema landed.

**Suggested action:** wire `backend.Read(ch)` once per tick for
hwmon channels with `Caps & CapReadFanRPM`, populate `RPM` in
the emitted ObsRecord. Add a RULE-CTRL-OBS-RPM-01 binding when
done.

## Medium findings

### M1. blendFn dt uses dtSeconds reconverted to time.Duration via float-roundtrip — loses sub-microsecond precision and can produce non-monotonic ticks on slow clocks

`internal/controller/controller.go:598` —
`time.Duration(dtSeconds*float64(time.Second))`. `dtSeconds` is
already a clamped float in [0.1, 10.0], so the conversion is
safe in range. But it's a round-trip through float64 of a
`time.Duration` that arrived intact from `now.Sub(c.lastTickAt)`.
The cleaner form is to compute and clamp `time.Duration`
directly, then derive `dtSeconds` only for the StatefulCurve
path that needs it as a float.

Negligible safety impact; cosmetic.

**Suggested action:** compute `dt time.Duration` once at the top
of `tick()`, derive `dtSeconds := dt.Seconds()` lazily for the PI
path. Pass `dt` directly to `blendFn`.

### M2. compiled-curve cache invalidates on `live` pointer change but the cache cleared in `initCurveStateIfNeeded` (line 676) sets `curveBuiltForCfg = nil` — meaning a curve-name-only change in the active config forces a rebuild via TWO different invalidation paths

`internal/controller/controller.go:559,667-678` —
`initCurveStateIfNeeded` clears `compiledCurve = nil` AND
`curveBuiltForCfg = nil`. The rebuild guard at line 559 checks
`c.compiledCurve == nil || c.curveBuiltForCfg != live || ...`.
After `initCurveStateIfNeeded` clears both, the first branch
catches the rebuild. The second branch is dead in that exact
sequence. Cost: one redundant nil check per curve-swap.

Not a bug, but the cache invalidation logic in `initCurveStateIfNeeded`
could be a single-field nil assignment.

**Suggested action:** trim to `c.compiledCurve = nil` in
`initCurveStateIfNeeded`; the cache miss at line 559 catches the
rebuild.

### M3. fatalErr channel size is 1 — when both `writeWithRetry`'s ErrNotPermitted AND a tick-level panic recovery race, only the first error reaches Run

`internal/controller/controller.go:289,304-308` — `c.fatalErr` is
a size-1 buffer; `signalFatal` does a non-blocking send and drops
on full. This is intentional ("first one wins" per the comment
at line 307), but it means a tick that first encounters
ErrNotPermitted from `writeWithRetry`, queues it via signalFatal,
then panics in subsequent code, will surface the ErrNotPermitted
to Run (correct) and lose the panic info (only logged by Run's
defer recover, which fires on Run-return-via-panic, not
signalFatal-after-panic-recovery).

In practice the tick-level `defer recover()` (line 370) catches
panics at the Run level, not the tick level. A panic mid-tick
escapes the goroutine. The defer at 368 fires only when Run's
own goroutine panics — which is the calling goroutine, so it
does catch a tick panic.

Safe today. Worth a comment noting "tick panic → recovered by
Run's defer, not tick's; intentional so signalFatal doesn't
double-fire".

**Suggested action:** add a `// NOTE:` comment to the defer at
line 368 noting that it catches both pre-panic queued fatal
errors AND tick-level panics, and the ordering interaction.

### M4. PI state map only ever has one entry — keyed by `c.pwmPath` for a Controller that manages exactly one channel

`internal/controller/controller.go:154,575` — `piState map[string]curve.PIState`
indexed by `c.pwmPath`. The map has at most one entry, since each
Controller manages one channel (per the comment at line 145).
Using a map is overkill; a single `piState curve.PIState` +
`hasPIState bool` field would be functionally identical, save
one `map[string]` allocation, and remove the dead `for k := range
c.piState` loop at line 432 (which iterates over at most 1
entry).

The comment at line 145-153 explains the map for "clean purge",
but `delete(c.piState, c.pwmPath)` and `c.piState[c.pwmPath] =
PIState{}` are no harder against a struct field than a map.

Not a bug. Code smell.

**Suggested action:** replace map with a single field. Saves a
map allocation per Controller construction; the codebase has
hundreds of Controllers in the typical multi-fan config.

## Low findings

### L1. Stale comment at controller.go:963 — "tach reads not yet wired into controller"

The comment is currently true but the wording implies future
intent. If RPM-into-ObsRecord is now an out-of-scope/won't-fix
decision, the comment should say so; if it IS pending v0.6.x
work, link the tracking issue. See H4 — this is the doc form
of the same gap.

**Suggested action:** decide H4's path; update or remove this
comment accordingly.

### L2. blended.go:106 MinKpForBumpless = 1e-9 is checked against `math.Abs(Kp)` even though Kp comes from deriveIMCPIGains which guarantees Kp > 0

`internal/controller/blended.go:450` —
`if math.Abs(Kp) < MinKpForBumpless`. RULE-CTRL-PI-01 pins
`K_p > 0`. The Abs() is a belt-and-braces guard against a future
sign-convention regression; the rule's bound test
TestPI_GainDerivation enforces the invariant. Cost: free.
Value: small future-proofing.

Not a bug. Worth a note that the Abs() is defence against the
RULE-CTRL-PI-01 sign regression, not a real handling of negative
gains.

**Suggested action:** add a comment line referencing
RULE-CTRL-PI-01 at the Abs() call. Failing CI on a Kp sign flip
catches the same issue, so optional.

### L3. blended.go gainRefreshSamples cache compares NSamples - cachedNSamples — uint64 underflow if NSamples decreases (impossible today, but no comment)

`internal/controller/blended.go:421` — `in.Coupling.NSamples -
st.cachedNSamples < gainRefreshSamples`. If NSamples were to
decrease across calls (catastrophic shard reset, future shard
re-init logic) the unsigned subtraction wraps. The marginal +
coupling shard contracts make NSamples monotonically non-
decreasing in a healthy lifecycle, so this is structurally safe
today.

**Suggested action:** add a one-line comment that the subtraction
is safe because NSamples is monotonic non-decreasing per
RULE-CMB-SHARD-02 / RULE-CPL-SHARD-02. If the v0.7+ shard reset
work lands, this becomes a real bug.

## Verified-correct (regression guards)

These were explicitly inspected against the rule contracts and
implement them correctly:

- **RULE-HWMON-RESTORE-EXIT** — `c.wd.Restore()` fires in Run's
  defer (line 369), reached on ctx.Done, fatalErr, and panic-
  recovery paths. Pass 5 already confirmed this.
- **RULE-HWMON-STOP-GATED** — both manual-mode (line 469) and
  curve-mode (line 609) refuse PWM=0 when AllowStop=false, after
  the clamp() floor enforces MinPWM. Symmetric coverage.
- **RULE-HWMON-CLAMP** — `clamp(raw, fan.MinPWM, fan.MaxPWM)`
  at line 586, plus post-blend re-clamp at line 602. Two
  independent clamps in the pipeline; the second is documented
  as the "controller package owns the final safety boundary"
  (line 600). Good defence-in-depth.
- **RULE-HWMON-INVALID-CURVE-SKIP** + **RULE-HWMON-PROLONGED-
  INVALID-RESTORE** + **RULE-HWMON-SENTINEL-FIRST-TICK-IMMEDIATE-
  RESTORE** — the three-way branching at lines 521-548 covers
  first-tick-no-lastPWM, 30s-elapsed, and within-window paths
  correctly. Only the carry-forward write path is broken (C1).
- **RULE-CTRL-PI-04 anti-windup** — `pwmSatPositive` /
  `pwmSatNegative` check both saturation direction AND
  integrator-push-direction at blended.go:468-471. Correct
  asymmetric clamp.
- **RULE-CTRL-BLEND-03** — zero-w_pred early return at
  blended.go:382-387 re-arms bumpless and exits before integrator
  + cache updates. Confirmed via line-by-line read.
- **RULE-CTRL-PATH-A-01 + 02** — Path-A integrator-freeze
  interaction with anti-windup (blended.go:478) correctly OR's
  `pathARefused` into `integratorFrozen` so a refused ramp does
  not wind up. Nil-marginal early-out at blended.go:637 is
  correct.
- **DecisionCache** — `slot()` takes c.mu under double-checked
  lookup; `Store` and `Load` use atomic.Pointer for the slot;
  `LoadAll` snapshots under c.mu without holding it during the
  per-channel pointer load. Race-free; consistent with the
  package comment.

## Files NOT audited and why

- All `*_test.go` in `internal/controller/` — read for context
  only per task brief; not part of the audit surface.

## Notes on scope

- Pass 1's #1035 smart-mode wiring gaps were excluded from
  re-flagging. The H4 finding above (`ObsRecord.RPM=-1`) is on
  the boundary — it's a controller-level wiring gap that feeds
  the smart-mode pipeline, and was not in Pass 1's mechanical
  symbol sweep because the field hard-code isn't a ghost method.
  Flagging here.
- The blended.go SPEC DIVERGENCE comment (lines 23-41) was
  verified against RULE-CTRL-PI-01 — implementation matches the
  amended rule contract (K_p > 0). The comment is load-bearing
  documentation, not stale.
- The "predictive arm reads zero θ until #1035 lands" condition
  from Pass 1 was confirmed — `piRefuseReason` at blended.go:579
  returns "b_ii=0" on every tick today, which is the operator-
  visible symptom of #1035's structural inertness. Out of scope
  to fix here.
