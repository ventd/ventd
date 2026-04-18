# P4-DITHER-01 — per-curve dither to de-sync paired fans

**Care level: MEDIUM.** Dithering perturbs PWM output by small amounts.
On its own, bounded to ≤5% of PWM, it cannot create unsafe states. But
it introduces a non-deterministic factor into the tick loop — two ticks
with identical sensors can produce different PWM values. Reviewers
should verify the randomness is bounded and the stateful RNG is
correctly seeded.

## Task

- **ID:** P4-DITHER-01
- **Track:** CTRL (Phase 4)
- **Goal:** Add an optional per-curve `dither_pct` (0-5) that applies a
  small, bounded, per-channel-unique PWM offset each tick. When two or
  more fans share the same curve, dither breaks their phase-lock and
  avoids beat-frequency audible throb. Dither is zero by default;
  configs without it are unaffected.

## Context you should read first

- `internal/controller/controller.go` — the tick loop; identify where
  curve.Evaluate returns a PWM value and where it reaches the backend
  Write call.
- `internal/config/config.go` — the curve config struct shape; dither
  is a per-curve field (not per-channel) so multiple channels sharing
  a curve inherit the same `dither_pct`.
- `internal/curve/points.go` — reference curve; note how PWM values are
  returned (uint8, already clamped 0-255).
- `docs/config.md` — existing curve reference.

## Design — read carefully, do not deviate

### Where dither lives

Dither is a **post-curve transform**, applied by the controller after
`curve.Evaluate(sensors)` returns a PWM byte, and before the value is
passed to `backend.Write(channel, pwm)`. The curve itself doesn't know
about dither — which keeps Curve implementations (Linear, Points, Mix,
PI, future MPC) all dither-agnostic.

### Config field

Add to the curve-config struct (wherever curves live in `config.go`):

```go
// DitherPct is the per-curve dither amount as a percentage of 255 PWM
// units (so DitherPct=2 → ±5.1 PWM offset max). Valid range [0, 5].
// Zero (default) disables dither. See docs/config.md §dither for
// why this exists.
DitherPct float64 `yaml:"dither_pct,omitempty"`
```

Validation (in `validate()` for every curve kind):
- `0 <= DitherPct <= 5` — reject with named-field error outside this range.
- NaN rejected.
- Values are truncated to 1 decimal place — 2.734 becomes 2.7. (Document this.)

### Per-channel RNG state

Per-channel dither state, mirroring the P4-PI-01 / P4-HYST-01 pattern
of controller-owned per-channel state:

```go
// On the Controller struct:
ditherRNG map[string]*rand.Rand  // keyed by channel ID
```

Initialize at controller start, one `*rand.Rand` per channel, seeded
from `time.Now().UnixNano() ^ hash(channelID)`. The XOR with a hash of
the channel ID means two channels seeded in the same nanosecond still
diverge — otherwise their dither streams would be correlated, defeating
the whole point.

Use `math/rand/v2` (not `math/rand`) since it's stdlib-current and
doesn't require manual Mutex wrapping. Each channel's RNG is accessed
only from its own tick, so no lock needed.

### Dither application

After `curve.Evaluate(sensors)` returns `pwm uint8`:

```go
if curve.DitherPct > 0 {
    r := c.ditherRNG[channelID]
    // Symmetric uniform distribution in [-maxOffset, +maxOffset].
    maxOffset := curve.DitherPct / 100.0 * 255.0
    offset := (r.Float64()*2 - 1) * maxOffset
    result := int(pwm) + int(math.Round(offset))
    if result < 0   { result = 0 }
    if result > 255 { result = 255 }
    pwm = uint8(result)
}
```

Clamping AFTER dither is critical: dither can push a PWM-0 channel
to -3, or a PWM-255 channel to 258 — both must clamp to the valid range.

### Composition with curve features (especially PI)

Dither applies uniformly to whatever the curve returned, including PI
output. This means a PI curve with `dither_pct: 2` gets ±5 PWM of jitter
on each tick. The integral loop in P4-PI-01 is driven by `err * Ki * dt`,
not by the dithered PWM, so dither doesn't wind up the integral. This is
worth calling out in the docs — it means dither and PI compose cleanly.

Mix curves: dither applies once, at the Mix level (outer curve
returning the final PWM), not at each sub-curve. This is free of design
work: the controller only calls the outermost curve's Evaluate, so
"apply dither after Evaluate" naturally puts it at the Mix level.

### SIGHUP reload

