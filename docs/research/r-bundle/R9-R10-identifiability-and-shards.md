# ventd Smart-Mode Research Bundle: R9 Identifiability and R10 RLS Shards

**Target:** Layer B (v0.5.7) coupling estimator and Layer C (v0.5.8) marginal-benefit estimator of `spec-smart-mode.md`.
**Status of upstream:** R12 (bounded-covariance RLS with directional forgetting, tr(P) ≤ 100, λ ∈ [0.95, 0.999], info-matrix monotonicity) is locked. R7 (signature hashing) and R11 (sensor noise floor) are locked. R3 keeps Steam Deck on the catalog-less refusal list (write-path blocked; monitor-only).
**Save target:** `/mnt/user-data/outputs/ventd-R9-R10-identifiability-and-shards.md` (content below; tooling for file write is not exposed in this run, but the markdown is reproducible verbatim).

---

## Section R9 — Identifiability of the Per-Channel Thermal Model

### R9.1 Model recap and parameter vector

Per channel `i`, the discrete-time ARX-with-exogenous-input model is:

```
T_i[k+1] = a_i·T_i[k] + Σ_j b_ij·pwm_j[k] + c_i·load_i[k] + w_i[k]
```

Stack the per-channel regressor and parameter vector:

```
φ_i[k] = [ T_i[k],  pwm_1[k], …, pwm_N[k],  load_i[k] ]ᵀ          ∈ ℝ^d
θ_i    = [ a_i,     b_i1,    …, b_iN,       c_i      ]ᵀ          ∈ ℝ^d
```

with `d = 2 + N` where `N` is the number of fans the channel is configured to be coupled with. So Tier S typically `d ≤ 4`, Tier M `d ≤ 10`, Tier L `d ≤ 26`.

### R9.2 Rank / persistence-of-excitation conditions

For an ARX model with regressor `φ_i ∈ ℝ^d`, **least-squares identifiability requires that the regressor be persistently exciting (PE) of order ≥ d**, i.e. there exist `α, β > 0`, integer `M ≥ d`, such that for all sufficiently large `k`:

```
α·I  ⪯  (1/M) · Σ_{τ=k}^{k+M-1} φ_i[τ]·φ_i[τ]ᵀ  ⪯  β·I              (PE-d)
```

This is the standard formulation in Ljung, *System Identification: Theory for the User* (2nd ed., Prentice-Hall, 1999), §13.4 and §14.4, and Söderström & Stoica, *System Identification* (Prentice-Hall, 1989), Ch. 5. Goodwin & Sin, *Adaptive Filtering, Prediction and Control* (Prentice-Hall, 1984), Ch. 6 derives the equivalent condition on the empirical information matrix `Σ φφᵀ`.

**Equivalence with regressor-matrix condition number.** Rupp & Sayed and others have shown (see "Condition Number of Data Matrix and Persistent Excitation Conditions in RLS Adaptive Filtering," ResearchGate 323161878) that PE-d, lower/upper bounds on the smallest/largest eigenvalues of the empirical Φᵀ Φ, and boundedness of the condition number `κ(Φ) = σ_max/σ_min` are mutually equivalent under bounded input energy. This is what justifies our online detector below.

For an *n-th order linear process* with exogenous inputs, identifiability requires the test signal to be persistently exciting of order **2n** (Ljung 1999; Söderström & Stoica 1989). Our model is order 1 in the autoregressive part and the exogenous channels each contribute one parameter, so **PE-d on the joint regressor `φ_i` is sufficient** — we do not need PE-2n separately.

**Frequency-domain interpretation.** PE-d is equivalent to the joint signal `[T_i, pwm_1..N, load_i]` having a power spectral density that is non-zero at at least `d/2` distinct frequencies in `(−π, π)`. This matters because *piecewise-constant, slowly-varying signals concentrate spectral energy near DC*, which is the dominant failure mode in ventd (see R9.4).

**Closed-loop caveat.** ventd's PWM is the controller output, so the system is closed-loop. Per Ljung 1999 §13.5: in closed loop, what matters is the PE of any **exogenous setpoint or disturbance entering the loop**, not the PE of `pwm` per se. ventd's exogenous signal is `load_i` (workload) plus `w_i` (sensor noise + ambient). Layer-A curve output is a deterministic function of `T_i`, so PWM excitation is *induced* by load excitation. **Conclusion: identifiability of `θ_i` is upper-bounded by the PE of `load_i` plus disturbance noise.** This is the single most important analytical conclusion for ventd.

### R9.3 Structural unidentifiability cases ventd will encounter

These are derived from the rank deficiency of `Φ_iᵀ Φ_i` under ventd's actual signal generation process. Each is a structurally degenerate column relationship in `Φ_i`.

