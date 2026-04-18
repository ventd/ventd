# P8-METRICS-01 — opt-in Prometheus /metrics endpoint

**Care level: LOW.** This is an additive observability feature. The
endpoint defaults off. Enabling it exposes control-loop internals as
Prometheus metrics over the existing authenticated HTTP interface.
Failure modes are benign (metric missing, wrong value type) and only
visible to operators who opted in.

## Task

- **ID:** P8-METRICS-01
- **Track:** METRICS (Phase 8)
- **Goal:** Add a Prometheus-compatible `/metrics` endpoint exposing
  per-channel fan state (RPM, PWM, temperature, curve band, PI
  integral), control-loop timing, and daemon-level counters (ticks,
  restarts, write failures). Gated by config flag `observability.metrics.enabled`.

## Context you should read first

- `internal/web/server.go` — existing HTTP server setup. You'll add
  one new route, gated on config.
- `internal/web/api.go` — how existing handlers are wired.
- `internal/controller/controller.go` — where the data lives that
  metrics will expose. Note the existing per-channel state (retry
  counters, RestoreOne triggers from #263, soon piState / hystState /
  ditherRNG from Phase 4).
- `internal/config/config.go` — add the `Observability` section.

## Design — read carefully, do not deviate

### Dependency choice

Use `github.com/prometheus/client_golang/prometheus/promhttp` as the
handler. This is the reference implementation. It's a moderate-sized
dependency (~500KB compiled) — the masterplan R15 says ≤100 KB binary
delta outside Phase 6 unless SIZE-JUSTIFIED. Observability is a
called-out exception in the test plan; 500KB is justified.

Confirm the dep is `CGO_ENABLED=0` compatible before adding to go.mod.
If not, document as a CONCERN and fall back to hand-written text-format
(Prometheus exposition is a simple line-oriented format).

### Config

```go
type Observability struct {
    Metrics struct {
        Enabled bool   `yaml:"enabled"`
        Path    string `yaml:"path"`       // default "/metrics"
    } `yaml:"metrics"`
}
```

Default: `enabled: false`. When `enabled: true` but `path` unset,
default to `/metrics`. Validation: path must start with `/` and not
collide with existing routes (`/api/*`, `/ui/*`).

### Metrics to expose

**Per-channel gauges** (labels: `channel_id`, `backend`, `role`):
- `ventd_fan_pwm` — current PWM 0-255.
- `ventd_fan_rpm` — current RPM reading.
- `ventd_temperature_celsius` — most-recent temp read for the channel's
  sensor.
- `ventd_hysteresis_band` — 0=quiet, 1=normal, 2=boost (when
  P4-HYST-01 lands; for now emit constant 1). Document this early-
  binding clearly; future-proofing the label today saves a metric
  rename later.
- `ventd_pi_integral` — current integral term (when P4-PI-01 lands; for
  now emit 0 for non-PI curves).

