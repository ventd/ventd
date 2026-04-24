# Spec 05 — Predictive thermal control (autolearning, short + long horizon)

**Status:** DRAFT — research complete, not ready for implementation.
**Depends on:** spec-03 (profile library + storage layout), spec-04 (PI controller + autotune).
**Masterplan IDs this covers:** NEW track P4-PREDICT-{01..04}, supersedes the notional P4-MPC-01 for v1.0.
**Target release:** v0.7.0 – v1.0.0 (four-phase rollout, see §9).
**Estimated session cost:** Sonnet, ~25–40 sessions over 3–6 months, $12–25 each. Four Opus consults ($3–6 each).
**Research source:** `docs/research/2026-04-predictive-thermal.md` (filed as non-binding research).

---

## Why this spec exists

ventd's v1.0 positioning is "world's first predictive Linux fan controller." Every competitor (fan2go, CoolerControl, thermald, fancontrol, liquidctl) is reactive — temperature rises, PWM rises after the fact. ventd anticipates thermal spikes and spins fans up *before* temperature moves, via two complementary mechanisms:

1. **Short-horizon (5–30 s)** — learned thermal plant model + feed-forward on power/utilisation derivatives.
2. **Long-horizon (30 s – 5 min)** — workload-signature anticipation from exec events and motif discovery.

This is the single largest feature ventd will ship. It is also the feature that most needs staged rollout: shadow-mode promotion, drift detection, and non-overrideable safety envelope are all load-bearing.

## Scope — what this session produces

Four PR groups (each group may be 2–4 PRs), strictly sequential. Each group gates a minor release.

- **Group A (v0.7.0)** — Phase 0 baseline: feed-forward + safety envelope
- **Group B (v0.8.0)** — Phase 1: ARX+RLS short-horizon predictive, shadow-promoted
- **Group C (v0.9.0)** — Phase 2: exec-signature workload pre-warm
- **Group D (v0.9.x – v1.0)** — Phase 3: matrix-profile motif mining + observability polish

Details in §9. Each group has its own DoD.

---

## 1. Goals

- 1.1 **Short-horizon anticipation.** Fan PWM begins rising on `dP/dt` or `d(util)/dt` before `dT/dt` becomes measurable.
- 1.2 **Long-horizon anticipation.** ventd recognises launch of known thermally-heavy workloads (compile, game, backup, VM boot, encode) and pre-warms fans.
- 1.3 **Zero config.** No user-specified workload→profile mapping. The mapping is learned.
- 1.4 **Safe by default.** A non-overrideable hard safety envelope runs in parallel with all learned components. Model divergence demotes to the conservative per-platform curve.
- 1.5 **Preserve ventd ethos.** Pure Go (CGO_ENABLED=0), linear-history conventional commits, `.claude/rules/*.md` invariants 1:1 with subtests, lightweight daemon.

## 2. Non-goals

- 2.1 Not a general-purpose ML framework. No PyTorch, no ONNX, no TensorFlow.
- 2.2 Not a replacement for thermald. ventd manages fans/pumps; thermald manages DVFS/RAPL. They complement each other.
- 2.3 No DVFS or P-state control. Fans and pumps only.
- 2.4 No deep learning (LSTM/GRU/Transformer). ARX+RLS is within 1–2 % of deep models at these horizons for ~1000× less cost.
- 2.5 No Gaussian Process or full Bayesian inference. RLS covariance gives the uncertainty signal we need.
- 2.6 No network submission of profiles or telemetry. Everything is local.

## 3. Signal acquisition contract

All signals arrive at a central `Sample` aggregator at 1 Hz (default) or 2 Hz (high-res mode). Per-source goroutine, typed channel, typed interface, fakes for testing.

