# R12 Amendment — Threshold Recalibration Against R9/R10 Warmup Criteria

**Status:** AMENDMENT to R12 (`ventd_Smart-Mode_Research__Tach-less_Fallback_Signals_and_Blended_Controller_Confidence_Formula__R8_and_R12_.md`).
**Drafted:** 2026-04-29.
**Source:** broader sweep of R12 against the full locked R-bundle (R1–R15) with focus on R9/R10 alignment.
**Apply when:** v0.5.7 (Layer B coupling) and v0.5.8 (Layer C marginal-benefit) implementation specs are drafted; both must reference this amendment alongside R12.
**Cost:** $0 chat work. No CC changes; this is a design-of-record amendment that changes spec text consumed by future v0.5.7/v0.5.8 spec-drafting chats.

---

## 1. Why this amendment exists

R12 was locked before R9 (identifiability) and R10 (RLS shard architecture) existed. R12 specifies the continuous confidence formulas for `conf_A`, `conf_B`, and `conf_C`, plus the user-facing categorical thresholds (0.10 / 0.40 / 0.70) for warming/converged/optimized states. R9/R10 subsequently established a binary admit/reject gate sitting *upstream* of any conf_B or conf_C output: shards that have not satisfied the R10 warmup criteria (`n_samples ≥ 5·d²` AND `tr(P) ≤ 0.5·tr(P_0)` AND R9's `κ ≤ 10⁴`) MUST NOT have their output consumed by the controller — they're in warmup, and Layer A's curve runs unaltered.

R12 as written would compute non-zero `conf_B` and `conf_C` values for shards that haven't passed warmup, because R12's continuous formulas (workload_variety × coupling_stability × sample_density for conf_B; saturation_admit × residual_term × covariance_term × sample_count_term for conf_C) all return non-zero when their inputs are non-zero, regardless of whether the underlying RLS estimator's output is admissible.

This amendment closes that gap. It does NOT recompute the user-facing categorical threshold values (0.10 / 0.40 / 0.70) — those remain. It adds explicit R9/R10 binary gates that force `conf_B = 0` and `conf_C = 0` until the upstream estimator passes warmup. The continuous formulas remain unchanged for the post-warmup regime where R12 was originally formulated to operate.

The conf_A formula is unaffected: Layer A doesn't use RLS shards, has no warmup criteria in R9/R10's sense, and R12's conf_A already incorporates R8's tier ceiling multiplicatively. No drift.

---

## 2. Drift points and amendment text

### Drift 1 — `conf_B` requires R10 warmup admission gate

**Current R12 §conf_B:**

```
conf_B(channel c) =
    workload_variety(c) * coupling_stability(c) * sample_density(c)
```

**Conflict:** R10 §R10.4 gates Layer B shard output on `n_samples ≥ 5·d_B²` AND `tr(P_B) ≤ 0.5·tr(P_B,0)` AND `κ_B ≤ 10⁴`. Layer B shards that haven't met all three criteria are in warmup; the controller is not allowed to consume their output. R12's conf_B formula returns non-zero whenever workload_variety > 0 and sample_density > 0, which begins long before the warmup criteria are met.

**Amendment text:**

> §conf_B (revised). Add a binary admission gate upstream of the continuous formula:
>
>     conf_B(channel c) =
>         R10_warmup_admit_B(c) *
>         workload_variety(c) * coupling_stability(c) * sample_density(c)
>
> where:
>
>     R10_warmup_admit_B(c) = 1.0 if shard_B[c] has passed R10 warmup
>     R10_warmup_admit_B(c) = 0.0 otherwise
>
> R10 warmup for Layer B is satisfied when, simultaneously: n_samples ≥ 5·d_B² (where d_B is the channel's Layer B regressor dimension per R9.1, capped at 18); tr(P_B) ≤ 0.5·tr(P_B,0) where P_B,0 is the initial covariance per R10.4; κ_B ≤ 10⁴ per R9's online detector. The gate is binary because admitting partial Layer B output during warmup would feed the blender (R12.4) input that the R9/R10 contract explicitly forbids the controller from consuming.

### Drift 2 — `conf_C` requires R10 warmup admission gate

**Current R12 §conf_C:**

```
conf_C(channel c, signature s) =
    saturation_admit(c, s) * residual_term(c, s) * covariance_term(c, s) * sample_count_term(c, s)
```

**Conflict:** Symmetric to Drift 1. R10 §R10.4 gates Layer C overlay shard output on the same three-condition warmup criterion (with d_C = 2 for marginal-benefit shards, so `n_samples ≥ 5·d_C² = 20` is the sample floor). R12's conf_C `sample_count_term` and `covariance_term` will return non-zero before warmup completes, in contradiction with R10's hard rule that pre-warmup output is masked.

Note: `sample_count_term` in R12 is currently underspecified — the R12 doc lists it as a multiplicative term without giving its formula. The amendment fixes this by folding it into the explicit warmup admission rather than leaving it as a separate continuous term.

**Amendment text:**

> §conf_C (revised). Add a binary admission gate upstream of the continuous formula:
>
>     conf_C(channel c, signature s) =
>         R10_warmup_admit_C(c, s) *
>         saturation_admit(c, s) * residual_term(c, s) * covariance_term(c, s)
>
> where:
>
>     R10_warmup_admit_C(c, s) = 1.0 if shard_C[c, s] has passed R10 warmup
>                                AND R10_warmup_admit_B(c) == 1.0
>                                AND R9_identifiable(c, s) == 1.0
>     R10_warmup_admit_C(c, s) = 0.0 otherwise
>
> R10 warmup for Layer C is satisfied when, simultaneously: n_samples ≥ 5·d_C² = 20; tr(P_C) ≤ 0.5·tr(P_C,0); κ_C ≤ 10⁴. Layer C admission additionally requires the parent Layer B shard to have passed warmup (per R10's parent-prior architecture: Layer-C overlay shards seed θ_0 from the parent Layer-B shard per R10.4) and R9's identifiability classification to be `healthy` (see Drift 3).
>
> The previously-listed `sample_count_term(c, s)` in R12's continuous formula is **subsumed by R10_warmup_admit_C**. After warmup admission, sample count is no longer a continuous confidence multiplier; it has already done its job by gating warmup. This simplifies the formula to three continuous terms (saturation, residual, covariance) post-admission.

### Drift 3 — R9 identifiability classification gates `conf_C`

**Current R12 §conf_C:** No reference to R9's identifiability classification. The covariance_term will return high confidence for a shard with shrunken `tr(P)` regardless of whether the underlying regressor is structurally identifiable.

**Conflict:** R9 §R9.6 enumerates structural unidentifiability cases (U1–U6). A shard can pass R10's `tr(P) ≤ 0.5·tr(P_0)` warmup criterion through observed information *along excited directions only* while remaining unidentifiable in unexcited subspaces. R10 §R10.7 bullet 4 says "R9's PE/κ detector results are reported in shard metadata so doctor can surface them as informational, never error." R12 needs to consume these as confidence-zeroing gates, not just informational annotations.

R10 §R10.4 enumerates four R9 classifications: `healthy | marginal | co-varying-grouped | unidentifiable`. The `co-varying-grouped` case means R10 has detected a fan group whose `b_ij` columns are linearly dependent and has merged them into a composite. R10 keeps these shards alive (they're still useful for the composite estimate), but a Layer C shard whose channel sits inside a co-varying group cannot uniquely identify its own `b_ij` contribution and therefore cannot meaningfully estimate marginal benefit — its conf_C MUST be zero.

**Amendment text:**

> §conf_C (revised, addendum to Drift 2 amendment). The `R9_identifiable(c, s)` predicate in R10_warmup_admit_C is defined as:
>
>     R9_identifiable(c, s) = 1.0 if R9_classification(shard_C[c, s]) == "healthy"
>     R9_identifiable(c, s) = 0.5 if R9_classification(shard_C[c, s]) == "marginal"
>     R9_identifiable(c, s) = 0.0 if R9_classification(shard_C[c, s]) == "co-varying-grouped"
>     R9_identifiable(c, s) = 0.0 if R9_classification(shard_C[c, s]) == "unidentifiable"
>
> The `marginal` partial admission (0.5) reflects R9's intermediate state where the shard is identifiable but with elevated condition number (`10² ≤ κ ≤ 10⁴`). Output is admissible but flagged as low-quality; the 0.5 multiplier propagates this into the user-facing categorical state — a marginal shard's conf_C is capped at 0.5, which never crosses the 0.40-and-stable threshold for the "predictive primary" label even with otherwise healthy continuous terms.
>
> This is the only place in R12 where the "binary admission" framing relaxes to a soft 0.5 multiplier. The relaxation is justified because R9's `marginal` is a continuous regime (κ between 100 and 10000) rather than a structural impossibility, and forcing it to zero would discard genuine partial information.

### Drift 4 — `covariance_term` formula reconciliation with R10 warmup criterion

**Current R12 §conf_C:**

```
covariance_term = clamp(1 - tr(P̂) / dim(θ), 0, 1)
where P̂ = P / P_init
```

**Conflict:** R12's `covariance_term` is a continuous score on the *normalized* trace `tr(P) / tr(P_0)`. R10's warmup criterion is a binary cut on the *absolute* condition `tr(P) ≤ 0.5·tr(P_0)`. These are related but different: R10 admits when `tr(P̂) ≤ 0.5·dim(θ)` (since P_0 = α·I gives `tr(P_0) = α·dim(θ)`, normalized `tr(P̂_0) = dim(θ)`, so R10's `tr(P) ≤ 0.5·tr(P_0)` becomes `tr(P̂) ≤ 0.5·dim(θ)`), while R12's covariance_term uses `1 - tr(P̂)/dim(θ)`.

Substituting R10's admission threshold into R12's formula: at the moment of admission, `tr(P̂) = 0.5·dim(θ)`, so `covariance_term = 1 - 0.5 = 0.5`. This means a shard that has *just* passed R10 warmup enters R12's continuous regime with `covariance_term = 0.5` and contributes `conf_C ≤ 0.5` (the other multiplicative terms can only reduce it further). The first non-zero conf_C is bounded above by 0.5, climbing toward 1.0 as `tr(P̂)` continues to shrink.

This is consistent and correct, but it should be stated explicitly so future spec-drafting chats and the v0.5.7/v0.5.8 implementers don't re-derive it.

**Amendment text:**

> §conf_C covariance_term (clarification, no formula change). The continuous covariance_term operates on the normalized covariance trace `tr(P̂) = tr(P/P_init)`. R10's binary warmup criterion `tr(P) ≤ 0.5·tr(P_0)` corresponds to `tr(P̂) ≤ 0.5·dim(θ)` (since `P_init = α·I` is diagonal). At the moment of R10 warmup admission, `tr(P̂) = 0.5·dim(θ)` and therefore `covariance_term = 0.5`. The shard's first admissible conf_C is bounded above by 0.5; conf_C climbs toward 1.0 only as continued observation drives `tr(P̂)` further down.
>
> This means the user-facing "predictive primary" threshold (conf_C ≥ 0.40) is reachable post-warmup *only* if saturation_admit and residual_term are both close to 1.0 simultaneously. A noisy environment (residual_term well below 1.0) keeps conf_C in the warming band (0.10–0.40) even after R10 warmup admission. This is the desired behavior — R10 admission says the estimator has converged enough to *output*, but R12 separately decides whether the output is *trusted enough to weight in the controller*. Two distinct gates with two distinct purposes.

### Drift 5 — User-facing categorical thresholds remain unchanged

**Current R12 §Q7:**

| Internal w_pred(c) range | Categorical state |
|---|---|
| 0.00 (hard pin) | Cold-start |
| 0.00–0.10 | Warming |
| 0.10–0.40 | Warming |
| 0.40–0.70 | Converged |
| 0.70–1.00 | Optimized |

**Audit conclusion:** The thresholds remain correct under the new R10 warmup gates. Reasoning:

The warmup gates force `conf_B = 0` and `conf_C = 0` during their respective warmup phases. The `min()` aggregation in spec-smart-mode §8 means `w_pred = min(conf_A, conf_B, conf_C) = 0` while either Layer B or Layer C is in warmup. Combined with the cold-start hard-pin (R12 §Q4: 5 minutes after Envelope C, 10 minutes after Envelope D), this means:

- During the hard-pin window: `w_pred = 0` (hard-pinned regardless of formula). User-facing state: "Cold-start — Calibrating, reactive only."
- After hard-pin, during Layer B warmup: `w_pred = 0` (forced by conf_B = 0 in min). User-facing state remains: still in the 0.00–0.10 band, "Warming — reactive primary."
- Layer B warmup completes, Layer C warmup begins on first observed signature: `w_pred` rises from 0 only when at least one (channel, signature) pair has admitted. User-facing state: depending on conf_A and conf_B continuous values, this typically lands in 0.10–0.40 ("Warming — predictive contributing") immediately on Layer C admission per Drift 4 (covariance_term ≥ 0.5, but min() clamps to whichever layer is lowest).
- Steady state: all three layers admitted, continuous formulas in their normal operating regime. Thresholds work as originally specified.

**Amendment text:**

> §Q7 (no formula change; clarification only). The user-facing categorical thresholds remain at 0.10 / 0.40 / 0.70 with hysteresis bands of ±0.02. Behavior under the R10/R9 admission gates added by Drifts 1–3:
>
> - The Cold-start (0.00 hard-pin) state now persists until *both* the time-based hard-pin elapses *and* Layer B warmup admits. This makes the cold-start band slightly longer in practice but does not require a numeric threshold change.
> - The Warming bands (0.10–0.40) are typically populated by `min()` clamping, where one of the three layers is the limiting term. The aggregation correctly surfaces "predictive is contributing but not primary."
> - The Converged threshold (≥ 0.40) requires all three layers to be simultaneously above 0.40 due to `min()`. R10 admission gives Layer B/C a starting covariance_term of 0.5; conf_B and conf_C therefore typically enter the Converged band shortly after admission, gated by their other continuous terms (workload_variety for B, residual_term for C). This is exactly the design intent.

---

## 3. Cross-cutting changes

### 3.1 Cold-start ramp-up timing is now layer-bounded

R12's original cold-start treatment focuses on the time-based hard-pin (5 min after Envelope C, 10 min after Envelope D). The amendment adds layer-bounded warmup as a *second* gate that may extend cold-start beyond the time-based pin.

For a typical desktop:

- Envelope C completes at t=0. Hard pin holds w_pred=0 until t=300s (5 min).
- Layer A starts learning. conf_A rises continuously per R12's existing formula.
- Layer B shard starts accumulating samples. d_B = 2+N where N is the channel's coupled-fan count (typically 2–4 on a desktop). At 0.5 Hz fast-loop tick, n_samples ≥ 5·d_B² for d_B = 6 means 180 samples = 360 seconds. Layer B warmup typically completes around t=360s, but may be delayed in idle-dominated regimes (R9 §R9.7 E3: idle-heavy classification can run for hours without admission).
- Layer C overlay shards activate per-signature on τ_act=60s dwell. d_C=2 means n_samples ≥ 20 = 40 seconds at fast-loop. Each signature's first Layer C admission therefore lands ~40s after activation.

So the typical desktop flow: Cold-start 0–5 min, then Warming 5–~7 min as Layer B admits, then per-signature Converged transitions as Layer C admits each signature. This is consistent with R12's original "diverse workloads observed = 24 h" steady-state convergence target.

For an idle-dominated NAS: Layer B warmup may take days because the regressor is poorly excited. The amendment makes this visible (conf_B = 0 during this period) rather than hidden behind continuous formulas that return small-but-non-zero values that mislead users into thinking learning is progressing.

### 3.2 Doctor surface impact

R13's "aggregate confidence" live metric (R13 §2.1, computed as mean of per-channel R12 conf) and the per-channel conf_A/conf_B/conf_C breakdown in the internals fold (R13 §4.2 sub-sections 7 and 8) will reflect the warmup gates correctly without R13 amendment. The R10 identifiability classification per shard (R13 §4.2 sub-section 6) was already specified — this amendment ensures R12's conf values are consistent with that classification rather than diverging from it.

No R13 amendment needed.

### 3.3 Persistence semantics unchanged

R12 §Q8 specifies that confidence formula *inputs* are persisted (counts, residuals, RLS state), not outputs. The amendment preserves this: the warmup admission gate is computed from the persisted RLS state (n_samples, tr(P), κ from R9 detector) and the persisted classification — both are inputs, not outputs. On daemon restart, the gates evaluate correctly from loaded state without any persistence schema change.

R12 §Q8's "freshness penalty" (cold-start hard pin re-applies if `time_since_last_persistence > 24 h`) interacts with the new gates: a daemon that has been offline for >24h will hard-pin AND re-evaluate R10 warmup against the persisted state. If R10 warmup is still satisfied (state has not deteriorated mathematically — it cannot have, since no observations were taken), conf_B and conf_C admission resumes once the time-based hard-pin elapses. This is correct and conservative.

---

## 4. Out-of-scope clarifications

These were considered during the audit and explicitly NOT amended:

- **`min()` aggregation rule** (spec-smart-mode §8). Unchanged. The new warmup gates strengthen `min()`'s correctness: forcing zero on either conf_B or conf_C while the other is still warming guarantees `w_pred = 0` during incomplete warmup, which is the intended safety property.
- **conf_A formula.** Unchanged. Layer A doesn't have R10 shards; R8 tier ceiling already incorporated.
- **Smoothness mechanism (LPF + Lipschitz).** Unchanged. Operates on the post-aggregation w_pred regardless of how the per-layer values arrive.
- **Drift detection (T_half = 60s, freeze_threshold = 0.05).** Unchanged. Drift response is downstream of the formula and consumes its output.
- **Cold-start hard-pin durations** (5 min for Envelope C, 10 min for Envelope D). Unchanged. Time-based pin is one of two cold-start gates; warmup-based gate is the other; both must elapse for w_pred to leave 0.
- **Persistence structure.** Unchanged.
- **Categorical threshold values** (0.10 / 0.40 / 0.70). Unchanged. Drift 5 audit confirms they remain correct.

---

## 5. New invariant bindings

For the v0.5.7 (Layer B) and v0.5.8 (Layer C) implementation specs:

- **`RULE-CONF-B-WARMUP-GATE`** — conf_B MUST return 0.0 if `R10_warmup_admit_B(c) == 0`. Test fixture: synthetic shard at n_samples = 5·d_B² - 1 returns conf_B = 0; at n_samples = 5·d_B² with tr(P) ≤ 0.5·tr(P_0) and κ ≤ 10⁴, returns continuous formula value.
- **`RULE-CONF-C-WARMUP-GATE`** — conf_C MUST return 0.0 if `R10_warmup_admit_C(c, s) == 0`. Test fixture: parent Layer B shard not admitted → conf_C = 0; Layer C shard at n_samples = 19 → conf_C = 0; both admitted with healthy R9 classification → continuous formula.
- **`RULE-CONF-C-IDENTIFIABILITY-GATE`** — conf_C MUST return 0.0 when R9 classification is `unidentifiable` or `co-varying-grouped`, MUST cap at 0.5 when classification is `marginal`, MUST be 1.0 multiplier when classification is `healthy`.
- **`RULE-CONF-MIN-AGGREGATION-WARMUP`** — w_pred MUST equal 0 when any of conf_A, conf_B, conf_C is 0. Test fixture: conf_A = 0.8, conf_B = 0.0 (warmup), conf_C = 0.0 (warmup) → w_pred = 0.

These bindings live in `.claude/rules/conf-*.md` files in the v0.5.7 and v0.5.8 PRs.

---

## 6. Cross-references

- **R9** — identifiability classification (`healthy | marginal | co-varying-grouped | unidentifiable`) and online κ detector. Drift 3 consumes both.
- **R10** — RLS shard architecture, per-channel Layer B / per-(channel, signature) Layer C. Warmup criterion `n_samples ≥ 5·d²` AND `tr(P) ≤ 0.5·tr(P_0)` AND `κ ≤ 10⁴`. Drifts 1, 2, 4 consume.
- **R12** — confidence formula being amended. All five drifts amend specific sections.
- **R13** — doctor surfaces. §3.2 confirms no R13 amendment needed; the new gates surface correctly through existing R13 fields.
- **R15** — spec-05 audit. R15 already noted the spec-05 RLS implementation must use bounded-covariance forgetting (directional or EFRA, not pure exponential). This amendment is downstream — it assumes that R15 amendment lands. If pure-exponential forgetting were retained, `tr(P)` could grow unbounded, R10 warmup criterion `tr(P) ≤ 0.5·tr(P_0)` could be re-satisfied by the math even when the estimator is diverging, and the warmup gate becomes meaningless. The R15 → R12 dependency is explicit: R15 must merge before R12-amended v0.5.8 ships.
- **spec-smart-mode §8** — `min()` aggregation. Unchanged but amendment strengthens correctness.
- **spec-16** — persistence. Unchanged. R10 warmup state (n_samples, tr(P), R9 classification) is part of R10's persistence shape, already specified.

---

## 7. Cost and integration

- **Chat work:** $0 (this document).
- **CC implementation impact:** zero new code files. The amendment changes the contract that `internal/confidence/layer_b.go` and `internal/confidence/layer_c.go` must satisfy. The five RULE bindings add five test fixtures to `internal/confidence/layer_b_test.go` and `internal/confidence/layer_c_test.go` in the v0.5.7 / v0.5.8 PRs. Estimated additional CC time: <30 minutes per PR, within existing v0.5.7 and v0.5.8 cost estimates.
- **Spec-drafting impact:** the v0.5.7 and v0.5.8 spec drafts must reference this amendment alongside R12. The spec-smart-mode design-of-record gets a one-line note in §8 pointing here.

---

## 8. Conclusions actionable

When v0.5.7 (Layer B) and v0.5.8 (Layer C) spec drafts happen (in chat, $0):

1. Both specs reference R12 + this amendment as the design-of-record for confidence formulas.
2. Both specs include the four new RULE bindings (`RULE-CONF-B-WARMUP-GATE`, `RULE-CONF-C-WARMUP-GATE`, `RULE-CONF-C-IDENTIFIABILITY-GATE`, `RULE-CONF-MIN-AGGREGATION-WARMUP`) in their `.claude/rules/` sections.
3. v0.5.8 spec must verify R15 spec-05 amendment (bounded-covariance forgetting) is in place before merge — this is now a hard prerequisite, not a soft cross-reference.
4. The user-facing categorical labels (Cold-start / Warming / Converged / Optimized) remain as R12 originally specified; no UI rework required in spec-12 amendments beyond what's already planned.
5. Doctor (R13, v0.5.10) consumes the amended conf values transparently — no doctor amendment needed.

---

**Amendment complete.** R12 + this amendment is the locked design-of-record for the blended controller's confidence formula. v0.5.7 and v0.5.8 implement against this combined contract.