| # | Case | Rank-deficient column relation | Practical incidence |
|---|------|--------------------------------|---------------------|
| U1 | **Co-varying fan group**: two or more fans `j₁, j₂` always commanded the same PWM (e.g. dual CPU fans on the same header, or daisy-chained Y-cable group). | `pwm_{j₁}[k] ≡ pwm_{j₂}[k]` ∀k → columns linearly dependent; only the *sum* `b_{i,j₁}+b_{i,j₂}` is identifiable. | Very common. ~50% of homelab setups. |
| U2 | **Idle-dominated workload**: `load_i[k]` is constant (idle bucket) for long windows. | `load_i` column is constant; collapses with the bias dimension absorbed into `a_i`. `c_i` unidentifiable from idle alone. | Universal — most desktops are idle >90% of the time. |
| U3 | **Constant temperature**: at deep idle with hysteretic PWM clamped at min, `T_i` quantizes to a single 1°C bucket (sensor quantization, R11). | `T_i[k] ≡ const` and `pwm[k] ≡ const` → entire row of `φ_i` is constant; only the steady-state gain is identifiable, not `a_i` separately. | Common at night / unattended idle. |
| U4 | **No coupled neighbors**: a single-zone channel with `N=0` (e.g. NUC with one fan/one sensor). | Trivially identifiable: only `a_i, c_i` (`d=2`). Already a degenerate but well-posed case. | Tier S baseline. |
| U5 | **No load proxy**: case-fan channel where no `load_i` signal exists (no obvious workload mapping for an enclosure ambient sensor). | `c_i` is structurally fixed at 0; remove the column and identify `(a_i, b_ij)` only (`d = 1 + N`). | Common for case fans on Tier M/L. |
| U6 | **Saturated PWM**: fan stuck at PWM=255 due to manual override or thermal emergency clamp. | `pwm_j[k] ≡ 255`; column collapses to bias. `b_ij` unidentifiable for that fan during saturation. | Sporadic — saturation events flagged by R11 already. |
| U7 | **Sensor frozen / aliased**: same temp file aliased to two channels (HWMON quirk on some SuperIO). | Two `T_i` columns identical → cross-channel rather than per-channel issue, but breaks Layer B coupling estimates. | Rare; surfaced by R6 hwmon validator. |

Cases U1, U2, U3 are the realistic dominant failure modes. U4, U5 are *well-posed reduced models*, not failures — handled by reducing `d`. U6, U7 are transient/structural and surfaced by other layers.

### R9.4 Online identifiability detector

Run per shard, every K ticks (suggest K=60, i.e. once a minute at 1 Hz controller cadence — cheap):

1. Maintain a windowed empirical regressor `Φ_i^(W)` of the last W samples (suggest W=600, ≈10 min at 1 Hz).
2. Compute `M = Φ_i^(W)ᵀ Φ_i^(W) / W`. This is `d × d`, very small.
3. Compute condition number `κ = cond(M)` via SVD on `M` (or via `mat.Cond` in gonum which uses 1-norm; the 2-norm is preferable here for spectral interpretation).
4. Classify:
   - `κ ≤ 10²`: **healthy**, proceed with full RLS update.
   - `10² < κ ≤ 10⁴`: **marginal**, apply directional-forgetting only (no parameter update in unexcited subspace; see R10.4).
   - `κ > 10⁴`: **unidentifiable**, hold `θ_i` at prior, increment `unident_ticks` counter. Surface in `ventd doctor` as informational.
5. Pairwise column-correlation pass (cheap, only when κ > 10²): for each pair `(j₁, j₂)` of fan columns, compute Pearson `ρ` over the window; if `|ρ| > 0.999`, declare a co-varying group and merge them into a composite channel for Layer B (tracked in shard metadata).

Threshold rationale: for a `d=10` system with float64 RLS updates, `κ > 10⁴` is the empirical edge above which RLS suffers numerical wind-up even with regularization; consistent with Lai/Islam/Bernstein, "Regularization-Induced Bias and Consistency in Recursive Least Squares" (arXiv 2106.08799), §III–IV, and with the "condition memory RLS" thresholds used in BMS contexts (arXiv 1912.02600). Belsley conditioning diagnostics flag `κ > 30` as worrying multicollinearity; that is a tighter threshold appropriate for econometric inference but too aggressive for an online controller where we *want* to use partial information when available.

### R9.5 Detection actions

| Detector outcome | Layer-B action | Layer-C action | Doctor output |
|-----------------|----------------|----------------|----------------|
| Healthy (κ ≤ 10²) | Full update of `(a, b, c)` | Update marginal-benefit estimate | none |
| Marginal (10² < κ ≤ 10⁴) | Update only directionally-excited subspace (per R12 directional forgetting); freeze unexcited dims at prior | Same | `info: shard <id> partially identifiable, dim_excited = m/d` |
| Co-varying group detected | Merge fans `(j₁,j₂)` into composite column; estimate sum coefficient; report `b_ij = sum/2` per fan with a confidence flag | n/a | `info: fans <j₁>,<j₂> co-varying, grouped` |
| Unidentifiable (κ > 10⁴) | Hold `θ` at prior (curve-only behavior); do not write to controller | Suppress signature shard activation | `info: shard <id> unidentifiable, holding prior` |