- 3.1 **Mandatory signals.** Per-core temperature, per-core utilisation (delta `/proc/stat`), PWM, RPM, package power (RAPL on x86; `energy-counter` fallback on AMD without RAPL; zero on ARM/POWER), ambient proxy (mobo/chassis sensor).
- 3.2 **Optional signals.** Per-core scaling_cur_freq, PSI (`avg10` on cpu/memory/io), NVML via `purego` dlopen of `libnvidia-ml.so.1`, AMDGPU sysfs, `/proc/diskstats`, `/proc/net/dev`, process events.
- 3.3 **Process event sources, in preference order.** (a) netlink proc-connector via `vishvananda/netlink`, (b) eBPF `sched_process_exec` tracepoint via `cilium/ebpf` with embedded bytecode (opt-in), (c) 1 Hz `/proc` polling fallback.
- 3.4 **DMI fingerprint.** Read once at startup via sysfs (reusing spec-03 fingerprint path; see spec-03 amendment).
- 3.5 All sysfs reads wrapped behind typed interfaces (`Sensors`, `Power`, `GPU`, `Process`); fakes live in `internal/testfixture/`.

## 4. Short-horizon model (Phase 1, v0.8.0)

### 4.1 Model structure

Second-order ARX per fan-zone:

    T(k) = -a1·T(k-1) - a2·T(k-2) + b1·P(k-1) + b2·u(k-1) + b3·pwm(k-1) + b4·T_amb + ε

where P is package power, u is aggregate utilisation, pwm is the previous commanded PWM, T_amb is ambient proxy. Regressor dimension ≤ 12 per zone. One ARX model per fan-zone (CPU fan zone, case fan zone, GPU fan zone).

### 4.2 Online estimator

**Variable Forgetting Factor RLS** (Paleologu-Benesty-Ciochină 2008):

- Forgetting factor λ ∈ [0.95, 0.999], adapted from prediction-error energy.
- Covariance trace clamped to prevent windup during idle.
- Skip update when `||φ||` below excitation threshold (prevents pathological drift).
- After every update: clamp parameters to physics-derived box constraints (see §4.5).

### 4.3 Feed-forward pre-action

Computed every tick, applied even before Phase 1 model is trusted:

    pwm_ff = k_fp · dP/dt + k_fu · d(util)/dt

Derivatives via Savitzky-Golay coefficients on a 5-sample window (one-line init, ~20-line runtime). k_fp and k_fu are learned by correlating step-response overshoot with feedforward magnitude during autotune (spec-04 amendment provides the hook).

### 4.4 Controller tuning

**IMC-PI from learned plant.** Once ARX identifies an FOPDT approximation (gain K, time constant τ, dead time θ), the IMC rules give:

    K_c = τ / (K·(λ + θ))
    τ_I = τ

λ (desired closed-loop time constant) is the single user-facing knob, exposed as `aggressiveness ∈ {quiet, balanced, responsive}` mapping to λ ∈ {2τ, τ, τ/2}.

### 4.5 Parameter box constraints

Physics-derived, clamped after every RLS update:

- R (thermal resistance): [R_min_physical, R_max_physical] per fan-zone family
- C (thermal capacitance): [C_min, C_max] per CPU die-area class
- K_c, τ_I: bounded from spec-04 `.claude/rules/pi-stability.md` RULE-PI-04
- Feed-forward gains: bounded such that `pwm_ff` alone cannot exceed 80 % of max PWM under any realistic `dP/dt`

## 5. Long-horizon model (Phase 2, v0.9.0)

### 5.1 Exec event collection

Process-launch source (per §3.3). On every `exec` event, log:

    (timestamp, comm, basename(exec_path), uid, cgroup_leaf, argv_hash_optional)

### 5.2 Signature keying

    signature_key = sha256(basename(exec_path) || cgroup_leaf || uid)

Optional argv-template tokenisation (v0.9.x): strip numeric/path arguments, keep flag structure, hash separately.

### 5.3 Per-signature Bayesian statistics

Track per known signature:

    {count, mean_Δpower, var_Δpower, mean_Δtemp, var_Δtemp,
     mean_duration, var_duration, first_seen, last_seen}

Δ values measured over a 60-second window starting 5 s after exec (skip startup overhead). Running mean/variance via Welford's algorithm.

### 5.4 Heavy-workload predicate

    is_heavy = (mean_Δpower > platform_heavy_threshold)
             ∧ (count ≥ 3)
             ∧ (last_seen < 30 days)

