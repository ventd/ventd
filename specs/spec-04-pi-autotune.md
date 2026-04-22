# Spec 04 — PI autotune controller (learning v1)

**Masterplan IDs this covers:** P4-PI-01, P4-PI-02, P4-HYST-01, T-PI-01, T-PI-02, T-HYST-01.
**Target release:** v0.6.0 (headline feature — "Autotuning PI controller" per RELEASE-PLAN).
**Estimated session cost:** Sonnet, ~10–15 sessions, $10–20 each, over 2–3 weeks calendar time. Two Opus consults (~$5 each) — one on PI stability proof, one on autotune safety.
**Dependencies already green:** P1-HAL-01, P1-HOT-01, controller safety suite.

---

## Why this before MPC

Market-strategy Wedge #8 is unambiguous: *"P4-PI-01 (simple PI with autotune) first — much lower cost, 80% of the benefit. Ship MPC as an opt-in flag."* MPC is the aspirational demo; PI is the product. A well-tuned PI controller beats a badly-tuned MPC every single time, and MPC without a PI fallback is unshippable anyway (see `.claude/rules/mpc-stability.md` RULE-MPC-<fallback>).

This spec ships v1 of the learning story. MPC is a separate spec (P4-MPC-01) that builds on this.

## What PI gives you that curves don't

**Curves are reactive.** Temperature rises → PWM rises after the fact. You're always chasing the setpoint.

**PI is a controller.** It sees *rate of change* in the integral term and can push PWM ahead of a spike. Correctly tuned: your CPU holds 72°C ±1°C under any load instead of oscillating between 65°C (fans spun down) and 85°C (fans spun up reactively).

**Autotune is the magic trick.** Ziegler-Nichols relay method drives the plant (fan → temp) into controlled oscillation, measures the critical gain `Ku` and period `Tu`, and spits out `Kp, Ki` that are within ~10% of analytic optimum on a well-behaved plant. The user does nothing except click "Autotune" in the UI. This is the "zero-config" story made real.

## Scope — what this session produces

Four PRs, strictly sequential. Do NOT let CC parallelise these — PI stability guarantees build on each other.

### PR 1 — PI curve type (P4-PI-01)

**Files:**
- `internal/curve/pi.go` (new)
- `internal/curve/pi_test.go` (new)
- `internal/curve/pi_safety_test.go` (new — bound to `.claude/rules/pi-stability.md`)
- `internal/controller/controller.go` (integration)
- `internal/config/config.go` (schema migration)
- `internal/config/testdata/pi_*.yaml` (fixtures for config round-trip)
- `.claude/rules/pi-stability.md` (new)
- `docs/config.md` (PI section)

**PI curve type:**
```go
package curve

// PICurve is a proportional-integral controller over a single sensor.
// It produces PWM in [0, 255] given (current_temp, setpoint, dt).
type PICurve struct {
    Setpoint      float64  // °C; the temperature we want to hold
    Kp            float64  // proportional gain; typically 1–10
    Ki            float64  // integral gain; typically 0.01–1.0 (per second)
    FeedForward   float64  // static PWM bias; 0–100, added before clamp
    IntegralClamp float64  // anti-windup cap for |integral|; MUST be set
    MinPWM        uint8    // clamp floor
    MaxPWM        uint8    // clamp ceiling
}

type piState struct {
    integral    float64
    lastError   float64  // for derivative-kick protection; unused in PI but kept for debugging
}
```

**Control law (hot-path, per tick):**
```
error      = temp - setpoint
integral  += error * dt
integral   = clamp(integral, -IntegralClamp, +IntegralClamp)  // anti-windup
output     = FeedForward + Kp*error + Ki*integral
pwm        = clamp(output, MinPWM, MaxPWM)

// Conditional integration: if we saturated in the direction that would have
// increased the error, freeze the integrator this tick.
if (pwm == MaxPWM && error > 0) || (pwm == MinPWM && error < 0) {
    integral -= error * dt  // undo the accumulation we just did
}
```

