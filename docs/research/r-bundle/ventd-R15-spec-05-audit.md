# R15 — spec-05 Audit Against Locked Smart-Mode R-Items

**Target output:** spec-05 amendment that reconciles the predictive controller (RLS+IMC-PI) with R7, R8, R9, R10, R11, R12, R14, and the v0.5.x patch sequence.

**Audit conclusion up front:** spec-05 as written is **mostly compatible** with locked R-items, but has **seven concrete drift points** that require an amendment before v0.5.9 implementation. None are architectural rewrites; all are scope clarifications, contract pins, or deletion of now-superseded sections.

---

## Audit matrix: spec-05 sections vs locked R-items

| spec-05 § | Locked R-item that touches it | Status | Action |
|---|---|---|---|
| §3 Signal acquisition | R11 (sensor noise floor, admissibility) | **Drift** | Amend §3 to require R11 admissibility check before consuming a sensor |
| §4.1 ARX structure | R9 (identifiability), R10 (shard arch) | **Drift** | Amend §4.1 to align ARX with R9's per-channel `[a, b_·, c]` model dimension; per-zone framing collapses to per-channel |
| §4.2 VFF-RLS | R12 (bounded-covariance forgetting) | **Drift** | Amend §4.2 to specify directional or EFRA forgetting per R12, not pure VFF |
| §4.3 Feed-forward | R8 (tach-less fallback) | **Compatible** | No change; feed-forward is independent of tach availability |
| §4.4 IMC-PI tuning | R12 (confidence formula) | **Drift** | Amend §4.4 to specify how IMC `λ` (closed-loop time constant) interacts with R12's blended `w_pred` weight |
| §4.5 Box constraints | R9 (identifiability gating) | **Compatible** | No change; box constraints orthogonal to identifiability |
| §5 Long-horizon (workload sigs) | R7 (signature hash design) | **Major drift** | Amend §5 to use R7's hashed proc/comm signatures, NOT spec-05's `sha256(basename‖cgroup‖uid)` scheme |
| §6.1 Layer A hard cap | All | **Compatible** | No change; sacred and orthogonal |
| §6.2 Layer B fallback curve | R3 (hardware refusal), R4 (envelope abort) | **Drift** | Amend §6.2 to specify how Layer B interacts with hardware_refusal class and envelope-C abort fallback |
| §6.3 Layer C box clamp | R10 (shard arch) | **Compatible** | No change; box clamp is per-channel, R10 sharding doesn't conflict |
| §6.4 Page-Hinkley drift | R12 (drift response) | **Drift** | Amend §6.4 to specify that drift-detection `m_k` consumes R12's residual feed, not raw sensor residual |
| §6.5 Shadow promotion | R12 (confidence formula), R14 (calib budget) | **Drift** | Amend §6.5 to require R12 confidence ≥ threshold during shadow window AND R14-budget-aware re-qualification |
| §7 Persistence | R10 (shard persistence), spec-16 | **Major drift** | Amend §7 to integrate with spec-16 schema, not introduce parallel `model.json`/`workloads.json` paths |
| §8.1 Prometheus metrics | R12, R8, R10 | **Drift** | Amend §8.1 to add R12 confidence metrics, R8 fallback-tier metrics, R10 shard-state metrics |
| §11 Libraries | R10 (gonum) | **Compatible** | No change; gonum is already in §11.1 |

---

## The seven concrete drift points (with proposed amendment text)

### Drift 1 — §4.1 ARX structure: per-zone → per-channel

**Current:** spec-05 §4.1: "One ARX model per fan-zone (CPU fan zone, case fan zone, GPU fan zone)." Regressor dimension ≤ 12.

**Conflict:** R9/R10 establish per-*channel* RLS (one shard per fan channel, dimension `d_B = 1 + N_coupled + 1`, max 18 with `N_coupled` capped at 16). Per-zone aggregates multiple channels into one model and loses the per-fan coupling matrix `b_ij` that R9 actually identifies. Worse, on Tier L hardware (24 fan channels), per-zone framing collapses to ~3 zones and loses 21 channels' worth of fan-individual coupling information.

**Amendment text:**

> §4.1 (revised). The plant model is per-channel, not per-zone, per R9 §R9.1. For each fan channel `i`:
>
>     T_i[k+1] = a_i·T_i[k] + Σ_j b_ij·pwm_j[k] + c_i·load_i[k] + w_i[k]
>
> Regressor dimension `d_B = 1 + N_coupled + 1` per channel; `N_coupled ≤ 16`. The original "ARX per zone" framing is superseded; "zone" becomes a presentation concept (UI grouping), not a model concept.

### Drift 2 — §4.2 VFF-RLS → bounded-covariance directional forgetting