Platform heavy threshold comes from spec-03 profile (amendment extends schema — see spec-03 amendment §4).

### 5.5 Pre-warm injection

On recognised heavy exec, inject additive to setpoint-error signal:

    e_eff(t) = e(t) + α · expected_Δpower · exp(-t / expected_duration)

α bounded such that the pre-warm contribution alone cannot drive PWM above 85 % of max. Decay over min(expected_duration, 5 min).

### 5.6 Motif discovery (Phase 3)

Offline STOMP/SCRIMP++ via `matrix-profile-foundation/go-matrixprofile`:

- Nightly goroutine at low priority (`sched_setattr` SCHED_IDLE).
- Input: past 7 days of 1 Hz packed telemetry.
- Output: discovered motifs become "scheduled events" in `motifs.json` with learned trigger times.
- Online loop reads `motifs.json` at each tick; pre-warm fires when within N seconds of a scheduled event.

## 6. Safety envelope (non-overrideable)

Three layers, checked at PWM write time in this order:

### 6.1 Layer A — hard cap (RULE-PREDICT-SAFETY-01)

    if temp > T_crit - 5: pwm = PWM_MAX

Runs in its own goroutine with its own sensor read path; cannot be disabled by any higher-layer component. Separate `.claude/rules/predict-safety.md` binding.

### 6.2 Layer B — conservative fallback curve (RULE-PREDICT-SAFETY-02)

Per-platform-family static temperature→PWM table shipped as read-only data in the binary (spec-03 profile fallback `defaults.curves`). Used when:

- Phase 1 model residual-SD > 3× historical baseline, OR
- Page-Hinkley drift detector trips (RULE-PREDICT-SAFETY-03), OR
- Model parameters NaN/Inf, OR
- No sensor update for > 10 s

Demotion is instant; promotion back to learned model requires shadow-mode re-qualification (§6.5).

### 6.3 Layer C — parameter box clamp (RULE-PREDICT-SAFETY-04)

Every RLS update output clamped to physics-derived bounds (§4.5). Violation counter incremented; 10 consecutive clamp events in a window triggers demotion to Layer B.

### 6.4 Drift detection (RULE-PREDICT-SAFETY-05)

Page-Hinkley CUSUM on prediction residual:

    m_k = max(0, m_{k-1} + (|r_k| - δ))
    if m_k > h: trip

δ and h tuned per-platform-family in profile. Trip → demote to Layer B, start recalibration in shadow.

### 6.5 Shadow-mode promotion (RULE-PREDICT-SAFETY-06)

New learned controller runs in parallel with authoritative controller for ≥ 24 h. Qualification:

- At least one observed idle→sustained-load→idle cycle in the window
- Shadow residual-SD ≤ live residual-SD on a held-out evaluation slice
- Shadow overshoot (per step response) ≤ live overshoot
- No Layer A activations during shadow period

Promotion is explicit, logged, and reversible (24 h automatic rollback window if residuals degrade).

### 6.6 Bumpless transfer

Every controller-layer handoff (A↔B, B↔C) uses the PI `FeedForward` bias to match the outgoing PWM exactly at the transition tick, then lets the integrator catch up. Builds on spec-04 RULE-PI-03 NaN fallback pattern.

## 7. Persistence

Extends spec-03 storage layout (see spec-03 amendment §3).

### 7.1 Files

    /var/lib/ventd/platform/<dmi_fingerprint>/
      model.json              # ARX parameters, RLS covariance, IMC λ
      workloads.json          # signature Bayesian stats
      motifs.json             # scheduled events from nightly STOMP
      telemetry/
        ring-7d.bin           # packed 1 Hz ring buffer, 7 days

All JSON files have `"schema_version"` field; migration contract per spec-03 amendment §2.

### 7.2 Writes

Atomic via `natefinch/atomic` (tempfile + `rename(2)`). Same library spec-03 uses. No ad-hoc writes.

### 7.3 Telemetry ring buffer format

Packed binary: `[timestamp_unix_u64][n_scalars_u16][scalar_u16...scalar_u16]` per frame. 7 days × 1 Hz × ~30 scalars × 8 B = ~145 MB raw, ~20 MB gzipped. Configurable via `/etc/ventd/config.yaml` `telemetry.retention_days` (default 7, min 1, max 30).