**PI stability invariants (`.claude/rules/pi-stability.md`):**
1. `RULE-PI-01`: Integral is bounded by `IntegralClamp`. Test: inject 10 minutes of sustained +5°C error → verify integral stays ≤ `IntegralClamp`.
2. `RULE-PI-02`: Saturation triggers anti-windup (conditional integration). Test: drive error to saturate at MaxPWM for 1 minute → error drops → output response latency is within 3 ticks.
3. `RULE-PI-03`: NaN in sensor input → fallback to `FeedForward` PWM, no NaN propagation. Test: feed NaN, verify output == FeedForward.
4. `RULE-PI-04`: Gain bounds enforced at config load: `Kp ∈ [0, 100]`, `Ki ∈ [0, 10]`, `IntegralClamp > 0`, `IntegralClamp ≤ 1e6`. Out-of-range rejects config at load time.
5. `RULE-PI-05`: Zero `dt` (clock issue) → skip integration this tick, do not divide. Test: pass dt=0, verify no panic, integral unchanged.
6. `RULE-PI-06`: Fallback on controller panic → watchdog restores fan to pre-ventd pwm_enable (this invariant already exists for curves; PI must not regress it).
7. `RULE-PI-07`: State isolation — one channel's integrator cannot contaminate another's. Test: two PI curves on two channels with different setpoints; confirm integrator values diverge correctly.

**Property tests (`PropPIStability`):**
- Random plant (first-order linear: `dT/dt = -a*(T - T_ambient) + b*PWM`), random setpoint in [30, 80]°C, random gains in safe ranges → controller settles within 60 seconds without oscillation, steady-state error < 1°C.
- Randomly perturbed `dt` in [0.5s, 2.0s] → controller still stable.
- Saturation events don't cause windup that takes >5 seconds to recover from.

### PR 2 — Hysteresis bands (P4-HYST-01)

Do this BEFORE autotune. Autotune on a PI controller without hysteresis bands will chatter during the characterisation sweep and produce junk gains.

**Files:**
- `internal/controller/controller.go` (extend)
- `internal/controller/hysteresis_test.go` (extend)
- `internal/config/config.go` (schema migration — single-scalar `hysteresis: N` still accepted)

**Behaviour:**
- Banded hysteresis: `quiet` / `normal` / `boost` thresholds with separate up/down hysteresis per band.
- Transitions logged as structured events at debug level (useful for diagnosing "my fan is chattering").
- Backward-compatible: old `hysteresis: 2.0` config migrates to `{quiet: 2.0, normal: 2.0, boost: 2.0}` at load.

**Invariants:**
- `RULE-HYST-01`: No transition without crossing the full hysteresis band in the relevant direction.
- `RULE-HYST-02`: Property test `PropHysteresisNoFlutter` — 10k random temperature traces, zero cases where a single tick of noise causes two transitions.

### PR 3 — Ziegler-Nichols relay autotune (P4-PI-02)

**Files:**
- `internal/calibrate/autotune.go` (new)
- `internal/calibrate/autotune_test.go` (new)
- `internal/calibrate/autotune_safety.go` (new — safety sentinels)
- `internal/calibrate/safety_test.go` (extend with autotune invariants)
- `internal/web/api/calibrate.go` (extend — new endpoint)
- `.claude/rules/calibration-safety.md` (extend with autotune rules)

**Algorithm (relay-feedback method, Åström-Hägglund variant):**
1. **Pre-check:** sensor must be responsive (±0.5°C change observed in last 5 minutes of normal operation), fan must be calibrated (min/max RPM known from existing calibration), system must be at thermal steady state (dT/dt < 0.1°C/s for 60s).
2. **Relay drive:** oscillate PWM between `min*1.2` (safe floor above fan stall) and `max*0.8` (safe ceiling below acoustic limit). Switch PWM direction every time temp crosses the user's chosen setpoint.
3. **Measure:**
   - `Tu` = average period of the last 5 oscillations (seconds).
   - `Ku` = `4 * relay_amplitude / (π * temp_amplitude)`.
