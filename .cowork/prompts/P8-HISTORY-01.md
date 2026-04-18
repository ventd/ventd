# P8-HISTORY-01 — in-process 30-min ring + GET /api/history

**Care level: LOW.** Additive observability. A bounded in-memory ring
buffer holds the last 30 minutes of tick data per channel. No
persistence (bounded memory is explicit non-goal to keep). Queried via
a new authenticated HTTP endpoint.

## Task

- **ID:** P8-HISTORY-01
- **Track:** HISTORY (Phase 8)
- **Goal:** Per-channel time-series ring buffer holding 30 minutes of
  (timestamp, pwm, rpm, temp) tuples. Exposed via
  `GET /api/history?channel=<id>&minutes=<n>` with memory cap enforced.

## Context you should read first

- `internal/web/api.go` — existing API handler patterns. Note how
  auth + JSON response are wired.
- `internal/web/history.go` — IF already present from previous work,
  inspect before adding. If it exists from the #223 sparkline work,
  this task likely wraps the existing store with a real API.
- `internal/controller/controller.go` — where ticks originate. You'll
  add a `historyStore.Record(...)` call after each successful
  curve-evaluate cycle.
- `internal/monitor/` directory — where existing monitoring code lives.
  `history.go` likely belongs here.

## Design — read carefully, do not deviate

### Ring buffer

```go
type Sample struct {
    TS        int64   // Unix milliseconds
    PWM       uint8
    RPM       uint32
    TempMilli int32   // °C * 1000 (to avoid float in the hot path)
}

type Ring struct {
    mu      sync.RWMutex
    samples []Sample  // ring, size = 30*60 = 1800 @ 1Hz
    head    int
    size    int
}

func (r *Ring) Add(s Sample) { ... wraps on head reaching end }

func (r *Ring) Range(sinceTS int64) []Sample { ... returns copy }
```

`samples` is sized to exactly `capacity = retention_seconds * tick_hz`
for the configured retention. With 1Hz ticks and 30-min retention
that's 1800 samples per channel * ~23 bytes each = ~41 KB per channel.
For a system with 20 channels that's 820 KB — within R15 easily.

Global cap: 50 channels * 1800 samples * 23 bytes ≈ 2 MB total. Document
this cap. If a config exceeds 50 channels (extreme edge case), the
controller logs a WARN and skips history recording on channels past 50.

### Per-channel map

```go
type Store struct {
    mu       sync.RWMutex
    rings    map[string]*Ring
    capacity int  // per-channel samples capacity
}

func (s *Store) Record(channelID string, sample Sample) { ... }
func (s *Store) Query(channelID string, sinceTS int64) []Sample { ... }
```

Lazy ring creation on first Record per channel. Channel deregister on
config reload should clean up the ring (else memory leak on
add/remove/add cycles).

### Controller integration

In `tick()`, AFTER the successful write:

```go
if c.history != nil {  // nil when disabled
    c.history.Record(channelID, monitor.Sample{
        TS:        time.Now().UnixMilli(),
        PWM:       pwm,
        RPM:       reading.RPM,
        TempMilli: int32(sensors[primarySensor] * 1000),
    })
}
```

Zero allocation per tick is non-negotiable. Pre-size the samples slice,
use value types, no map writes inside Record (map access should only
be for channel-level ring lookup, which is amortised O(1)). Verify
with `go test -benchmem -run=^$ -bench=BenchmarkRecord` showing zero
allocs/op.

### HTTP endpoint

```
GET /api/history?channel=<id>&minutes=<n>
```