### 7.4 Export/import

`ventd model export > node.json` emits current fingerprint + model + workloads + motifs as a single file. `ventd model import node.json` imports as seed for matching fingerprint, or as warm-start (subject to drift re-qualification) for mismatched fingerprint. Homelab clusters sharing identical hardware can provision all nodes from one learned node.

## 8. Observability

### 8.1 Prometheus metrics

    ventd_predict_residual_sd                # current RLS residual SD
    ventd_predict_forgetting_factor          # current λ
    ventd_predict_parameter_vector{param}    # ARX coefficients, one series per
    ventd_predict_feedforward_contribution   # pwm_ff as fraction of total pwm
    ventd_predict_shadow_residual_sd         # shadow controller residual
    ventd_predict_drift_events_total         # Page-Hinkley trip counter
    ventd_predict_fallback_activations_total # demotion to Layer B counter
    ventd_predict_layer_a_activations_total  # hard-cap fires
    ventd_predict_workload_signatures_known  # count of known workloads
    ventd_predict_motifs_discovered          # count of scheduled events
    ventd_predict_time_since_model_update    # staleness watchdog

### 8.2 Debug API

Unix socket endpoint `/var/run/ventd/debug.sock`:

    GET /predict/model          → current model.json
    GET /predict/workloads      → current workloads.json
    GET /predict/motifs         → current motifs.json
    GET /predict/shadow         → shadow controller state + qualification progress
    POST /predict/recalibrate   → force demotion to Layer B + new calibration

### 8.3 Structured logs

INFO: layer transitions, workload recognitions, motif hits, recalibration events.
DEBUG: per-tick residual, feedforward contribution, parameter updates.
TRACE: every RLS step (off by default, dev-only).

## 9. Phased rollout — releases and DoD

### Phase 0 → v0.7.0: feed-forward baseline + safety envelope

**PRs:**
- Layer A hard cap + goroutine isolation.
- Feed-forward on dP/dt, d(util)/dt (no learning, static gains from spec-04 autotune output).
- Shadow-mode framework (infrastructure, not yet running a learned controller).
- Drift-detector scaffolding (metrics only, no demotion yet).
- Prometheus metrics surface.

**DoD:**
- [ ] Feed-forward contribution visible in Prometheus during synthetic power step.
- [ ] Hard-cap fires in property test 100 % of cases when synthetic sensor exceeds T_crit - 5.
- [ ] Shadow framework runs a no-op shadow controller without affecting live control (sanity).
- [ ] RULE-PREDICT-SAFETY-01 bound to subtest.
- [ ] Zero regressions in spec-04 PI suite.

### Phase 1 → v0.8.0: ARX+RLS short-horizon

**PRs:**
- Signal acquisition layer (Sample aggregator, RAPL, PSI, per-core delta).
- ARX + VFF-RLS estimator.
- IMC-PI tuning from identified plant.
- Parameter box constraints.
- Page-Hinkley drift detector wired to Layer B demotion.
- Shadow-mode promotion logic.

**DoD:**
- [ ] Synthetic-plant test: ARX converges to within 5 % of true parameters in < 300 samples.
- [ ] Shadow promotion test: promotion gated exactly by the three §6.5 criteria.
- [ ] Page-Hinkley unit test trips within 30 samples on synthetic drift of specified magnitude.
- [ ] HARDWARE-REQUIRED: phoenix-desktop CPU loop shows measurable temperature overshoot reduction vs. v0.7.0 baseline on a controlled `stress-ng` step.
- [ ] RULE-PREDICT-SAFETY-02..06 all bound to subtests.

### Phase 2 → v0.9.0: exec-signature workload pre-warm

**PRs:**
- Netlink proc-connector source via `vishvananda/netlink`.
- Signature keying + Bayesian stats.
- Heavy-workload predicate + pre-warm injection.
- `ventd model export | import`.