**Current:** spec-05 §4.2 specifies Paleologu-Benesty-Ciochină 2008 VFF-RLS with covariance trace clamping.

**Conflict:** R12 locks bounded-covariance RLS with **directional forgetting** (Bittanti-Campi 1990/1994 or EFRA), not exponential VFF. Pure VFF can violate information-matrix monotonicity in the unexcited subspace, which R12 explicitly forbids. Trace clamping alone is insufficient — it preserves `tr(P) ≤ 100` but not the directional constraint.

**Amendment text:**

> §4.2 (revised). The online estimator is bounded-covariance RLS with directional forgetting per R12 §R12. Use Bittanti-Campi-Bolzern (1990) directional-forgetting RLS, or equivalently EFRA (Exponential Forgetting and Resetting Algorithm). Pure exponential VFF is rejected because it violates information-matrix monotonicity in unexcited subspaces. Forgetting factor `λ ∈ [0.95, 0.999]`, auto-tuned per shard from prediction-error energy and per R11 sensor-noise SNR. Covariance bound: `tr(P) ≤ 100` per R12; on update violating the bound, rescale `P ← P · (100 / tr(P))` (preserves eigenvectors, attenuates magnitudes).

### Drift 3 — §4.4 IMC-PI ↔ R12 confidence interaction

**Current:** §4.4 specifies IMC-PI tuning rule: `K_c = τ / (K·(λ + θ))`, `τ_I = τ`, with user-facing `aggressiveness` knob mapping to λ ∈ {2τ, τ, τ/2}.

**Conflict:** R12 introduces a blended controller weight `w_pred ∈ [0,1]` derived from confidence formula. The IMC-PI gains compute the *predictive* contribution; the *blended* command is `pwm = w_pred · pwm_predict + (1 − w_pred) · pwm_reactive_curve`. spec-05 doesn't specify this blending — it implicitly assumes the predictive controller is always authoritative once promoted. R12 says it never is; it's always blended with a confidence-weighted reactive baseline.

**Amendment text:**

> §4.4 (extended). The IMC-PI gains compute the predictive PWM command `pwm_predict[k]`. The actual fan-write PWM is the blended command per R12 §R12.4:
>
>     pwm[k] = w_pred[k] · pwm_predict[k] + (1 − w_pred[k]) · pwm_curve[k]
>
> where `w_pred[k]` is R12's confidence-weighted blender output (LPF-smoothed, Lipschitz-capped). Layer A's hard cap (§6.1) operates on the final `pwm[k]` regardless of source. The `aggressiveness` knob continues to control IMC `λ` for the predictive component only; it does not affect `w_pred` directly. R12's confidence formula and the user `aggressiveness` knob are orthogonal axes.

### Drift 4 — §5 workload signatures: replace bespoke scheme with R7

**Current:** §5.2 specifies `signature_key = sha256(basename(exec_path) || cgroup_leaf || uid)`.

**Conflict:** R7 specifies the canonical workload-signature hash design for smart-mode. Maintaining two hashing schemes (one in spec-05 Phase 2, one in R7-aligned Layer C) means Layer C v0.5.8 and the predictive workload pre-warm v0.5.9 would not share a signature space — same workload would generate two different keys, double the per-signature shard memory, halve the convergence speed.

**Amendment text:**

> §5.2 (revised). Workload signatures use the R7 canonical hashing scheme (see `Workload_Signature_Hash_Design_for_ventd_Smart-Mode_Layer_C.md`). spec-05's bespoke `sha256(basename‖cgroup‖uid)` scheme is superseded; the R7 hash function is the single source of truth across Layer C, predictive pre-warm, and any future workload-keyed feature.
>
> §5.3 (revised). Per-signature Bayesian statistics persist in the **same** spec-16 store as Layer C shards (one `signatures.cbor` keyed by R7 hash, two consumer subsystems read different fields). No parallel `workloads.json` file; that file path is removed.

### Drift 5 — §6.4 Page-Hinkley input source

**Current:** §6.4 runs Page-Hinkley CUSUM on "prediction residual `r_k`" without specifying which residual.

**Conflict:** R12 maintains residual-tracking state for confidence computation per channel per shard. Spec-05's drift detector should consume R12's residual stream, not maintain its own. Two parallel residual computations could trip at different times, producing inconsistent state.

**Amendment text:**

> §6.4 (revised). Page-Hinkley CUSUM `m_k` consumes the per-channel residual stream maintained by R12's confidence machinery (see R12 §R12 implementation `internal/confidence/`). Page-Hinkley is a downstream consumer of R12 state; it does not duplicate residual computation. Trip thresholds `δ`, `h` per-platform-family in spec-03 profile, unchanged.

### Drift 6 — §7 persistence: integrate with spec-16

