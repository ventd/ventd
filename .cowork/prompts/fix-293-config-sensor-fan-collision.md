# fix-293-config-sensor-fan-collision

You are Claude Code. Cassidy's issue #293: `config.validate` tracks sensor
names in a `sensors map[string]struct{}` and fan names in a `fans map[string]Fan`
but never intersects the two sets. A config with `sensor.name="cpu"` and
`fan.name="cpu"` passes validation; at runtime `HistoryStore.Record` keys
ring buffers by raw metric name, so the sensor's °C samples and the fan's
duty samples interleave into the same ring. Sparklines paint zigzags of
interleaved temperature and PWM; `/api/history` returns mixed streams.

## Goal

Add a cross-namespace uniqueness check in `config.validate` after both
sensors and fans maps are populated. One-line error; rejects at load
rather than letting it corrupt runtime state silently.

## Context you should read first

- `internal/config/config.go` — specifically the `validate()` function;
  look for the two `make(map[...])` calls that populate `sensors` and
  `fans`.
- `internal/config/config_test.go` — existing validation tests; you'll
  add one alongside.
- `internal/web/history.go` `RecordStatus` — the collision site.

## What to do

1. In `internal/config/config.go` `validate()`, after both the sensors
   and fans loops populate their respective maps, add:

```go
for name := range fans {
    if _, clash := sensors[name]; clash {
        return fmt.Errorf(
            "config: %q is used as both a sensor name and a fan name; "+
                "names must be unique across sensors and fans so history "+
                "keyspace stays unambiguous", name)
    }
}
```

   Variable names (`sensors`, `fans`) should match whatever validate() already
   uses. Do not introduce new helpers.

2. Add to `internal/config/config_test.go` a new test
   `TestValidate_SensorFanNameCollision` that:
   - Constructs a minimal config with a sensor named "cpu" and a fan
     named "cpu", otherwise valid.
   - Calls Parse + validate.
   - Asserts the error is non-nil and the message contains the
     substring `both a sensor name and a fan name`.
   - Also asserts a sibling case where names are distinct still validates
     successfully (guards against the new check flagging innocents).

3. Note the R19 magic-comment binding on the test (Mia's #290 might or
   might not have landed yet — either way, include the binding comment):

```go
// regresses #293
func TestValidate_SensorFanNameCollision(t *testing.T) { ... }
```

   If the regresslint doesn't yet recognize `// regresses #N` (because
   fix-290 hasn't merged when this PR opens), the test still documents
   the binding for humans and will start being picked up once fix-290
   lands. No harm.

## Definition of done

- `internal/config/config.go` `validate()` rejects sensor/fan name
  collisions.
- `TestValidate_SensorFanNameCollision` in config_test.go passes.
- Existing validate() tests still pass.
- go vet / golangci-lint / gofmt clean.
- CGO_ENABLED=0 build clean.
- CHANGELOG entry under `## Unreleased / ### Fixed` —
  "config: reject sensor/fan name collisions at load (fixes #293)."
- PR references `Fixes #293`.
- Magic-comment binding `// regresses #293` present on the new test.

## Out of scope

- Namespacing HistoryStore keys (Cassidy's alternate suggestion; more
  invasive, not this PR).
- Any other validation additions.
- Changes outside `internal/config/`.
- Tests unrelated to the name-collision case.

## Branch and PR

- Branch: `claude/fix-293-config-name-collision`
- PR title: `fix(config): reject sensor/fan name collisions at load (fixes #293)`
- Open as ready-for-review (NOT draft).

## Constraints

- Files touched (allowlist):
  - `internal/config/config.go`
  - `internal/config/config_test.go`
  - `CHANGELOG.md`
- No new dependencies.
- `CGO_ENABLED=0` compatible.

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS as standard.
- Additional section: TEST_RUN — paste the go test output showing the
  new test passing AND the happy-path sibling test passing.

## Time budget

15 minutes wall-clock.

## Final note

Smallest Phase 1.5 fix on the board. Sonnet-eligible. Parallel-safe with
everything except anything else touching `internal/config/config.go`.
