# Smart-mode load-test design — predict-not-react training rig

**Status:** design (not yet implemented)
**Target host:** Phoenix's MSI Z690-A DDR4 desktop (NCT6687D + RTX 4090)
**Scope:** v0.5.9 confidence-gated controller (Layer A + B + C aggregator)
**Goal:** drive enough representative load against the host that the smart-mode
algorithm clears its warmup gates and the predictive arm starts cooperating
with — and eventually leading — the reactive arm.

## Why this test exists

The v0.5.9 confidence-gated controller blends a **predictive** PI-controller
output (driven by Layer-B coupling estimates and Layer-C marginal-benefit
estimates) with the **reactive** v0.4.x curve output, weighted per channel by
`w_pred ∈ [0, 1]`:

```
output = w_pred · predictive + (1 − w_pred) · reactive
```

For `w_pred` to climb above zero — i.e., for predictive to start earning a
slice of the output — every layer must clear its warmup gate:

| Layer | Warmup gate | Time-to-converge under realistic load |
|---|---|---|
| A (per-channel reactive coverage) | ≥ 3 obs in ≥ 6 of 16 PWM bins | minutes |
| B (per-channel coupling RLS) | n_samples ≥ 5·d² AND tr(P) ≤ 0.5·tr(P_0) AND κ ≤ 10⁴ | tens of minutes |
| C (per-(channel, signature) marginal RLS) | n_samples ≥ 20 AND tr(P) ≤ 0.5·tr(P_0) AND parent B converged | tens of minutes |
| Aggregator | LPF τ_w = 30 s; cold-start hard-pin = 5 min after Envelope C | minutes |

A passive observation log will eventually populate these — but on a desktop
that mostly sits idle, observation-only convergence takes weeks. The load
test compresses that into a single ~3-hour run by deliberately driving each
warmup gate's load surface.

## What "good" training data looks like

Three independent properties have to be true together:

1. **Workload diversity.** The signature library (R7) keys per-(channel,
   workload) state on a SipHash of `/proc/PID/comm` for the top-K=4
   contributing processes. To create useful Layer-C shards, the run must
   exercise at least three distinct K-stable signatures: idle baseline, an
   all-core compute load, and a mixed CPU+GPU load. Each signature must hold
   for ≥ 6 seconds (the M=3 K-stable promotion gate at 2s ticks) so the
   library actually creates the bucket.

2. **PWM coverage.** Layer A's coverage metric counts bins (16 raw PWM units
   each) with ≥ 3 observations. Reactive curves under heavy load only
   exercise the upper bins — a cold-only run would clear ~25% of bins. To
   clear ≥ 6 bins we need to traverse the curve from idle through to
   thermal-saturation, then back down for cool-down. Idle → ramp → peak →
   cool-down → idle is the minimum cycle.

3. **Δ-pwm with measurable Δ-T.** Layer B/C's RLS estimator needs samples
   where `|ΔT| ≥ 2°C` (R11 noise floor) responds to a measurable Δ-pwm.
   That means: don't just hold steady at full-tilt — induce changes. The
   stair-step ramp phase (below) is the load-bearing piece for B/C
   convergence.

## Workload phase plan

Total runtime ≈ 100 minutes per pass, 3 passes back-to-back ≈ 5 hours
unattended. Each pass is self-contained (signatures persist; warmup gates
accumulate across passes via the persisted KV state).