**DoD:**
- [ ] Proc-connector integration test: synthetic exec event propagates to signature store in < 100 ms.
- [ ] Bayesian-stats property test: Welford's algorithm matches batch variance to float64 precision over 10k random streams.
- [ ] Pre-warm injection bounded such that `α·contribution ≤ 0.85·PWM_MAX` for all finite inputs.
- [ ] HARDWARE-REQUIRED: repeated `stress-ng` runs on phoenix-desktop produce recognised signature + observable pre-spin within 5 runs.
- [ ] Export/import round-trip test passes.

### Phase 3 → v0.9.x – v1.0: motif mining + polish

**PRs:**
- Nightly STOMP job via `go-matrixprofile` at SCHED_IDLE.
- Scheduled-event pre-warm.
- Optional eBPF exec tracer via `cilium/ebpf` (opt-in flag).
- Optional argv-template tokenisation.
- Observability polish + docs.
- v1.0 release readiness audit.

**DoD:**
- [ ] Motif-mining correctness test against synthetic periodic workload (daily 03:00 pattern discovered within 7 simulated days).
- [ ] eBPF fast path optional and gated on kernel feature detection.
- [ ] v1.0 release audit: all 4 Phase groups stable in shadow/live for 30 days on phoenix-desktop + miniPC HIL.
- [ ] Docs: end-user "how it works" page, contributor "how to add a new signal source" page.

## 10. Invariants — `.claude/rules/` files

Four new rule files, each 1:1 with subtests (per tools/rulelint):

- `.claude/rules/predict-signal.md` — signal acquisition contract (RULE-PREDICT-SIG-01..N)
- `.claude/rules/predict-model.md` — ARX/RLS correctness (RULE-PREDICT-MODEL-01..N)
- `.claude/rules/predict-safety.md` — safety envelope (RULE-PREDICT-SAFETY-01..06+)
- `.claude/rules/predict-workload.md` — exec signature + motif contracts (RULE-PREDICT-WORKLOAD-01..N)

Exact rule enumeration frozen at Group-A PR 1 after Opus consult 1 (see §12).

## 11. Libraries

### 11.1 Use
- `gonum.org/v1/gonum/mat`, `/stat` — linear algebra, running stats
- `konimarti/kalman` or `rosshemsley/kalman` — Kalman, if §4.1 upgrades to EKF in Phase 3
- `vishvananda/netlink` — proc-connector
- `cilium/ebpf` — optional eBPF exec tracer (Phase 3 opt-in)
- `ebitengine/purego` — NVML dlopen (already used in ventd)
- `mdlayher/go-smbios` — DMI fingerprint (shared with spec-03)
- `natefinch/atomic` — atomic file writes (shared with spec-03, fan2go pattern)
- `matrix-profile-foundation/go-matrixprofile` — offline motif mining (Phase 3)
- `prometheus/client_golang` — metrics (ventd-wide standard)

### 11.2 Reject
- Gorgonia, Zerfoo, Born, any deep-learning framework — wrong scale.
- LSTM/GRU/Transformer predictors — mismatched to thermal plant linearity.
- Full Gaussian Process — O(N²) per step, approximable by RLS covariance.
- `NVIDIA/go-nvml` direct CGO — use purego dlopen (ventd-wide rule).
- Protobuf for v1 state — JSON+schema_version is debuggable; proto can come in v1.x if model grows.

## 12. Opus consults — four, at phase boundaries

### Consult 1 — before Phase 0 PR 1
**Purpose:** Review the safety-envelope three-layer design and the shadow-mode promotion gate. Specifically:
- Is the §6.5 promotion gate rigorous enough, or do we need a closed-loop Nyquist margin check?
- Is Page-Hinkley the right drift detector vs. ADWIN for this signal class?
- Are the four `.claude/rules/predict-*` file names and scopes stable?

### Consult 2 — before Phase 1 PR 1
**Purpose:** Review the VFF-RLS implementation plan and IMC-PI tuning rule.
- Variable forgetting factor adaptation rule (prediction-error energy vs. alternatives)?
- Parameter box constraints — derive physics bounds from first principles per platform family?
- IMC λ mapping to `{quiet, balanced, responsive}` — defensible at 2τ/τ/τ/2 or should we use different ratios?