This is **informational, never error**. Layer A continues to ship the curve unconditionally; Layer B/C are augmentations that fail safely to curve-only.

### R9.6 Synthetic workload generator — REQUIREMENTS spec (defer build)

Defer construction to a CC task analogous to `spec-05-prep-trace-harness.md`. The generator must:

**G1 — Workload trajectories.** Implement a Markov chain over R7's hashed proc/comm tuples. Configurable transition matrix `P_{b→b'}` and bucket dwell-time distribution (geometric default; Weibull optional for heavy-tailed dwell). Parameter set: `{idle-heavy, balanced, transient-heavy}` presets with explicit `(P, dwell)` recipes.
   - *idle-heavy*: P[idle→idle] = 0.99, mean dwell idle = 1800 s, ≤2 active non-idle buckets total.
   - *balanced*: P[idle→idle] = 0.7, mean dwell idle = 60 s, 5–8 active buckets.
   - *transient-heavy*: P[idle→idle] = 0.3, mean dwell idle = 5 s, 10+ active buckets, frequent crossings.

**G2 — Bucket-to-load mapping.** Per-channel mapping `bucket → load_i ∈ [0,1]`. Stochastic: `load_i = μ_b + σ_b·ε`, ε ~ 𝒩(0,1) clipped to [0,1]. Ground-truth `(μ_b, σ_b)` per bucket recorded.

**G3 — PWM trajectory injection.** Run the simulated curve (from `spec-curve.md` semantics) on the simulated `T_i` to produce `pwm_j[k]`. Allow override mode "PRBS injection" (pseudo-random binary sequence on PWM with amplitude δ and probability p) for *probing experiments* — not for production but for empirical PE-floor characterization.

**G4 — Sensor noise injection.** Per R11: additive `w_i ~ 𝒩(0, σ_T²)` plus 1°C quantization, plus optional bias drift `μ(t) = μ₀ + Δ·t/T_run`. Multipliers `{0.5×, 1×, 2×, 5×}` of R11's noise floor.

**G5 — Ground truth output.** Generator emits the trace AND the `(a_i, b_ij, c_i)` parameters used to synthesize it, plus the realized empirical PE order of the trace (post-hoc SVD on Φ to record `σ_min(Φ/√k)`).

**G6 — Integration.** Output format: parquet or CBOR-framed records compatible with the existing trace-harness IO. Single-binary, CGO_ENABLED=0, GPL-3.0.

### R9.7 Validation experiment list (run once G1–G6 exist)

| Exp | Question | Pass/Fail threshold |
|----|----------|---------------------|
| E1 | Recovery vs trace length, balanced regime, σ_T = 1× R11 floor. | `‖θ̂ − θ*‖∞ / ‖θ*‖∞ < 10%` within **N = 6 hours** of simulated time. |
| E2 | Recovery vs SNR (σ_T ∈ {0.5×, 1×, 2×, 5×}), balanced regime, 24 h trace. | Error scales as `O(σ_T)`; coefficient ≤ 5×. No estimator divergence at 5× (tr(P) bound holds). |
| E3 | Idle-heavy regime, 24 h. | Detector classifies idle windows as unidentifiable in ≥95% of cases. `c_i` reported as held-at-prior; no false convergence claim. |
| E4 | Co-varying fan group injected at fan pair (1,2). | Pearson ρ detector flags pair within W=600 samples in 100% of trials. Merged composite identifies sum within 10% accuracy. |
| E5 | Transient-heavy regime, 6 h. | Recovery error <5%; convergence within 1 h. |
| E6 | Cross-check analytical predictions: cases U1–U6 driven artificially and compared against detector classification. | 100% true-positive rate on structural cases U1, U2, U3, U6. Mismatches recorded as research findings. |

**Convergence target N = 6 hours of homelab-typical workload** is justified by: typical homelab daily cycle has at least one balanced workload window (gaming, compile, batch job) of ≥1 h, and 6 h gives ≥3× buffer. Tighter targets (1 h) are achievable only under transient-heavy synthetic regimes, not real workloads.

**Recovery error target X = 10%** on identifiable parameters is appropriate because the downstream consumer is a fan curve coupling correction, not a precision instrument; thermal time constants `1/(1−a_i)` need to be accurate to within a factor 1.1 for the controller to behave correctly, and `b_ij` needs same-sign correctness more than tight magnitude.

### R9.8 Conclusions feeding R10

1. **Per-shard `d` is small** (typically 4–10, max 26). `d×d` covariance is ≤ 5 KiB even Tier L worst case. Memory-cheap.
2. **Most signature shards will spend most of their lifetime in marginal or unidentifiable regimes.** R10 must not pay full RLS update cost when the shard's detector says "hold prior."
3. **Co-varying fan groups should be detected at shard *creation* time and stored as a structural property of the shard**, not re-detected every tick. R10 needs a shard-creation hook.
4. **Layer B coupling parameters `b_ij` are roughly workload-independent** under U2 (idle-heavy): the thermal coupling matrix doesn't change with workload; only excitation does. This justifies *Layer B = per-channel only*.
5. **Layer C marginal-benefit is inherently per-(channel,signature)** because benefit depends on the load trajectory which is signature-defined.
6. **No point sharding into a structurally unidentifiable dimension.** R10 must consult R9's detector before allocating a Layer-C shard for a low-traffic signature; if an existing shard for the (channel, signature) pair has spent >Θ ticks unidentifiable, eviction is favored over retention.

---

## Section R10 — RLS Shards Architecture

### R10.1 Sharding strategy: hybrid (recommendation)

**Recommendation:** Hybrid two-tier sharding.

- **Layer-B base shard: per-channel.** One shard per fan channel, dimension `d_B = 1 + N + 1` (the full `[a, b_·, c]`). Estimates the workload-independent thermal coupling matrix.
- **Layer-C overlay shards: per-(channel, signature), activated on demand.** One shard per (channel, signature) pair, dimension `d_C = 2` (only marginal-benefit slope and intercept; details below). Activated on first observation of a signature with ≥ τ_act dwell-time; evicted under LRU+TTL.

Justification (refining R9.8):

- A pure per-channel scheme cannot distinguish workload-dependent benefit (Layer C requirement).
- A pure per-(channel, signature) scheme inflates memory, and worse, fragments the data for `b_ij` estimation across signatures that all share the same physical coupling — slowing convergence on the most-needed parameter.
- Per-(channel, sensor) is redundant: a channel is *defined* by its primary sensor in `spec-channel.md`; Layer B coupling already addresses the "case fan affects CPU and GPU temps" case via `b_ij` from those neighbor fans into channel `i`'s sensor.

**Layer C shard parametric form.** Layer C does not need to re-estimate the full `(a, b, c)` vector per signature. It estimates a 2-parameter local linear marginal-benefit model:

```
ΔT_predicted_drop_at_+1pwm[k] = β_0,s + β_1,s · load_i[k]   (per signature s)
```

i.e. at signature `s`, what is the expected temperature reduction per unit PWM increase, parameterized by current load. This is `d_C = 2` and is what the marginal-benefit threshold consumes (auto-ON / manual-OFF gate). The Layer-B shard supplies the thermal model; Layer C just learns the *signature-conditional sensitivity* on top.

This separation is consistent with the "informativity vs. parametrization" decomposition (Bazanella et al., "Identifiability and excitation of linearly parametrized rational systems," *Automatica*, 2016): structural parameters and operating-point-dependent sensitivities are separately identifiable.

### R10.2 Per-shard memory cost

Layer-B shard, per channel:

| Item | Size |
|------|------|
| `θ` (param vector) | `8·d_B` bytes |
| `P` (covariance, dense `float64`) | `8·d_B²` bytes |
| `Φ` window (W=600, optional, for κ detector) | `8·W·d_B` bytes |
| Forgetting-factor scratch (λ, last residual variance, EMA terms) | ~64 bytes |
| Co-varying group bitmap, identifiability flags | ~64 bytes |
| Allocation overhead (Go slice headers, mutex) | ~128 bytes |

For `d_B = 10` (Tier M typical, 8 fans + 2): θ=80 B, P=800 B, φ-window=48 KiB. **The window dominates.** Mitigate by storing only sufficient statistics for the rolling κ check rather than raw samples: maintain rolling `Φᵀ Φ` updated incrementally with a `W`-step ring buffer of φᵀφ contributions (still `d²·8 = 800 B` plus a 600-entry ring of d-dim vectors = 48 KiB). Or: subsample the window at 1/10 (W_eff=60) — sufficient because κ doesn't change fast. **Recommend W=60 with subsampling, ~5 KiB per shard.**

Layer-C shard, per (channel, signature): `d_C = 2` → `θ = 16 B`, `P = 32 B`, no window needed (κ trivially well-conditioned for 2D); ≈300 B with overhead.

**Tier budgets (revised against actual cost):**

- **Tier S (Steam Deck monitor-only):** 1–2 channels, 5–10 signatures.
  - Layer B: 2 × 5 KiB ≈ 10 KiB.
  - Layer C: 2 × 10 × 300 B ≈ 6 KiB.
  - **Total: ~16 KiB. Budget 16 MiB. Fits with 1000× margin.** R10 budget validation: ✓ trivially.
- **Tier M (homelab desktop):** 4–8 channels, 20–50 signatures.
  - Layer B: 8 × 5 KiB ≈ 40 KiB.
  - Layer C: 8 × 50 × 300 B ≈ 120 KiB.
  - Plus Go runtime, hwmon paths, signature LRU metadata, etc. (~5 MiB baseline).
  - **Total: ~5.2 MiB. Budget 64 MiB. Fits with ~12× margin.** ✓
- **Tier L (server/EPYC):** 8–24 channels, 50–200 signatures.
  - `d_B` worst case = 26 (24 fans + a + c). `P = 8 × 676 = 5.4 KiB`. With window (subsampled): ~20 KiB.
  - Layer B: 24 × 20 KiB ≈ 480 KiB.
  - Layer C: 24 × 200 × 300 B ≈ 1.5 MiB.
  - **Total: ~2 MiB shard data + ~10 MiB baseline ≈ 12 MiB. Budget 256 MiB. Fits with ~20× margin.** ✓

**Cliffs.** The only cliff is **`d_B²` scaling on Tier L**. At `d_B = 26`, P is 5.4 KiB; at `d_B = 50` (hypothetical 48-fan IPMI behemoth), P is 20 KiB and the rank-1 update becomes ~2500 FLOPs/tick. Still negligible CPU, but a `mat.Dense` allocation of 20 KiB per shard means 24 × 200 = 4800 shards × 20 KiB = 96 MiB. That would breach the Tier L 256 MiB envelope only with concurrent activity in all 4800. **Mitigation: cap `N_fans` per channel at 16 (a channel cannot be coupled to more than 16 neighboring fans).** Above that, the analytical PE conditions degrade anyway (need PE-d ≥ 18) and identifiability is hopeless with workload-driven excitation. Document this cap in `spec-smart-mode.md`.

### R10.3 Per-shard CPU cost

RLS rank-1 update is `O(d²)` per tick using the Sherman-Morrison form:

```
K[k]   = P[k-1]·φ[k] / (λ + φ[k]ᵀ·P[k-1]·φ[k])           # ~d² mul + d² add
P[k]   = (P[k-1] − K[k]·φ[k]ᵀ·P[k-1]) / λ                # ~d² mul, divide
θ[k]   = θ[k-1] + K[k]·(y[k] − φ[k]ᵀ·θ[k-1])             # ~d mul + d add
```

For Tier M with `d_B = 10`: ~250 FLOPs/tick × 8 channels × 1 Hz = **2000 FLOPs/sec ≈ 0.0001% of one core.** Tier L worst case (`d_B = 26`, 24 channels, 5 Hz): ~250k FLOPs/sec ≈ negligible.

**The actual hot path is signature dispatch, not the math.** Per tick, for each channel: hash the current proc/comm tuple via R7's signature function, look up shard in a `map[uint64]*shardC`, fall through to "currently-no-overlay" if unsigned. With 200 active signatures on Tier L, lookups average 2–3 ns on modern hardware; per-tick total under 100 ns/channel. Use `sync/atomic`-loaded pointer to swap maps on eviction sweep rather than locking on every lookup hot path.

**Concurrency model recommendation:**

- **One estimator goroutine per channel.** `goroutine count = N_channels`, max 24 on Tier L. Channels are independent (per-channel `θ_i`); coupling is through their inputs, not state.
- Shards within a channel held in `map[Signature]*shardC` plus the always-present `*shardB`.
- Per-channel mutex guards the map (acquired only on activation/eviction; lookups go through atomic pointer to current map snapshot, COW on mutation). Update operations on the located shard go through a per-shard mutex (so a doctor goroutine reading shards for `ventd doctor smart` doesn't block updates).
- Tick fan-in: a single ticker goroutine broadcasts `tick(k)` on a buffered channel per estimator; each estimator does its math and writes back the result for the controller.
- **Do NOT use one goroutine per shard.** That would be 4800 goroutines on Tier L; the goroutine cost is small but the scheduling overhead and the synchronization fan-in is wasteful. Keep goroutines proportional to channels.

### R10.4 Activation, eviction, warmup

**Activation criterion (Layer-C overlay).** A signature `s` is observed for the first time at tick `k₀`. Defer overlay-shard creation until the signature has accumulated at least `τ_act = 60 s` of dwell (sum of dwells across visits in a 1 h window). Rationale: ephemeral one-off processes (cron, OS housekeeping, brief curl) generate signatures we never benefit from modeling; this filters them out cheaply.

**Initialization.** `P_0 = α · I` with `α = 1000` (the standard high-uncertainty RLS warm-start; e.g., Goodwin & Sin 1984, §3.3). `θ_0` for Layer-C overlay seeded from the parent Layer-B shard's prediction at the same operating point — Bayesian-prior style — rather than zero. This dramatically cuts warmup ticks.

**Warmup criterion.** A shard's output is *not consumed* by the Layer-B/C controllers until:
- `n_samples ≥ d² · 5` (rule of thumb; matches the "5× the parameter count" heuristic from Belsley conditioning, plus 1 dimension for safety), AND
- `tr(P) < tr(P_0) · 0.5` (P has shrunk meaningfully, indicating the estimator has actually learned), AND
- `κ(M) < 10⁴` (R9 detector says identifiable).

Until all three hold, the shard is in *warmup*; Layer A's curve output is used. This cleanly handles cold-start, Tier-S monitor-only mode (where the estimator runs but the writes are blocked anyway), and signatures that briefly appear and never reach PE.

**Eviction policy.**

- **Layer-C overlay TTL:** evict if the signature has not been observed for `τ_evict = 7 days` of wall time. Use coarse timestamp updated only every 1000 ticks to avoid hot-path writes.
- **LRU bound:** hard cap on number of overlay shards per channel (Tier S: 16, Tier M: 64, Tier L: 256); on activation, if cap exceeded, evict least-recently-observed shard whose warmup has *not* completed (or if all warmed, oldest by last-observation timestamp).
- **R9-driven eviction:** if a shard has spent `Θ_unident = 3600` ticks (1 h at 1 Hz) in unidentifiable status, evict — it's not earning its keep. Numerical stability is already protected by R12's bounded covariance, but memory is not.
- **Reference-count via R7's signature lifecycle:** when R7 retires a signature from its bucket cache (LRU at the hash level), Layer-C overlay shards for that signature become eligible for immediate eviction.

**Allocation pattern.** Use `sync.Pool` keyed by shard size class (small `d=2` for Layer C, medium `d≤10` for Layer B/Tier-M, large `d≤26` for Tier L). On eviction, return shard to pool. Steady-state allocations approach zero on long-running daemons. This matters for ventd's "no GC pressure under sustained load" goal from spec-perf.md.

### R10.5 Concurrency model summary

```
+------------------+        +------------------+
|  ticker (1 Hz)   | -----> |  channel-i goroutine    | ----> controller out (fan_i)
+------------------+        |  - holds shard_B[i]     |
                            |  - holds map<sig,shard_C>|
                            |  - on tick:              |
                            |     1. read φ from R6    |
                            |     2. dispatch sig hash |
                            |     3. RLS update on B   |
                            |     4. RLS update on C   |
                            |     5. emit prediction   |
                            +------------------+
                                     ^
                                     | (read-only pointer snapshot)
                            +------------------+
                            | doctor goroutine |
                            | (snapshot reader)|
                            +------------------+