| # | Phase | Duration | Stimulus | Expected Δ-temp | Why |
|---|---|---|---|---|---|
| 0 | Pre-flight | 1 min | none | 0 | confirm `ventd` running, `nvidia-smi` works, baseline temps captured |
| 1 | Idle baseline | 5 min | nothing | 0 | populate `signature/idle` bucket; conf_A coverage on low-PWM bins |
| 2 | Single-thread CPU | 3 min | `stress-ng --cpu 1 --cpu-method all` | +5–10°C CPU | populate signature for one-thread compute; minimal GPU load |
| 3 | All-core CPU sustained | 8 min | `stress-ng --cpu 0 --cpu-method matrixprod -t 8m` | +25–40°C CPU | populate `signature/cpu-allcore`; saturate CPU-fan curve high-bins |
| 4 | Cool-down | 5 min | nothing | back to baseline | high-low sweep for coverage; re-populate `signature/idle` |
| 5 | GPU compute | 5 min | `gpu-burn 300` (or `nvidia-smi -q -d POWER` loop + a CUDA mat-mul kernel) | +20–30°C GPU | populate `signature/gpu-compute`; exercise GPU-fan channel |
| 6 | Cool-down | 5 min | nothing | back to baseline | another high-low sweep for both fans |
| 7 | Mixed CPU + GPU | 8 min | stress-ng + gpu-burn in parallel | +30–40°C across both | populate `signature/mixed-load`; co-vary CPU and GPU fans for Layer-B coupling |
| 8 | Cool-down | 5 min | nothing | back to baseline | |
| 9 | Stair-step CPU ramp | 12 min | `stress-ng --cpu 0 --cpu-load X` X ∈ {25, 50, 75, 100} held 3 min each | gradient | the load-bearing phase for Layer-B/C: each step is a Δ-pwm event with measurable Δ-T |
| 10 | Cool-down + summary | 8 min | nothing | back to baseline | scrape final `/api/v1/confidence/status`; emit report |

After phase 10, w_pred should be:
- **Pass 1**: 0.0 → 0.05 (cold-start pin clears, Layer A coverage approaching gate)
- **Pass 2**: 0.05 → 0.15 (Layer B clearing warmup; Layer C admits at least one shard)
- **Pass 3**: 0.15 → 0.4+ (predictive earning a real slice; cost-gate stops refusing ramps)

If w_pred at the end of pass 3 is still below 0.1, the load test exposed a
real bug — either a warmup gate is too tight, the signature library never
promoted a bucket, or the aggregator's LPF is dampening too hard. That is
the value of the test: it produces a falsifiable convergence target.

## Instrumentation

### Existing endpoints we can poll

| Endpoint | Polling rate | What we capture |
|---|---|---|
| `GET /api/v1/status` | 2 s | per-fan PWM, RPM, temps, controller-state |
| `GET /api/v1/confidence/status` | 2 s | per-channel `w_pred`, `ui_state`, `conf_a/b/c`, `tier`, `coverage`, `seen_first_contact`, `age_seconds`, `preset`, `global_state` |
| `journalctl -u ventd -o json` | tail -F | controller decisions, warmup-gate transitions, aborts |

The observation log itself (msgpack at `/var/lib/ventd/observation.log`) is
the ground-truth record but is binary; for the load test we capture it
en masse and decode post-run.

### Endpoints / fields we should add for this test

1. **`GET /api/v1/smart/snapshot`** (new) — single bundled call returning
   aggregator + layerA + layerB + layerC + signature library state. Today
   the dashboard pieces this together with two calls; the load test wants
   one call to keep the time-series clean.

2. **`X-Smart-Tick: <n>` response header** on `/api/v1/status` — lets the
   client dedupe samples that were taken within the same controller tick.

3. **Signature library snapshot endpoint** (`GET /api/v1/smart/signatures`)
   — returns the current promoted signature label, the per-bucket
   `HitCount`, `LastSeenUnix`, and the in-flight K-stable counter. Today
   this state is persisted to KV but not exposed. The load test wants to
   confirm that phases 1, 3, 5, 7 produce four distinct buckets.

These three additions are cheap (≤ 100 LOC each) and immediately useful
for any future smart-mode HIL work, not just this test.

### Optional but valuable

- **Per-tick NDJSON trace** (writer in the controller): one event per
  tick wrapped in the standard `internal/ndjson` envelope, payload =
  `{ts, channel, reactive_pwm, predictive_pwm, output_pwm, w_pred,
  conf_a, conf_b, conf_c, sensor_temp, rpm, ui_state}`. Gated behind
  an env var (`VENTD_SMART_NDJSON=/var/log/ventd/smart-trace.ndjson`)
  so it ships to no one by default. NDJSON over CSV because the trace
  carries typed fields (floats, bools, enums) that CSV would coerce
  to strings; `internal/ndjson.SchemaThermalTraceV1` is the schema
  constant. The `report.py` script consumes the file via the same
  msgpack→NDJSON path used by `ventd diag export-observations`.