4. **Compute gains (Ziegler-Nichols PI):**
   - `Kp = 0.45 * Ku`  (PI-tuned, not P-tuned)
   - `Ki = Kp / (Tu / 1.2)`  (integral time = 0.833 * Tu per Z-N PI table)
5. **Validate:** simulate 60 seconds on the ARX-fitted plant model from the oscillation data. If simulation diverges or oscillates, reject gains and return an error. Do NOT apply untested gains to real hardware.
6. **Persist:** store `(Kp, Ki, Tu, Ku, captured_at)` in the fan's calibration record. Timestamp lets you detect drift across reboots.

**Safety sentinels — non-negotiable:**
- `RULE-CAL-AUTOTUNE-01`: Autotune aborts if temperature exceeds `thermal_max - 5°C` at any point. User-visible error: "Autotune aborted — thermal headroom insufficient. Run during low ambient temp."
- `RULE-CAL-AUTOTUNE-02`: Autotune aborts if oscillation doesn't stabilise within 10 minutes. User-visible error: "Autotune aborted — plant too slow. This sensor/fan pair may not be suitable for PI control."
- `RULE-CAL-AUTOTUNE-03`: Autotune aborts if >3 consecutive oscillation periods differ by >30%. Indicates external disturbance (AC turning on, workload change). User can retry.
- `RULE-CAL-AUTOTUNE-04`: Autotune never leaves a fan at min*1.2 on exit — always restores to pre-autotune PWM state.
- `RULE-CAL-AUTOTUNE-05`: Autotune fallback on crash — watchdog restores the fan's pre-autotune `pwm_enable`, same as normal exit path.

**API endpoint:**
```
POST /api/calibrate/autotune
{
  "fan_id": "hwmon1/pwm3",
  "sensor_id": "k10temp/Tctl",
  "setpoint": 70.0,
  "timeout_s": 600
}
→ 200 {"kp": 3.8, "ki": 0.12, "tu": 24.5, "ku": 8.4, "captured_at": "..."}
→ 4xx with specific rule violation codes on abort
```

**Tests (T-PI-02):**
- Synthetic plant with known transfer function → autotune returns gains within 10% of analytic optimum.
- Degenerate plant (no oscillation possible — e.g., sensor noise dominates response) → autotune aborts cleanly with correct error.
- All five safety sentinels have dedicated abort-path tests.

### PR 4 — Dither (P4-DITHER-01)

Small. Do it here because it plays with PI — dithered PWM across mixed-curve fans prevents acoustic beat frequencies that PI can otherwise lock into.

**Files:**
- `internal/controller/controller.go` (dither injection)
- `internal/controller/dither_test.go` (new)
- `internal/config/config.go` (add `dither_pct: 0–5` per curve)

**Behaviour:**
- Per-curve `dither_pct` ∈ [0, 5], default 0.
- Each fan assigned to the curve gets a different instantaneous PWM offset, drawn from a triangular distribution centred on 0 with bounds `±dither_pct%` of max PWM, low-pass-filtered at 0.2 Hz so the dither itself doesn't become audible.
- Mean dither across time and across fans is zero (verified by `PropDitherMean`).

## The Opus consults — two, at different stages

### Consult 1 — before PR 1 (PI stability)

**Purpose:** Review the control law, anti-windup scheme, and proposed invariant list. Specifically:
- Is conditional integration sufficient, or do we also need back-calculation anti-windup?
- Does `RULE-PI-03` (NaN → FeedForward) introduce a bumpless-transfer problem when the sensor recovers?
- Are the gain bounds in `RULE-PI-04` defensible, or are they arbitrary?

**Expected output:** one-page confirmation or a specific diff against the control law.

### Consult 2 — before PR 3 (autotune safety)

**Purpose:** Review the five autotune safety sentinels and the Z-N gain formulae. Specifically:
- Are Z-N PI gains right for fan-control applications, or should we use Tyreus-Luyben (more conservative)? Cooling systems typically prefer less aggressive gains than chemical-process systems.
- Is 10 minutes a reasonable timeout, or should it scale with thermal mass?
- Is validation-via-simulation (step 5 of the algorithm) rigorous enough, or do we need closed-loop stability check (Nyquist margin)?