- Auth: same as all /api/* — session cookie or setup token.
- `channel`: required. Must match a known channel ID. 404 on missing.
- `minutes`: optional; default 5, max 30. Out-of-range clamped silently
  to valid range (log at INFO so operators see the clamp if they
  expected 60 and got 30).
- Response: JSON array of samples, oldest first.
  ```json
  [
    {"ts": 1755043200000, "pwm": 128, "rpm": 1450, "temp_c": 42.5},
    ...
  ]
  ```
- Temp is decoded from `TempMilli / 1000` before JSON output.
- Cache-Control: `no-store` (data is live).

### Config

Add to `Observability` (same section as P8-METRICS-01; you may rebase
on that PR if it lands first):

```go
History struct {
    Enabled           bool          `yaml:"enabled"`
    RetentionMinutes  int           `yaml:"retention_minutes"`  // default 30, max 60
}
```

Default: `enabled: true` (this feature is cheap enough to ship on by
default — unlike metrics, there's no security-sensitive endpoint
surface exposed beyond authenticated /api/history). 60-min cap is
hard; anything higher rejected at load.

### Tests (R19-compliant)

1. `TestHistory_RingWraps_Correctly` — insert 2000 samples into a
   1800-capacity ring, assert Range returns exactly the most recent
   1800 and in chronological order.
2. `TestHistory_QuerySince_FiltersCorrectly` — insert 1000 samples
   with TS spanning an hour; Query(since=TS500) returns samples 500-999.
3. `TestHistory_UnknownChannel_Returns404` — GET with unknown channel
   returns 404 with JSON error body.
4. `TestHistory_MinutesClamped` — GET with minutes=60 clamps to 30,
   minutes=0 clamps to 1, minutes=-5 clamps to 1.
5. `TestHistory_RecordZeroAlloc` — benchmark with -benchmem; assert 0
   allocs/op.
6. `TestHistory_DisabledInConfig_NoRecording` — enabled=false, Record
   is no-op, /api/history returns 503 "history disabled".
7. `TestHistory_RetentionLimit_Enforced` — config with
   `retention_minutes: 90` rejected at validate.
8. `TestHistory_Unauthenticated_Returns401` — auth surface matches.

## Out of scope for this PR

- Persistence to disk. 30-min in-memory is by design.
- Compression.
- SSE streaming endpoint (future UI work).
- Historical aggregation (avg over 1h).
- Multi-channel query.

## Definition of done

- `internal/monitor/history.go` with Ring + Store.
- `internal/web/history.go` with GET handler (update if pre-existing;
  preserve any pre-existing RecordStatus calls from #223).
- `internal/controller/controller.go`: call history.Record in tick.
- `internal/config/config.go`: Observability.History section.
- `docs/operations/history.md`: API reference + response schema.
- `CHANGELOG.md`: entry under `## Unreleased / ### Added`.
- All 8 tests pass + benchmark shows 0 allocs on Record.
- `CGO_ENABLED=0` build clean.
- go vet / golangci-lint / gofmt clean.

## Branch and PR

- Branch: `claude/P8-HISTORY-01-ring-api`
- PR title: `feat(monitor/web): 30-min history ring + /api/history (P8-HISTORY-01)`
- Open as ready-for-review (NOT draft).

## Constraints

- Files touched (allowlist):
  - `internal/monitor/history.go` (new or updated — check first)
  - `internal/monitor/history_test.go` (new)
  - `internal/web/history.go` (new or updated)
  - `internal/web/history_test.go` (new)
  - `internal/controller/controller.go` (single call site)
  - `internal/config/config.go`
  - `docs/operations/history.md` (new)
  - `CHANGELOG.md`
  - `config.example.yaml`
- No new dependencies.
- `CGO_ENABLED=0` compatible.
- Zero allocations in Record (non-negotiable; verified by benchmark).
- Preserve all safety guarantees.

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS as standard.
- Additional section: MEMORY_FOOTPRINT — compute per-channel bytes at
  the default config; state the 50-channel cap total.
- Additional section: BENCHMARK_RESULT — paste the `go test -bench`
  output showing allocs/op.
- Additional section: API_SAMPLE_RESPONSE — paste a hypothetical 5-sample
  JSON response so reviewers can spot-check the shape.

## Final note

Parallelizable with P8-METRICS-01 and P8-CLI-01. `internal/config/config.go`
conflict expected on the Observability section. If P8-METRICS-01 lands
first, rebase your History subsection under the existing Observability
struct. Trivial.