## Pass / fail criteria

The test passes when, at the end of pass 3:

| # | Criterion | Threshold |
|---|---|---|
| 1 | At least one channel has `w_pred ≥ 0.4` for the final 60 s | hard |
| 2 | Signature library has ≥ 4 distinct active buckets | hard |
| 3 | Layer A coverage ≥ 0.5 on at least one fan channel | hard |
| 4 | Layer B `WarmingUp == false` on at least one channel | hard |
| 5 | Layer C has at least one warmed shard for `signature/cpu-allcore` | hard |
| 6 | No "first-contact clamp" log line in the last 30 min | soft |
| 7 | No `PIRefused: true` from the integrator-divergence guard | soft |
| 8 | No thermal aborts (>85°C absolute) at any point | hard (safety) |
| 9 | The dashboard's confidence-pill visited `cold-start → warming → converged` for ≥ one channel | soft |
| 10 | Predictive output diverged from reactive by ≥ 5 PWM units on at least one stair-step transition | soft |

If hard criteria 1–5 + 8 pass: predictive arm is functioning. If only 8
passes: hardware is safe but the smart-mode algorithm has an issue (most
likely a warmup gate is too tight; investigate via the captured time-series).

## Implementation outline

`tools/smartmode-loadtest/run.sh`:

```bash
#!/usr/bin/env bash
# Driven from: a Linux host with ventd running, stress-ng installed,
# gpu-burn built (NVIDIA only), and root or sudo for systemctl.
set -euo pipefail

VENTD_HOST="${VENTD_HOST:-http://127.0.0.1:9999}"
VENTD_TOKEN="${VENTD_TOKEN:?must set api token}"
OUT="$(date +%Y%m%d-%H%M%S)-smartmode-pass"
mkdir -p "$OUT"

# Safety: abort if any sensor exceeds ABORT_TEMP at any phase boundary.
ABORT_TEMP="${ABORT_TEMP:-85}"

phase() {
  local name="$1" duration="$2" stimulus="$3"
  echo "[$(date +%H:%M:%S)] phase=$name duration=${duration}s"
  bash -c "$stimulus" &
  local stim_pid=$!
  # poll endpoints every 2s, abort if temps exceed cap
  poll_loop "$duration" "$OUT/$name.csv" || { kill -TERM $stim_pid 2>/dev/null; exit 1; }
  kill -TERM $stim_pid 2>/dev/null || true
  wait 2>/dev/null || true
}

poll_loop() { … }   # curl /api/v1/status + /api/v1/confidence/status, append CSV row, abort on >ABORT_TEMP

phase preflight   60  ":"
phase idle1      300  "sleep 300"
phase cpu1       180  "stress-ng --cpu 1 --cpu-method all -t 180"
phase cpu_all    480  "stress-ng --cpu 0 --cpu-method matrixprod -t 480"
phase cooldown1  300  "sleep 300"
phase gpu        300  "gpu-burn 300"
phase cooldown2  300  "sleep 300"
phase mixed      480  "stress-ng --cpu 0 --cpu-method matrixprod -t 480 & gpu-burn 480"
phase cooldown3  300  "sleep 300"
phase stair_25   180  "stress-ng --cpu 0 --cpu-load 25 -t 180"
phase stair_50   180  "stress-ng --cpu 0 --cpu-load 50 -t 180"
phase stair_75   180  "stress-ng --cpu 0 --cpu-load 75 -t 180"
phase stair_100  180  "stress-ng --cpu 0 --cpu-load 100 -t 180"
phase cooldown4  480  "sleep 480"

# Aggregate + report
python3 tools/smartmode-loadtest/report.py "$OUT" > "$OUT/report.md"
```

`tools/smartmode-loadtest/report.py` produces a Markdown report with three
inline charts (matplotlib SVG):

1. `w_pred` per channel over time (target: monotonic climb across passes).
2. `conf_A`, `conf_B`, `conf_C` per channel over time (target: each clears
   their warmup ceiling at expected phase boundaries).
3. `predictive_pwm` vs `reactive_pwm` per channel (target: divergence after
   pass 1, with predictive damping the steepness of stair-step transitions).