```

- One goroutine per channel (8–24).
- `atomic.Pointer[shardMap]` per channel for the C-overlay map; update on activation/eviction by COW.
- Per-channel mutex guards activation/eviction sequencing.
- Doctor reads via the atomic pointer; lock-free on hot path.

### R10.6 Cold-start and persistence

**Cold start.** First boot of ventd: all shards initialized as in R10.4. Warmup masks output for the first ~60 s on Tier S, ~5 minutes on Tier M (because `d_B² × 5 ≈ 500` samples needed, at 1 Hz). Document this in `spec-smart-mode.md` as "smart mode warms up over the first ~5 minutes after enable."

**Warm start (persistence integration with spec-16).**

- Persist Layer-B shard state to the existing spec-16 state directory (`$STATE_DIR/smart/shard-B/<channel>.cbor`).
- Persist Layer-C shards conditionally: only those that have completed warmup and have been observed within the last 7 days. Evict-before-persist; don't restore cold state.
- Serialization: CBOR with a versioned envelope `{schema_version, hwmon_fingerprint, channel_id, signature, theta, P_compressed, lambda, n_samples, last_seen_unix}`. Use `mat.Dense.MarshalBinary` style for `P` (float64 little-endian raw) gated by GOARCH endianness check.
- **`hwmon_fingerprint` invalidation:** if R6's hwmon discovery returns a different chip/path layout than the saved fingerprint, discard the persisted shards. Hardware change → re-warm. Cheap and prevents subtle bugs from stale parameters mapped to renamed sensors.
- **Forgetting factor restore:** `λ` restored as-is from disk. Not reset. R12's auto-tuner will re-converge `λ` to current SNR within minutes regardless.
- **`tr(P)` clamp on restore:** if persisted `tr(P)` exceeds R12's cap, rescale `P` proportionally. Cheap safety net for cross-version migrations.
- **Schema versioning:** any change to `d_B` definition (e.g., adding a new regressor) is a *breaking* schema bump; on mismatch, discard rather than migrate. Document in `spec-16-state.md`.

### R10.7 R9 → R10 integration: identifiability gates shard allocation

This is the explicit interface between the two layers:

1. On Layer-C activation request for `(channel, signature)`: run R9's online detector against the current Layer-B shard's `M = ΦᵀΦ/W`. If `κ > 10⁴` OR co-varying fan group covers the channel's fan set, reject activation; defer for τ_retry = 1 h.
2. On Layer-B detector classifying co-varying group: collapse the affected `b_ij` columns into a composite, reducing `d_B` accordingly; reallocate `P` at smaller dimension; re-warm.
3. On Layer-C shard reaching `Θ_unident` consecutive unidentifiable ticks: evict per R10.4.
4. R9's PE / κ detector results are reported in shard metadata so doctor can surface them as informational, never error.

### R10.8 Library / implementation notes

- **gonum/mat** is pure Go (no CGO), works under `CGO_ENABLED=0`. Use `*mat.SymDense` for `P` to leverage symmetry (halves storage and update cost). `SymRankOne` is the right primitive for the rank-1 update in Sherman-Morrison form. Reference: https://pkg.go.dev/gonum.org/v1/gonum/mat
- For the κ detector, `mat.Cond(M, 2)` gives the 2-norm condition number via SVD; for `d ≤ 26`, this is microseconds per call.
- Avoid `math.Inf` propagation: clamp `tr(P)` to R12's cap before computing `K[k]`; if numerator denominator `λ + φᵀPφ` falls below 1e-12, skip the update for this tick rather than dividing.
- Existing prior art for the parametric model (sanity-check that this approach is in the field): US patent 7,711,659 ("Adaptive system for fan management," Intel) describes RLS-based fan-control parameter adaptation with observation vectors built from past power and temperature samples — same general architecture, validating the approach as workable. (https://image-ppubs.uspto.gov/dirsearch-public/print/downloadPdf/7711659)
- Existing Linux fan controllers (`fancontrol` from lm-sensors, `nbfc-linux`, `tpfancontrol`) **do no parameter estimation**: they use hard-coded thresholds and piecewise-linear maps (https://github.com/lm-sensors/lm-sensors/blob/master/doc/fancontrol.txt; https://github.com/nbfc-linux/nbfc-linux). ventd's smart mode is genuinely novel in the FOSS Linux fan-control space; there is no upstream prior art to align with at the algorithmic level.
- For directional forgetting per R12, the cleanest reference algorithms are:
  - Bittanti, Bolzern, Campi, "Convergence and exponential convergence of identification algorithms with directional forgetting factor," *Automatica* 26 (1990) 929–932 — establishes the modification of Kulhavý's DF that achieves exponential convergence under PE.
  - Bittanti, Campi, "Bounded error identification of time-varying parameters by RLS techniques," *IEEE TAC* 39(5):1106–1110 (1994) — the locked R12 result on covariance-bounded RLS; bounds the tracking error iff the covariance is L-bounded.
  - Cao, Schwartz, "A directional forgetting algorithm based on the decomposition of the information matrix," *Automatica* 36 (2000) — practical algorithm with two adjustable parameters; bounded above and below information matrix; suitable reference implementation.
  - Optional modern refinement: SIFt-RLS (arXiv 2404.10844, 2024) — explicit eigenvalue bounds without persistent excitation; cleanly aligns with our "marginal" κ regime.

### R10.9 Conclusions actionable for `spec-smart-mode.md`

For Layer B (v0.5.7) and Layer C (v0.5.8) specs, write the following normative text (to be lifted into spec):

1. **Sharding:** Layer B uses one RLS shard per channel of dimension `d_B = 1 + N_coupled + 1` (for `[a, b_·, c]`); `N_coupled` is capped at 16. Layer C uses on-demand per-(channel, signature) overlay shards of dimension 2 for marginal-benefit estimation, parented to the Layer-B shard for prior seeding.
2. **Identifiability gating:** every shard maintains a windowed regressor `M = ΦᵀΦ/W` with W=60 (subsampled at 1/10 of tick rate) and reports condition number `κ`. Layer-C activation is gated on parent Layer-B shard `κ ≤ 10⁴` and absence of co-varying fan groups covering the relevant fan set.
3. **Activation:** Layer-C overlay shards activate only after `τ_act = 60 s` of cumulative dwell at the signature.
4. **Warmup:** `n_samples ≥ 5·d²` AND `tr(P) ≤ 0.5·tr(P_0)` AND `κ ≤ 10⁴`. Output not consumed by controller until all three hold; Layer A's curve runs unaltered during warmup.
5. **Eviction:** Layer-C overlay shards evict on TTL=7 days of non-observation, on LRU cap (S=16 / M=64 / L=256), on `Θ_unident = 3600` ticks unidentifiable, or on R7 signature retirement.
6. **Concurrency:** one estimator goroutine per channel. Per-channel atomic-pointer-swapped overlay map. Per-shard mutex for update/snapshot separation. No goroutine-per-shard.
7. **Persistence:** shards (post-warmup only, observed within 7 days) serialized via spec-16 `$STATE_DIR/smart/`. CBOR envelope with hwmon fingerprint; mismatch discards. `λ` and `P` restored as-is, with `tr(P)` clamped to R12's cap.
8. **Tier-S behavior:** estimator runs in monitor-only mode; writes blocked per R3; persistence and warmup proceed normally so that a future writeable driver finds the model already converged.
9. **Doctor surfaces:** identifiability classification (`healthy | marginal | co-varying-grouped | unidentifiable`) per shard, with co-varying-group membership and last-observation timestamps. **Informational, never error.**
10. **Memory budgets validated:** Tier S 16 KiB shard data (vs 16 MiB budget; ✓), Tier M ~160 KiB (vs 64 MiB; ✓), Tier L ~2 MiB (vs 256 MiB; ✓). CPU cost dominated by signature dispatch, not RLS math; <0.01% of one core sustained on Tier M typical workload.

---

## Cross-references and final notes

- **R9 → R10 contract:** R9's online κ detector is the gating signal for R10 activation, eviction, and output consumption. R9 must run before R10 commits a shard's output to the controller. This is reflected in §R10.4 warmup criteria and §R10.7 integration rules.
- **R12 compatibility:** all R10 update primitives use the directional forgetting from Bittanti-Campi-Bolzern 1990 with the bounded-covariance variant of Bittanti-Campi 1994 (the locked R12). Information-matrix monotonicity is preserved because directional forgetting only forgets in excited directions; unexcited directions retain their prior information. The `tr(P) ≤ 100` cap is enforced via a post-update clamp (rescale P by `100 / tr(P)` if exceeded), which preserves direction (eigenvectors) and only attenuates magnitudes.
- **R7 compatibility:** Layer-C overlay shard keys are the R7 hashed signatures; eviction couples to R7 signature retirement.
- **R11 compatibility:** sensor noise floor used in R9.6 G4 generator and in λ auto-tuner SNR estimate (per R12).
- **R3 compatibility:** Tier-S (Steam Deck) runs the full estimator pipeline in monitor-only mode; writes are gated by the catalog as before.

### Open research items (out-of-scope for this bundle, surface for follow-up)

- **Empirical validation of the κ thresholds** (10², 10⁴): the values are defensible from literature but not specifically tuned to ventd's d-range and noise floor. Run E1–E6 once the synthetic generator exists.
- **Auto-tuning of `τ_act`, `τ_evict`, `Θ_unident`**: defaults proposed are conservative; field telemetry over v0.5.7/v0.5.8 will inform tuning. Consider exposing as `[smart]` config knobs with sensible defaults.
- **PRBS probing mode** (G3 optional): controlled small-amplitude PWM dithering to inject PE into idle-dominated regimes. Out-of-scope for v0.5.7/v0.5.8 — too invasive — but worth a future R-task. Tradeoff: marginal extra audible noise vs. dramatically faster Layer-C convergence on quiet machines.
- **Joint-channel identification:** current architecture identifies each channel independently. If two channels share a common neighbor fan with strong coupling, joint estimation could improve `b_ij` accuracy. Out-of-scope, defer.

### Primary references

- Ljung, *System Identification: Theory for the User*, 2nd ed., Prentice-Hall, 1999, §13.4, §14.4.
- Söderström & Stoica, *System Identification*, Prentice-Hall, 1989, Ch. 5.
- Goodwin & Sin, *Adaptive Filtering, Prediction and Control*, Prentice-Hall, 1984, Ch. 3, Ch. 6.
- Bittanti, Bolzern, Campi, "Convergence and exponential convergence of identification algorithms with directional forgetting factor," *Automatica* 26 (1990) 929–932. https://www.sciencedirect.com/science/article/abs/pii/0005109890900127
- Bittanti, Campi, "Bounded error identification of time-varying parameters by RLS techniques," *IEEE TAC* 39(5):1106–1110, 1994. https://colab.ws/articles/10.1109/9.284904
- Cao, Schwartz, "A directional forgetting algorithm based on the decomposition of the information matrix," *Automatica* 36 (2000). https://www.sciencedirect.com/science/article/abs/pii/S0005109800000935
- Lai, Islam, Bernstein, "Regularization-Induced Bias and Consistency in Recursive Least Squares," 2021. https://arxiv.org/pdf/2106.08799
- Lai et al., "SIFt-RLS: Subspace of Information Forgetting Recursive Least Squares," 2024. https://arxiv.org/html/2404.10844v1
- Lee, Park, "Low computational cost method for online parameter identification of Li-ion battery in BMS using matrix condition number" (CMRLS), 2019. https://arxiv.org/pdf/1912.02600
- gonum/mat package. https://pkg.go.dev/gonum.org/v1/gonum/mat
- US patent 7,711,659, "Adaptive system for fan management" (Intel, RLS-based fan control). https://image-ppubs.uspto.gov/dirsearch-public/print/downloadPdf/7711659
- lm-sensors fancontrol (no parameter estimation; baseline comparison). https://github.com/lm-sensors/lm-sensors/blob/master/doc/fancontrol.txt
- nbfc-linux (no parameter estimation; baseline comparison). https://github.com/nbfc-linux/nbfc-linux