**Per-channel counters** (labels: `channel_id`):
- `ventd_pwm_write_total` — successful writes.
- `ventd_pwm_write_retry_total` — writes that needed a retry
  (from #263 retry path).
- `ventd_pwm_write_failed_total` — writes where both tries failed
  (RestoreOne fired).
- `ventd_restore_one_total` — RestoreOne invocations.

**Daemon-level gauges / counters:**
- `ventd_tick_duration_seconds` (histogram; buckets 0.0001 to 0.1 in
  8 buckets) — how long each tick took.
- `ventd_ticks_total` — counter of ticks since start.
- `ventd_uptime_seconds` — gauge.
- `ventd_watchdog_restore_total` — full Restore invocations on
  controller shutdown.
- `ventd_goroutines` — stdlib `runtime.NumGoroutine()`.

Follow Prometheus naming conventions strictly: lowercase, underscores,
unit suffix (`_seconds`, `_bytes`, `_total` for counters).

### Registration and lifecycle

At controller construction, if metrics enabled, create a
`prometheus.Registry` (not the default global — using a local registry
keeps our metrics isolated from any transitively-included library's
default metrics). Register each collector once; update values on each
tick via closures or by having the Controller expose a
`MetricsSnapshot() map[string]any` method that the collector's
`Collect` callback calls.

Critical pattern: use `prometheus.NewGaugeFunc` / `NewCounterFunc`
with closures pointing at controller state, NOT mutex-protected value
setters. This avoids a synchronization point in the hot tick path.
Read the tick-published state snapshot under the existing controller
lock; the scrape (probably ~15s cadence) is far less frequent than
ticks (~1s).

### HTTP handler

Add to the existing HTTP mux:

```go
if cfg.Observability.Metrics.Enabled {
    path := cfg.Observability.Metrics.Path
    if path == "" { path = "/metrics" }
    mux.Handle(path, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
}
```

Auth: `/metrics` is behind the same session-cookie / setup-token auth
as `/api/*`. Do NOT expose it unauthenticated — that would leak thermal
fingerprints to anyone on the LAN. Document this in the
section-specific CONCERN:

> "Metrics endpoint is authenticated by default. Prometheus scrapers
> need a session cookie or setup token. This is deliberate; the
> endpoint leaks hardware fingerprint + thermal behaviour which are
> fleet-identifying."

### Tests

Per masterplan this phase is out-of-scope-for-tests, BUT R19 allows
task-bound invariant tests. Add the following:

1. `TestMetrics_EndpointOff_Returns404` — metrics disabled in config,
   GET /metrics returns 404. Also verify no `/metrics` route registered.
2. `TestMetrics_EndpointOn_ReturnsExpectedMetrics` — metrics enabled,
   one channel with known state, GET /metrics (with auth) returns 200
   and the body contains `ventd_fan_pwm{channel_id="..."} <value>`.
3. `TestMetrics_UnauthenticatedRequest_Returns401or403` — auth path
   matches the rest of /api/*. Verify no bypass.
4. `TestMetrics_PathCollision_RejectedAtLoad` — config with
   `metrics.path: /api/status` is rejected at validate().

## Out of scope for this PR

- OTel traces (P8-OTEL-01, separate task).
- Push gateway integration.
- Fleet aggregation (P8-FLEET-01).
- Metric retention / history ring (P8-HISTORY-01).
- Per-tick structured JSON logs (already exist; no change).

## Definition of done

- `internal/web/metrics.go` with the handler and registry.
- `internal/controller/controller.go`: `MetricsSnapshot()` method.
- `internal/config/config.go`: `Observability` section.
- `docs/operations/metrics.md` (new file): list of metrics + sample
  Grafana dashboard JSON stub (optional, but nice).
- `CHANGELOG.md`: entry under `## Unreleased / ### Added` noting
  "Opt-in Prometheus /metrics endpoint (off by default)."
- `config.example.yaml` includes a commented-out
  `observability.metrics` stanza.
- All 4 tests pass; race detector clean.
- `CGO_ENABLED=0` build clean (verify client_golang is CGO-free).
- go vet / golangci-lint / gofmt clean.
- Binary size delta noted in PR; if >100 KB outside the
  client_golang library itself, investigate.

## Branch and PR

- Branch: `claude/P8-METRICS-01-prometheus-metrics`
- PR title: `feat(observability): opt-in Prometheus /metrics endpoint (P8-METRICS-01)`
- Open as ready-for-review (NOT draft).

## Constraints

- Files touched (allowlist):
  - `internal/web/metrics.go` (new)
  - `internal/web/metrics_test.go` (new)
  - `internal/web/server.go` (wire the mux route)
  - `internal/controller/controller.go` (add MetricsSnapshot method)
  - `internal/config/config.go` (Observability section)
  - `docs/operations/metrics.md` (new)
  - `CHANGELOG.md`
  - `config.example.yaml`
  - `go.mod` / `go.sum`
- One new dependency permitted: `github.com/prometheus/client_golang`.
- `CGO_ENABLED=0` compatible.
- Preserve all safety guarantees.
- No changes to the hot tick path's timing (metrics scrape is
  coupled only through a lock-free read of the published snapshot).

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS as standard.
- Additional section: BINARY_SIZE_DELTA — before vs after, in bytes.
- Additional section: METRIC_CATALOG — list every metric emitted,
  with unit and labels.
- Additional section: SCRAPE_SAMPLE — paste the actual /metrics text
  output from a test run (authenticated).

## Final note

Parallelizable with P8-HISTORY-01 and P8-CLI-01 (different files).
Shared: `internal/config/config.go` (each task adds its own
Observability-adjacent subsection). Adjacent-field rebase conflict
expected and trivial.