### Consult 3 — before Phase 2 PR 1
**Purpose:** Review workload-signature design, privacy implications of exec event logging.
- Is sha256(basename||cgroup||uid) sufficient to avoid cross-user collision on multi-user systems?
- Should argv ever be hashed? What's the threat model?
- Pre-warm α bounding — derive from safety envelope or pick empirically?

### Consult 4 — before v1.0 release audit
**Purpose:** End-to-end review. Is the "world's first predictive" claim defensible? Gap analysis vs. CoolerControl, fan2go, iCUE, Armoury Crate at time of release.

## 13. Cost discipline notes

- Group A (v0.7.0) is the cheapest: no model, mostly plumbing. ~5 sessions, $50–75 total.
- Group B (v0.8.0) is the expensive one: ARX+RLS + shadow-mode + property tests. ~10–15 sessions, $120–250.
- Group C (v0.9.0): netlink plumbing + stats. ~5–8 sessions, $60–120.
- Group D (v1.0): motif mining + polish. ~5–8 sessions, $60–120.
- **Total envelope:** $300–600 over 3–6 months at $300/mo budget. Fits.

Per-session Sonnet. Opus consults in chat only, outputs committed to `docs/control-theory-notes.md` alongside spec-04's. No Opus in CC ever.

Property-test scaffolds (`PropARXConvergence`, `PropShadowPromotionGate`, etc.) are pure random-input loops — Haiku if Sonnet context inflates.

## 14. Explicit non-goals (redux — cut scope aggressively)

- **No MPC (explicit or otherwise) in v1.0.** The predictive layer is already the headline; MPC is a v1.x experiment.
- **No per-fan PI autotune during predictive mode.** Autotune runs once (spec-04), predictive rides on top. Re-autotune requires explicit user action.
- **No ML-based calibration curve generation.** Calibration stays deterministic (spec-03).
- **No fleet/cloud aggregation of learned profiles.** Local-only. Profile sharing is user-initiated `export/import`.
- **No UI for tuning model internals.** Only the `aggressiveness` knob is user-visible; everything else is dev-facing via debug socket.

## 15. Open questions — resolve before leaving DRAFT status

- [ ] Does Phase 1 use ARX+RLS or 2nd-order RC + EKF? Decide via Consult 2 benchmark, then strip the losing option from the spec.
- [ ] Is `aggressiveness` the right user-facing knob name? Alternatives: `response`, `mode`, `profile`. Decide at Phase 0.
- [ ] Telemetry ring buffer — raw packed vs. compressed? Decide at Phase 1.
- [ ] eBPF opt-in default: off for v1.0? On for v1.1 once kernel coverage confirmed?
- [ ] How does spec-05 interact with spec-03 profile capture? Do captured profiles include learned model state? (Probably no — captured profiles should be reproducible defaults, not learned state. But needs explicit spec-03-amendment or spec-05 §7 decision.)

## 16. CC session prompt — copy/paste this (for when DRAFT lifts)

```
[DO NOT RUN UNTIL spec-05 Status == Ready]

Read /home/claude/specs/spec-05-predictive-thermal.md. This is a
phased spec organised in four PR groups (A/B/C/D), one group per
minor release. Groups are strictly sequential.

Before any group: confirm the Opus consult for that group has been
run and committed to docs/control-theory-notes.md. Do not start
group N without group N-1 DoD green and tagged.

For all groups, .claude/rules/predict-*.md are authoritative. Every
RULE-<N> line maps to a subtest. The T-META-01 lint will fail the
PR if this mapping breaks.

Use Sonnet throughout. Do NOT call Opus from inside CC. Do NOT
start a parallel subagent session for property-test generation —
use Haiku in the same CC session if needed.

Commit at every green-test boundary. If a property test discovers
a failing case, commit the failing case as a regression test
BEFORE fixing the bug.

[DRAFT NOTE — 2026-04-24] This spec is not ready to run. It
depends on spec-03 (profile library + DMI storage) and spec-04
(PI controller + autotune + FOPDT output) being shipped first.
Current blocker: v0.4.0 spec-02 in flight.
```
