# P4-HYST-01 — banded hysteresis (quiet/normal/boost)

**Care level: MEDIUM.** This is a controller behavioural change. It does
not touch pwm_enable save/restore or watchdog paths, but it does change
the shape of every tick's curve evaluation for any config that uses
hysteresis. Bad transitions produce audible toggle-bounce, not unsafe
hardware states — but "audible" is the whole point of this daemon so
treat it as user-visible.

## Task

- **ID:** P4-HYST-01
- **Track:** CTRL (Phase 4)
- **Goal:** Replace the existing single-scalar hysteresis with a
  three-band model (quiet / normal / boost). Each band has its own
  temperature window with independent entry and exit thresholds, so a
  fan doesn't oscillate at a band boundary. Legacy scalar-hyst configs
  migrate cleanly to an equivalent banded shape.

## Context you should read first

- `internal/controller/controller.go` — the tick loop and current
  hysteresis application. Locate the existing `Hysteresis` field
  (scalar, probably °C or PWM delta) and trace every call site.
- `internal/config/config.go` — the current `Hysteresis` field in
  whatever curve-level or controller-level struct holds it. Note the
  YAML tag and validation logic.
- `internal/curve/points.go` — how per-tick evaluation flows from
  sensor → curve → PWM, so you know where the banding layer sits.
- `docs/config.md` — existing hysteresis docs (if any). You'll rewrite
  the section.
- `CHANGELOG.md` — entry goes under `## Unreleased / ### Changed`
  (behavioural change, not pure addition, because the YAML schema
  evolves even with migration).

## Design — read carefully, do not deviate

### Band model

Three bands, evaluated in order:

```
quiet  : temp < quiet_exit              → PWM from Quiet curve slice (low)
normal : quiet_enter <= temp < boost_exit → PWM from Normal curve slice
boost  : temp >= boost_enter            → PWM from Boost curve slice (high)
```

Thresholds satisfy: `quiet_exit <= quiet_enter < boost_exit <= boost_enter`.
The two gaps `[quiet_exit, quiet_enter)` and `[boost_exit, boost_enter)` are
the hysteresis bands themselves — once in a band, you stay in it until the
temperature crosses the *other* edge of the band.

This is NOT four thresholds specified independently. It is two pairs:
`{quiet_enter, quiet_exit}` and `{boost_enter, boost_exit}`. The
`quiet_enter > quiet_exit` relation (enter at a higher temperature than
exit) is what prevents oscillation.

### Struct shape

In `internal/config/config.go`:

```go
// HysteresisBands is the banded hysteresis configuration for a single
// controller channel. When present it supersedes the legacy scalar
// Hysteresis field. Zero-value HysteresisBands means "no hysteresis"
// (single-band behaviour, no latching).
type HysteresisBands struct {
    // QuietEnter is the temperature (°C) above which the controller
    // leaves the quiet band and enters normal. Must be >= QuietExit.
    QuietEnter float64 `yaml:"quiet_enter"`

    // QuietExit is the temperature below which the controller returns
    // to the quiet band from normal. Must be <= QuietEnter.
    QuietExit float64 `yaml:"quiet_exit"`

    // BoostEnter is the temperature above which the controller leaves
    // normal and enters boost. Must be >= BoostExit.
    BoostEnter float64 `yaml:"boost_enter"`

    // BoostExit is the temperature below which the controller returns
    // to normal from boost. Must be <= BoostEnter and >= QuietEnter.
    BoostExit float64 `yaml:"boost_exit"`
}
```