**Expected output:** confirmation of Z-N vs Tyreus-Luyben, plus any additional sentinel the user's control-theory lens surfaces.

Each consult is ~1 hour in claude.ai. Walk out with a short doc you commit to `docs/control-theory-notes.md` for future contributors.

## Definition of done

### PR 1
- [ ] `go test -race ./internal/curve/...` passes.
- [ ] `PropPIStability` passes 10k random runs.
- [ ] All 7 PI stability invariants bound to subtests in `.claude/rules/pi-stability.md`.
- [ ] `docs/config.md` has a PI example with explained fields.

### PR 2
- [ ] `PropHysteresisNoFlutter` passes 10k random traces.
- [ ] Config round-trip: old-format `hysteresis: 2.0` loads and produces equivalent banded config.

### PR 3
- [ ] Synthetic-plant autotune returns gains within 10% of analytic optimum.
- [ ] All 5 safety sentinels have abort-path tests.
- [ ] API endpoint has an OpenAPI spec entry + authenticated access only.
- [ ] HARDWARE-REQUIRED: autotune runs successfully on phoenix-desktop against CPU fan / k10temp. Do NOT claim DoD on this bullet without real-hardware verification.

### PR 4
- [ ] `PropDitherMean` passes.
- [ ] Two fans on the same curve with `dither_pct: 3` produce distinct instantaneous PWMs in 100% of test samples.

## Explicit non-goals

- **No MPC.** Separate spec, separate release. P4-MPC-01 is v0.6+experimental or v0.7 default.
- **No acoustic feedback loop.** Phase 7. PI tunes to thermal setpoint; quietness is emergent.
- **No model-based feed-forward from workload prediction.** That's MPC territory.
- **No per-fan autotune parallelism.** Autotune runs one fan at a time — parallelism invalidates the identifiability assumption of relay feedback.

## CC session prompt — copy/paste this

```
Read /home/claude/specs/spec-04-pi-autotune.md. This is a four-PR spec,
STRICTLY SEQUENTIAL — do not start PR N+1 until PR N's DoD is green. PI
stability guarantees compound and skipping verification will cost you later.

Before PR 1: confirm I have run the Opus consult on PI stability. Output
from that consult is in /home/claude/specs/spec-04-opus-pi-notes.md (I will
add). Read it before writing internal/curve/pi.go.

Before PR 3: confirm I have run the Opus consult on autotune safety. Output
in /home/claude/specs/spec-04-opus-autotune-notes.md. Read it before writing
internal/calibrate/autotune.go.

For PR 1 and PR 3, the `.claude/rules/*.md` files are the authoritative
source of what must be tested. Every RULE-<N> line maps to a subtest. The
T-META-01 lint will fail the PR if this mapping breaks.

Use Sonnet throughout. Do NOT call Opus from inside CC — I run those
consults separately and commit the notes. This keeps the cost structure
clean: flat-rate Opus for design, per-token Sonnet for implementation.

Commit at every green-test boundary. If a property test discovers a failing
case, commit the failing case as a regression test BEFORE fixing the bug.
```

## Cost discipline notes

- PR 1 is the foundation. Spend extra session time here. Cheaping out on PI means PR 3 and PR 4 both wobble.
- PR 3 will have the highest Sonnet burn because autotune is stateful and the test scenarios are rich. Break it into 3 sub-sessions: (a) synthetic plant simulator, (b) autotune algorithm itself, (c) safety sentinels + API wiring.
- The property-test infra (`PropPIStability`, `PropHysteresisNoFlutter`, etc.) is pure Go random-input loop work — Haiku can write the scaffolds if Sonnet gets expensive in long sessions.
- Do NOT let CC re-derive the Ziegler-Nichols formulae from scratch. They're in the spec. If CC "proposes improvements," stop the session and commit what you have.