Plus a verdict table: each pass/fail criterion above with an emoji + the
captured value.

## Hardware-specific notes for Phoenix's MSI Z690-A + RTX 4090

- **Fan inventory:** NCT6687D exposes 8 PWM channels (most boards wire
  4–6 to physical headers). The CPU fan is typically pwm1; case fans
  pwm2–pwm5; the AIO pump (if present) pwm6 or pwm7. The smart-mode
  algorithm needs the calibration data to know which is which — the load
  test assumes calibration has been completed in a prior wizard run.
- **GPU fan:** RTX 4090 reference card has 3 fans tied to `nvidia0`. NVML
  reports them as one logical channel. The `--enable-gpu-write` flag must
  be on the daemon for w_pred to drive the GPU fan; otherwise GPU is
  monitor-only and contributes only to coupling estimates, not to the
  blended-output write.
- **Tjmax:** Z690 reports Tjmax = 100°C; the safety abort at 85°C leaves
  15°C headroom for the predictive arm to spike during early passes
  before LPF damps it.
- **ABORT_TEMP override:** if Phoenix wants to push harder for stair-step
  data, `ABORT_TEMP=95` is the reasonable upper limit on consumer Z690
  silicon. Anything higher and stress-ng's `--cpu-method matrixprod` will
  run into Intel's PL2 throttle, which complicates Layer-B coupling
  estimates.

## Safety envelope

The test is **opt-in** and runs only when `VENTD_SMARTMODE_LOADTEST=1` is
exported. Hard caps:

- 85°C absolute abort on any sensor (configurable via ABORT_TEMP).
- 5°C/sec dT/dt abort (matches RULE-ENVELOPE-04).
- Run aborts cleanly: kill all stimulus processes, wait 30 s for cool-down
  before the next phase fires (or before exit).
- Output directory under `~/.ventd-smartmode-loadtest/` so a forgotten
  partial run doesn't fill `/var`.

## Repeatability

Three passes back-to-back gives the warmup gates time to compound. If a
single pass already clears every hard criterion, the test reports
"converged in pass 1" and exits early. Otherwise pass 2 and 3 run to
collect more samples. The persisted KV state at `/var/lib/ventd/state.yaml`
+ `/var/lib/ventd/smart/{shard-A,shard-B,shard-C}/*.cbor` is the durable
record — if the daemon is restarted between passes, persisted shards
re-load and the run picks up from where it left off.

## Rule bindings (proposed)

If we accept this test as a permanent CI / HIL fixture, three new rules
make sense:

- **RULE-SMARTMODE-LOADTEST-01** — three-pass run on a healthy desktop
  must produce `w_pred ≥ 0.4` on at least one channel by end of pass 3.
- **RULE-SMARTMODE-LOADTEST-02** — signature library must promote ≥ 4
  distinct buckets across the run.
- **RULE-SMARTMODE-LOADTEST-03** — no thermal aborts, no first-contact
  clamps after pass 1, no PIRefused-from-divergence-guard logs.

These rules can't run in CI without HIL hardware — they're explicitly
HIL-only and bound to `validation/smartmode-loadtest-<host>-<date>.md`
artefacts produced by the test rig (same pattern as RULE-INSTALL-05's
AppArmor HIL log).

## What we ship vs what we run today

**Ship in v0.5.11:**
- The three new endpoints (`/api/v1/smart/snapshot`, `/api/v1/smart/signatures`,
  `X-Smart-Tick` header).
- The optional NDJSON trace writer behind `VENTD_SMART_NDJSON` (uses
  `internal/ndjson.SchemaThermalTraceV1`).
- `tools/smartmode-loadtest/` directory with the bash runner + report.py.

**Run today (one-off, not v0.5.11 blocker):**
- The 5-hour three-pass HIL run on Phoenix's desktop.
- Capture the report, file as the first row of a future
  `validation/smartmode-loadtest-*.md` log series.
- If pass 3 fails any hard criterion, file an issue per failure with the
  captured time-series and triage; iterate on the warmup gate or
  aggregator before tag.

The load test is the canary that tells us "the algorithm actually
predicts" before we promote v0.5.11 from snapshot to tag.