**Current:** §7.1 specifies its own filesystem layout under `/var/lib/ventd/platform/<dmi_fingerprint>/` with `model.json`, `workloads.json`, `motifs.json`, `telemetry/ring-7d.bin`. §7.2 atomic writes via `natefinch/atomic`.

**Conflict:** spec-16 (already shipping as v0.5.0.1, foundation for the entire smart-mode patch sequence) defines the persistent state contract for ventd. Predictive controller state must use spec-16 primitives, not parallel JSON files. Otherwise we ship two disk layouts, two migration paths, two atomic-write strategies. R10 §R10.6 already specifies how RLS shards integrate with spec-16; §7 should defer to that.

**Amendment text:**

> §7 (revised). All persistent state under spec-05 uses spec-16 primitives. The path layout `/var/lib/ventd/platform/<dmi_fingerprint>/` is retained as the spec-16 mount point; sub-paths within it use spec-16's KV/append-log primitives, not ad-hoc JSON files.
>
> - Layer-B shard state: `$STATE_DIR/smart/shard-B/<channel>.cbor` per R10 §R10.6.
> - Layer-C/predictive shard state: `$STATE_DIR/smart/shard-C/<channel>-<sig>.cbor` per R10 §R10.6.
> - Workload-signature stats (predictive Bayesian): `$STATE_DIR/smart/signatures.cbor`, R7-keyed.
> - Motif schedule: `$STATE_DIR/smart/motifs.cbor` (Phase 3, v1.0+).
> - Telemetry ring: `$STATE_DIR/smart/telemetry/ring.binlog` (spec-16 append-only log primitive).
>
> §7.2 atomic writes: deferred to spec-16's atomic-write contract. `natefinch/atomic` remains the underlying library; spec-16 wraps it.
>
> §7.4 export/import: re-implement against spec-16 store; preserve user-facing CLI surface (`ventd model export | import`).

### Drift 7 — §6.5 shadow promotion gate

**Current:** §6.5 lists four shadow-promotion criteria: idle→load→idle cycle, residual-SD comparison, overshoot comparison, no Layer A activations.

**Conflict:** R12 §R12.7 already specifies promotion criteria for predictive components (confidence ≥ threshold, hard-pin satisfied, Lipschitz-bounded ramp-up). spec-05's four criteria are mostly subsumed but not explicitly aligned. R14 (calibration time budget) further constrains promotion: shadow window competes with calibration windows for idle-gate access; R12's hard-pin (5 min after R14 calibration) gates re-qualification.

**Amendment text:**

> §6.5 (revised). Shadow-mode promotion criteria are R12-authoritative. The four spec-05 criteria are retained as **supplemental sufficient conditions** layered on top of R12's confidence threshold:
>
> 1. R12 confidence `conf_C` (Layer C) ≥ 0.40 sustained for ≥ 24 h (R12 promotion threshold), AND
> 2. R12's hard-pin gate satisfied (no R14 calibration in last 5 min, no Envelope D fallback in last 10 min), AND
> 3. ≥ 1 observed idle→sustained-load→idle cycle within the shadow window (spec-05 original), AND
> 4. Shadow residual-SD ≤ live residual-SD on held-out slice (spec-05 original), AND
> 5. Shadow overshoot ≤ live overshoot per step response (spec-05 original), AND
> 6. No Layer A activations during shadow period (spec-05 original).
>
> Demotion: any of the six trips → demote to reactive curve, w_pred → 0 via Lipschitz ramp per R12.

---

## Other cross-cutting changes

### Phase numbering: re-align spec-05 phases to v0.5.x patch sequence

Spec-05 §9 currently maps Phase 0/1/2/3 to v0.7.0/v0.8.0/v0.9.0/v1.0. Post-pivot, the predictive controller graduates to v0.5.9 (single tag). The amendment must fold spec-05 phases into the smart-mode patch sequence:

| Old spec-05 phase | New target | Notes |
|---|---|---|
| Phase 0: feed-forward + safety envelope | v0.5.4 + v0.5.7 (split) | Feed-forward into passive observation logging; safety envelope into Layer B coupling work |
| Phase 1: ARX+RLS short-horizon | **v0.5.7 + v0.5.8** | ARX structure Layer B (v0.5.7), RLS Layer C (v0.5.8); spec-05's Phase 1 is the substance of these patches |
| Phase 1 IMC-PI controller | **v0.5.9** | Confidence-gated controller per R12 — the predictive PWM command consumer |
| Phase 2: exec-signature pre-warm | **post-v0.6.0** | NOT in v0.6.0 smart-mode tag. Defer to post-1.0 work; predictive *workload pre-warm* is additional, not replacement |
| Phase 3: motif mining | **post-v1.0** | Unchanged from spec-05 |

**Amendment text:**