On config reload: if a curve's `DitherPct` changed, the per-channel RNG
state is unaffected (it's stream state, not value state). Streams keep
rolling. Event: `event=dither_pct_changed curve=<name> old=<x> new=<y>`.

If a channel's curve changes entirely (curve name swap in the config),
reset that channel's RNG with a fresh seed to avoid correlating streams
between curves sharing a channel ID. Event:
`event=dither_rng_reset_on_curve_swap channel=<id>`.

### Docs

In `docs/config.md`, add a `## Dither` subsection under curves:

- One-paragraph explanation: two identical 120mm case fans on the same
  PWM curve will produce audible beats (difference-frequency
  interference) as they slip into and out of phase. Dither breaks the
  phase-lock by perturbing each fan's duty cycle independently by a
  few PWM units per tick.
- The YAML example:

```yaml
curves:
  case_fans:
    kind: points
    sensor: CPU Package
    anchors:
      - {temp: 40, pwm: 40}
      - {temp: 80, pwm: 180}
    dither_pct: 2    # ±5 PWM (2% of 255) per tick per channel
```

- Tuning guidance: start with 1%. Beats typically audible with
  identical fans on zero-dither; usually suppressed with 2-3%. Above 4%
  is audible as low-level noise.
- Note on RPM stability: dither adds high-frequency PWM jitter. On
  most fans, the mechanical inertia smooths this to a <0.5% RPM
  variation — imperceptible. On PWM-sensitive fans (rare), prefer ≤1%.
- Compose-with-PI note: dither and PI curves compose safely; dither does
  not wind up the integral term.

## Tests you MUST add (R19-compliant task-bound invariants)

In `internal/controller/dither_test.go` (new file):

1. `TestDither_ZeroPctIsNoop` — curve returns 128; dither_pct=0;
   assert output is exactly 128 across 1000 ticks.
2. `TestDither_StaysWithinBound` — dither_pct=5, curve returns 128;
   over 10000 ticks, assert every output ∈ `[128-13, 128+13]` (since
   5% of 255 = 12.75, rounded up).
3. `TestDither_ClampsAtLowEdge` — curve returns 0, dither_pct=5; over
   1000 ticks, no output below 0 (all ≥ 0 since clamp fires).
4. `TestDither_ClampsAtHighEdge` — curve returns 255, dither_pct=5;
   over 1000 ticks, no output above 255.
5. `TestDither_DecorrelatedAcrossChannels` — two channels, same
   curve, same dither_pct=3; collect 1000 samples each; assert
   Pearson correlation |r| < 0.1. This is the key anti-beat invariant.
6. `TestDither_Mean_IsZeroWithinTolerance` — curve returns 128,
   dither_pct=3, over 100000 ticks: mean of outputs is within ±0.5 of
   128. (Symmetric distribution assertion.)
7. `TestDither_ConfigValidation_RejectsBadValues` — table-driven:
   dither_pct=-0.1, dither_pct=5.01, dither_pct=NaN. Each returns
   named-field error.
8. `TestDither_RNGSurvivesReloadWithoutValueChange` — seed RNG,
   take 10 samples, simulate reload with same dither_pct, take 10
   more samples. Assert the RNG stream continues (the 11th sample is
   *not* the same as the 1st sample — RNG not re-seeded).

## Out of scope for this PR

- Phase-offset dither (sinusoidal instead of uniform random). Uniform
  random is strictly simpler and solves the beat problem.
- Per-channel dither override (override curve-level with channel-level).
  YAGNI; add later if a real config demands it.
- UI to preview the dither amount.
- Dither in Mix sub-curves (explicitly pinned as "outermost curve only"
  above).
- Binding to a `.claude/rules/` file.

## Definition of done

- Curve config struct has `DitherPct` field; validated.
- Controller applies dither post-curve, pre-write, per-channel RNG
  state initialized at startup, clamped 0-255, SIGHUP handling as
  specified.
- `docs/config.md` has the §Dither subsection with YAML example +
  tuning + compose-with-PI note.
- `CHANGELOG.md`: entry under `## Unreleased / ### Added`.
- All 8 tests pass; race detector clean.
- go vet / golangci-lint / gofmt clean.
- `CGO_ENABLED=0 go build ./...` clean.
- Existing controller tests unchanged and still pass.

## Branch and PR

- Branch: `claude/P4-DITHER-01-per-curve-dither`
- PR title: `feat(controller): per-curve dither to de-sync paired fans (P4-DITHER-01)`
- Open as ready-for-review (NOT draft).
- PR body includes the YAML example and a short explanation of when an
  operator would enable dither.

## Constraints

- Files touched (allowlist):
  - `internal/config/config.go`
  - `internal/controller/controller.go`
  - `internal/controller/dither_test.go` (new)
  - `docs/config.md`
  - `CHANGELOG.md`
  - `config.example.yaml` (add a `dither_pct` example stanza under an
    existing curve)
- No new dependencies. `math/rand/v2` is stdlib.
- `CGO_ENABLED=0` compatible.
- Preserve every existing safety guarantee. pwm_enable handling is
  untouched.

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS as standard.
- Additional section: DITHER_DEMO — 10 consecutive (tick, pwm_in,
  offset, pwm_out) tuples from a 128-PWM input at dither_pct=3, so
  Cowork can eyeball that outputs vary and stay in range.
- Additional section: DECORRELATION_CHECK — print the Pearson
  correlation computed in TestDither_DecorrelatedAcrossChannels. It
  should be near zero (|r| < 0.1).

## Final note

This task runs in parallel with P4-PI-01 and P4-HYST-01. Shared file:
`internal/controller/controller.go`. Rebase conflict on the Controller
struct field block is expected and trivial — the three new maps
(piState, hystState, ditherRNG) sit adjacent. Whoever merges third
rebases with all three present.