Validation (in `validate()`):
- `QuietExit <= QuietEnter` (else named-field error)
- `BoostExit <= BoostEnter`
- `QuietEnter < BoostExit` (the two bands don't overlap)
- All four values finite, non-NaN.
- If any of the four fields is non-zero, all four must be provided (no
  half-configured bands).

### Migration from legacy scalar

If a config has the old `Hysteresis: <float>` scalar (symmetric
width around a curve's notional midpoint) and no `HysteresisBands`,
synthesise bands at load time:

- Pick a reference temperature `T_ref` from the associated curve's
  midpoint (for a Points curve, that's `Anchors[len/2].Temp`; for
  Linear, it's `(Linear.TempMin + Linear.TempMax) / 2`). Document this
  heuristic.
- `QuietEnter  = T_ref - scalar/2`
- `QuietExit   = T_ref - scalar`
- `BoostEnter  = T_ref + scalar`
- `BoostExit   = T_ref + scalar/2`

This produces a two-band-equivalent shape: scalar hysteresis was
symmetric around a midpoint, and this mapping preserves that symmetry
while gaining the band-exit/enter distinction. Log an INFO event on
each such migration:
`event=hysteresis_migrated_scalar_to_bands curve=<name> scalar=<val> ref=<T_ref>`.

The legacy scalar field stays in the schema for one release (Deprecated
comment + it's still parsed), with a WARN logged on load. Removal is a
separate future task; do not delete the scalar field in this PR.

### Controller integration

Per-channel state (mirroring P4-PI-01's `piState` pattern):

```go
// hystBand tracks which band a channel is currently in. Persists across
// ticks until a band edge is crossed. Zero value = bandNormal.
type hystBand int

const (
    bandNormal hystBand = iota
    bandQuiet
    bandBoost
)

// On the Controller struct:
hystState map[string]hystBand
```

Initialize `bandNormal` for every channel at controller start. In
`tick()`, BEFORE curve evaluation:

```go
// channelID derived from the per-channel loop variable, whatever its name is
cur := c.hystState[channelID]
switch cur {
case bandQuiet:
    if temp >= bands.QuietEnter { cur = bandNormal }
case bandNormal:
    if temp < bands.QuietExit     { cur = bandQuiet }
    if temp >= bands.BoostEnter   { cur = bandBoost }
case bandBoost:
    if temp < bands.BoostExit     { cur = bandNormal }
}
c.hystState[channelID] = cur
```

On every band transition, emit a structured event at INFO level:
`event=hysteresis_band_change channel=<id> from=<prev> to=<new> temp=<val>`.

How the band influences PWM is explicitly NOT specified by this task —
it's a lookup-shift left to the curve type. For now, the band is
*observed* state but doesn't alter PWM output. The immediate value of
this PR is the band-tracking infrastructure plus the structured events.
A follow-up (not in this PR) will let curves consume `band` as an
additional input. This is deliberate: ship observable state first, then
wire consumers incrementally.

That means this PR's behaviour-diff on merged main is: structured log
events at band transitions, a new config schema section, and a new
per-channel state map. No PWM numbers change. The existing scalar
hysteresis mechanism continues to drive PWM output in this release.

### SIGHUP reload

On config reload, if bands for a channel change: reset that channel's
`hystState[channelID] = bandNormal`. Log `event=hysteresis_reset_on_reload channel=<id>`.

### Docs

In `docs/config.md`, replace or extend the current hysteresis section:

- Two-paragraph explanation of why bands beat scalar hyst (single
  scalar gives one edge; bands give four — prevents the pathology where
  a fan at the edge of its normal range toggles on every small temp
  fluctuation).
- Full YAML example with all four fields:

```yaml
hysteresis_bands:
  quiet_enter: 52.0    # °C — leaving quiet band
  quiet_exit:  48.0    # °C — returning to quiet
  boost_enter: 78.0    # °C — entering boost
  boost_exit:  72.0    # °C — leaving boost
```

- Migration note: if you previously used `hysteresis: 3.0`, the system
  auto-generates bands around your curve's midpoint. To opt into
  explicit bands, replace the scalar with a `hysteresis_bands` block.
- Show what the structured events look like so operators know what to
  grep journald for.

## Tests you MUST add (R19-compliant task-bound invariants)

In `internal/controller/hysteresis_test.go` (new file):

1. `TestHystBand_StaysInQuiet_WhileBelowEnter` — ramp temp from
   `quiet_exit - 5` up to `quiet_enter - 0.1` over 50 ticks; assert
   band stays `bandQuiet` the entire time.
2. `TestHystBand_EntersNormal_AtQuietEnter` — crossing `quiet_enter` by
   0.01 transitions to `bandNormal` on that same tick.
3. `TestHystBand_DoesNotReturnToQuiet_UntilQuietExit` — after entering
   normal, drop temp to exactly `quiet_enter - 0.01`. Assert still
   `bandNormal`. Drop to `quiet_exit - 0.01`; assert `bandQuiet` now.
4. `TestHystBand_BoostAnalogues` — mirror tests 1-3 for the
   boost/normal boundary.
5. `TestHystBand_NoOscillation_AtEdge` — hold temp at exactly
   `quiet_enter` for 1000 ticks; count band transitions. Expect ≤1.
6. `TestHystMigration_LegacyScalarToBands` — parse a config with
   `hysteresis: 4.0` and no `hysteresis_bands`; assert bands populated
   via the midpoint heuristic with the correct arithmetic.
7. `TestHystValidation_RejectsInverted` — table-driven over
   `{QuietExit > QuietEnter}`, `{BoostExit > BoostEnter}`,
   `{QuietEnter > BoostExit}`, NaN inputs, half-populated bands. Each
   returns a named-field error.
8. `TestHystState_ResetsOnReload` — start in bandBoost, issue
   reload-equivalent call with different bands; assert state for that
   channel is bandNormal.

## Out of scope for this PR

- Letting curves actually *use* the band to shift PWM output. That's a
  follow-up — ships structured events first, PWM shaping second.
- UI affordances for bands.
- Removing the legacy scalar field.
- PI curves consuming bands (P4-PI-01 and P4-HYST-01 compose cleanly
  because P4-PI-01 doesn't touch the band system; future task
  integrates the two).
- Binding to a `.claude/rules/` file.

## Definition of done

- `internal/config/config.go`: `HysteresisBands` struct, validation,
  migration from legacy scalar at load.
- `internal/controller/controller.go`: `hystState` map, per-tick band
  update, structured event on transition, reload-reset.
- `docs/config.md`: banded hysteresis section with YAML example +
  migration note + event format.
- `CHANGELOG.md`: entry under `## Unreleased / ### Changed` noting the
  schema evolution + auto-migration.
- All 8 tests pass; race detector clean.
- go vet / golangci-lint / gofmt clean.
- `CGO_ENABLED=0 go build ./...` clean.
- Existing controller tests unchanged and still pass.

## Branch and PR

- Branch: `claude/P4-HYST-01-banded-hysteresis`
- PR title: `feat(controller): banded hysteresis (quiet/normal/boost) (P4-HYST-01)`
- Open as ready-for-review (NOT draft).
- PR body includes a before/after YAML sample and the structured-event
  example string.

## Constraints

- Files touched (allowlist):
  - `internal/config/config.go`
  - `internal/controller/controller.go`
  - `internal/controller/hysteresis_test.go` (new)
  - `docs/config.md`
  - `CHANGELOG.md`
  - `config.example.yaml` (add a `hysteresis_bands` example stanza)
- No new dependencies.
- `CGO_ENABLED=0` compatible.
- Preserve every existing safety guarantee. pwm_enable handling is
  untouched by this change.

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS as standard.
- Additional section: MIGRATION_DEMO — paste a legacy YAML fragment
  (`hysteresis: 4.0`) and the resulting auto-generated bands from the
  loader, so Cowork can eyeball the arithmetic without running the
  loader.
- Additional section: EVENT_LOG_SAMPLE — three sample structured-log
  lines emitted by a simulated up-ramp through all three bands.

## Final note

This task runs in parallel with P4-PI-01. They touch different areas
(controller+config vs curve+controller) — the one shared file,
`internal/controller/controller.go`, needs both PRs to land without
clobbering each other. Whoever merges second will likely need a trivial
rebase in the controller struct field block (add your `hystState` field
adjacent to the other's `piState` field). That's expected; do not try
to pre-merge.