> §9 (revised). spec-05 phasing folds into the smart-mode v0.5.x patch sequence per `spec-smart-mode.md` §11:
>
> - Phase 0 feed-forward → v0.5.4 passive observation foundation; Phase 0 safety envelope (Layer A hard cap) ships in v0.5.7 as the foundation safety primitive shared by Layers B and C.
> - Phase 1 ARX structure → v0.5.7 (Layer B coupling) per R9.
> - Phase 1 RLS estimator → v0.5.8 (Layer C marginal-benefit) per R10.
> - Phase 1 IMC-PI controller → v0.5.9 (confidence-gated controller) per R12.
> - Phase 2 exec-signature pre-warm → post-v0.6.0 (predictive workload pre-warm is an enhancement, not a smart-mode prerequisite).
> - Phase 3 motif mining → post-v1.0, unchanged.
>
> The four-phase Opus-consult schedule (§12) is **rescinded**: design decisions are now made in claude.ai chat against the locked R-bundle. Per-PR cost estimates per `docs/claude/spec-cost-calibration.md` apply.

### Open questions resolution

spec-05 §15 has five open questions. Post-pivot resolutions:

| Q | Resolution |
|---|---|
| ARX+RLS or RC+EKF? | **ARX+RLS**, per R9/R10. EKF stripped from spec. |
| `aggressiveness` knob name? | Defer to spec-12 amendment (smart-mode rework). Current preset name is `Silent / Balanced / Perf` per smart-mode design; `aggressiveness` may map to per-preset IMC λ multiplier internally. |
| Telemetry ring raw vs compressed? | Spec-16 append-log primitive determines this; defer. |
| eBPF opt-in default? | Post-v1.0; not in scope for v0.6.0. |
| Captured profiles include learned model state? | **No.** Profiles (spec-14a/14b) are deterministic seeds. Learned state lives in spec-16 user store, never crossed into shareable profile YAML. |

---

## Amendment scope summary

The full amendment file `spec-05-amendment-smart-mode-rework.md` would contain:

1. Header (status, source, apply-when) — analogous to existing `spec-04-amendment-predictive.md` style
2. Why this amendment exists (smart-mode pivot graduates spec-05 to v0.5.9)
3. The seven drift-point amendments above (§§4.1, 4.2, 4.4, 5, 6.4, 6.5, 7)
4. Phase re-alignment (§9 rewrite)
5. Open-question resolutions (§15 rewrite)
6. Out-of-scope clarifications:
   - MPC remains rejected
   - LSTM/GPR remains rejected
   - eBPF/motif-mining remain post-v1.0
   - Telemetry ring buffer 7-day default unchanged
7. New invariant binding: `RULE-PREDICT-MODEL-R9-COMPAT`, `RULE-PREDICT-MODEL-R12-COMPAT`, `RULE-PREDICT-WORKLOAD-R7-COMPAT` — three new rules in `.claude/rules/predict-*.md` enforcing alignment with the locked R-items
8. Cost: amendment itself is $0 chat work; net effect on v0.5.7/v0.5.8/v0.5.9 implementation is **cost-neutral** (R-items already define the work; amendment removes spec-05 redundancies).

---

## Conclusions actionable now

1. **Draft `spec-05-amendment-smart-mode-rework.md` as a chat artifact before v0.5.7 implementation begins.** This is $0 chat work; produces an amendment file analogous to spec-04-amendment-predictive.md and spec-12-amendment-smart-mode-rework.md.
2. **Three new rule bindings** in `.claude/rules/predict-*.md` will need subtests during v0.5.7/v0.5.8/v0.5.9 implementation. Rulelint will catch missing subtests; CC prompts for those patches must reference the amendment.
3. **No retroactive work needed on v0.5.0.1, v0.5.1, v0.5.2, v0.5.3, v0.5.4, v0.5.5, v0.5.6.** The amendment touches only Layer B, Layer C, controller, and persistence — none of which ship before v0.5.7.
4. **Amendment removes scope from spec-05** more than it adds: deletes parallel persistence layout, deletes bespoke signature scheme, rescinds four Opus consults, defers Phase 2/3 to post-v1.0. Net cost-of-implementation goes down.
5. **One open coordination point with spec-12 amendment:** the `aggressiveness` knob name decision and its mapping to IMC λ. Cleanest resolution: `aggressiveness` is internal-only (debug socket exposed), user-facing knob is the smart-mode preset (`Silent / Balanced / Perf`), preset → λ multiplier table lives in spec-12 amendment §UI-preset-mappings. Confirm during spec-12 amendment finalization.

---

**Audit complete.** R15 produces an amendment file (no new research), $0 chat cost, removes more from spec-05 than it adds, and aligns the predictive controller (v0.5.9) with the locked R-bundle.
