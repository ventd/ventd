# R17 — Multi-Channel Coordination and Aerodynamic Interference in Autonomous Fan Controllers

**Status:** Research complete, spec-ready
**Position in program:** Phase 4 (post-HWCURVE, pre-DITHER); R17 of the smart-mode research bundle
**Author:** ventd research desk
**Date:** 2026-04-30

---

## 0. Executive summary

ventd's smart-mode currently treats each fan channel as an independent SISO control loop with three learned layers: A (PWM→RPM curve), B (channel→sensor thermal coupling), and C (per-(channel, workload) RLS estimator). The architectural blind spot is **fan-fan aerodynamic coupling** — the fact that, in any case with non-trivial impedance topology, the airflow produced by one fan at a given PWM is a function of every other fan's RPM, because all fans share the same chassis impedance network. Layer A is a function of PWM only; in reality it is a function of (PWM, **p_chassis**), where p_chassis depends on every other fan in the same flow domain.

The net of this research is:

1. **Adopt a parsimonious lumped-pressure model.** Use a one-parameter coupling coefficient γ_ij per ordered (channel, neighbour) pair with the constraint that ventd only ever fits γ for pairs that the auto-discovery layer has flagged as coupled. Reject MIMO-RLS and DMPC as out-of-scope: they violate the Celeron budget, the single-binary constraint, and Layer C's locked state shape.
2. **Detect coupling cheaply.** Use a two-stage detector: a coarse Pearson cross-correlation between Layer A residuals and neighbour RPMs, gated by a confidence threshold (R8), then a one-shot bias-corrected Granger-style F-test on a short window. Bristol's Relative Gain Array (RGA) framing motivates the math but the actual computation collapses to ~4×4 Gram matrix operations.
3. **Calibrate sparsely.** Replace any notion of full-factorial pair sweeps with two-level fractional-factorial (Resolution III) calibration only for channels flagged at runtime as members of a coupling group; default behaviour stays one-at-a-time.
4. **Compensate, don't co-optimise.** Each channel keeps its independent control law; coupling shows up as a feed-forward correction to the PWM target, and as an inflation of Layer A's residual variance bound (which feeds R8 confidence and R12 drift detection). No joint optimisation, no DMPC, no game-theoretic coordination.
5. **Spec target: v0.7.x INTERFERENCE,** with the doctor surface and group-discovery wired in v0.7, and the residual feed-forward pushed to v0.8.0 once R16 anomaly detection lands (because the failure modes overlap and want a shared event taxonomy).

The remainder of this document is the long-form research narrative; §12 is the spec-ready findings appendix.

---

## 1. Aerodynamic coupling taxonomy

### 1.1 Series and parallel fan combination

The textbook fluid-mechanics result, presented in Cengel & Cimbala *Fluid Mechanics: Fundamentals and Applications* and Crowe et al. *Engineering Fluid Mechanics*, treats fans as nonlinear two-port elements characterised by a **fan curve** Δp(Q) — pressure rise as a function of volumetric flow rate. The system the fans push against is a **system curve** Δp_sys(Q) ≈ k·Q² for turbulent flow through a fixed geometry. The **operating point** is the intersection of fan curve and system curve.

For two identical fans:

- **Series** (same Q, pressures add): Δp_total(Q) = 2·Δp_fan(Q). Operating point shifts up the system curve. *Series operation gives the largest benefit in high-impedance systems.* Push-pull radiator and stacked tower-cooler configurations are the canonical examples.
- **Parallel** (same Δp, flows add): Q_total(Δp) = 2·Q_fan(Δp). Operating point shifts right on the system curve. *Parallel operation gives the largest benefit in low-impedance systems.* Side-by-side intake fans on a NAS are the canonical case.

Manufacturer technical notes (Noctua "Axial fans in series or parallel operation"; Comair Rotron application notes; ACHR News' multi-fan systems primer) all warn that the linear superposition above is an **upper bound** and that interference, surge regions in the curve, and turbulence at the boundary between fans cause real-world performance to fall short. Comair Rotron explicitly notes that with five or more fans in either topology, regions of the combined curve become **unstable and unpredictable**, and recommends laboratory testing.

### 1.2 The flow network method (FNM)

For chassis with more than two fans and non-trivial geometry the appropriate canonical model is the **Flow Network Method**, described in the electronics-cooling literature (Belady & Minichiello, IBM/HP technical reports, and the survey accessible via NSF PAGES *Impact of Fans Location on the Cooling Efficiency of IT Servers*). FNM treats the chassis as a directed graph: edges carry flow Q with a quadratic impedance Δp = R·Q·|Q|; fans are edges with active pressure sources Δp_fan(Q, ω); junctions enforce mass conservation Σ Q = 0; loops enforce pressure conservation Σ Δp = 0. The result is a system of nonlinear algebraic equations that can be linearised around an operating point to yield a small Jacobian — this is the formal object that ventd needs an approximation of.

The crucial published result (NSF PAGES paper; Pandiyan thesis, UTA): **at lower or negative server pressure differential, fan power becomes strongly dependent on operating pressure differential, and an imbalance of internal impedances can onset recirculation of exhaust air within the chassis.** Recirculation is the worst-case failure mode of independent control: a fan saturates, pressure equalises, and an adjacent "intake" fan begins to suck from the wrong side of the chassis. ventd cannot detect or prevent this from PWM/RPM telemetry alone, but it must at least avoid driving the system into such a regime.

### 1.3 Fan arrays in HVAC / CRAC fans

The data-centre cooling literature (Patel et al., HP Labs Dynamic Smart Cooling; Lazic et al., NeurIPS 2018, *Data center cooling using model-predictive control*; the MDPI proactive-cooling-control paper) treats banks of CRAC fans and AHU fans as a MIMO system. Practical industrial controllers almost universally fall back to **decentralised PI per fan with an outer pressure-setpoint loop** rather than full MIMO MPC, because the inner-loop dynamics are fast and well-decoupled by the plenum's pressure averaging. The Lazic paper (Google DC) explicitly chooses a linear MPC with median-aggregated cold-aisle temperatures and DP sensors, and notes that the coupling between AHUs is *intentionally* mediated through the plenum rather than via direct fan-fan models. **This pattern — push the coupling onto a shared scalar (plenum pressure or aggregate flow) and treat each fan as SISO against that scalar — is the architecturally cheapest way to handle aerodynamic coupling and is the pattern ventd should adopt.**

### 1.4 Push-pull radiator and stacked tower coolers

For a push-pull AIO/custom-loop radiator (M=2), Noctua's manufacturer testing and *MartinsLiquidLab* numerical work both report that push-pull at moderate RPM is closer to **series** than to "doubled flow," with an ~10–25 % improvement in heat-transfer-coefficient at a fixed coolant ΔT versus single-side. Crucially, the two fans are mechanically and aerodynamically **rigidly coupled**: their RPMs cannot be controlled independently without inducing pressure imbalances that reduce, rather than increase, total flow. The right model is to treat M=2 push-pull as a **single virtual channel** with a doubled pressure source.

For Noctua D15 / Phanteks PH-TC14PE / Thermalright Dual-Tower coolers with stacked fans, the same logic applies: the cooler manufacturer specifies a recommended pair of fans and a relative speed convention. The ventd implication: when a "push-pull" or "stacked" coupling group is detected, the controller should *constrain* the two channels to a fixed RPM ratio (or PWM ratio) rather than letting them diverge.

### 1.5 Counter-rotating and contra-rotating axial pairs

Specialised topology used in industrial server fans (Delta PFC/PFR series, Sanyo San Ace 9CRA). The aerodynamic coupling is by design tight; the rear rotor recovers swirl from the front rotor and the assembly behaves as a single high-static-pressure unit. From ventd's perspective this looks exactly like push-pull (M=2 virtual channel) except both fans share a single tachometer in many implementations. No special handling beyond §1.4.

### 1.6 Automotive engine cooling

Literature here (Salah et al., *Multivariable model predictive control of an electric variable cam timing engine*; the Purdue *Decoupled Feedforward Control for an Air-Conditioning and Refrigeration System* paper) analyses MIMO cooling problems with three coupled inputs: condenser fan, compressor speed, and expansion valve. The relevant method exported to ventd is **input-output pairing via the Relative Gain Array** (Bristol 1966; Skogestad & Postlethwaite *Multivariable Feedback Control* §3.4 and §10.6): given a 2×2 or 3×3 steady-state gain matrix G, the RGA element λ_ij = G_ij·(G⁻¹)_ji measures how much of channel j's authority over output i remains after the other loops are closed. Pairings near 1 are stable for decentralised SISO control; pairings near 0 or negative indicate strong coupling that decentralised control cannot tame. This is the textbook test ventd should use to decide *whether to bother* modelling the coupling at all.

### 1.7 Summary table

| Topology | Canonical model | M | ventd treatment |
|---|---|---|---|
| Push-pull radiator | Series (Δp adds) | 2 | Virtual single channel, fixed RPM ratio |
| Stacked tower-cooler | Series (Δp adds) | 2 | Virtual single channel, fixed RPM ratio |
| Contra-rotating server fan | Series + swirl recovery | 2 (1 tach) | Treat as one channel |
| Side-by-side intake array | Parallel (Q adds) | 2–3 | Coupling group with γ_ij |
| 1U server (front-to-back) | FNM, dominant series | 4–8 | Coupling group, optional virtual aggregate |
| NAS chassis (intake/exhaust/HDD) | FNM, multi-zone | 4–6 | Coupling group per zone |
| GPU AIO + case fans | Different domains, weak | 2 zones | Independent zones, no γ |
| Workstation w/ passive PSU | All forced by case fans | 3–5 | Coupling group |

---

## 2. Detection — when does coupling matter?

The literature offers a hierarchy of coupling-strength metrics that span several orders of magnitude in computational cost.

### 2.1 RGA and dynamic RGA

Bristol's RGA (Skogestad & Postlethwaite §3.4) requires only a static gain matrix G ∈ ℝ^{M×M} where G_ij = ∂(steady-state output i)/∂(input j). For ventd, "input j" is PWM_j and "output i" is the achieved RPM_i (or, equivalently, the achieved per-channel ΔT_i). The RGA element λ_ii close to 1 (say |λ_ii − 1| < 0.2) is the de-facto industry "diagonal-pairing is fine" threshold; values outside [0.5, 2.0] are widely cited (Verma & Padhy 2010; *Estimation of the Dynamic Relative Gain Array for Control...* DiVA 2016) as the threshold for needing decoupling.

Gramian-based dynamic extensions (Halvarsson; *Hankel Interaction Index*) frequency-resolve the metric but require an LTI MIMO state-space identification that ventd does not have and cannot afford to do online. We discard them.

### 2.2 Pearson cross-correlation on residuals

The cheap proxy that aligns with ventd's existing telemetry: compute, over a sliding window of N samples, the Pearson correlation ρ_ij between channel i's **Layer-A residual** (observed RPM minus Layer-A predicted RPM) and channel j's **RPM**. If ρ_ij is statistically significantly nonzero (Fisher's z-test, |ρ| > 0.3 with N≥256), j is aerodynamically influencing i. The cost is O(M²·N) per evaluation but with M≤8 and N≤512 this is trivial; on a Celeron J3455 it is sub-millisecond.

This corresponds to the methodology used in the *MDPI Evaluation of Granger Causality Measures* paper (Papana et al. 2019) for identifying coupling structure in multivariate time series, in its simplest bivariate Pearson form. Their evaluation across a dozen Granger-causality variants concludes that for **identifying which pairs are coupled at all** (as opposed to estimating coupling strength), the simplest measures are competitive with the most complex ones — a happy result for an embedded controller.

### 2.3 Granger causality (linear, bivariate)

The next step up: a linear-Gaussian bivariate Granger test asks whether the Layer-A residual of channel i is better predicted by including channel j's past RPM versus only its own past. For a VAR(p) model with p∈{1,2,3} this is a small F-test on the ratio of residual variances of restricted vs. unrestricted models. Total cost: a 2p×2p least-squares per pair, on a window of ~512 samples, which is ~10 µs per pair on Celeron.

This is the cheapest detection metric that distinguishes **j drives i** from **j and i are jointly driven by an exogenous input** (e.g., ambient temperature spike). For ventd it is overkill on most desktops but cheap enough to enable behind a feature flag for the NAS / 1U-server use cases. (Reference: *Information-based detection of nonlinear Granger causality...* Faes et al. 2011, PMID 21728495; *Evaluation of Granger Causality Measures...* Papana et al. 2019, MDPI Entropy 21:1080.)

### 2.4 What we discard

- **Cross-spectral coherence** requires Welch periodograms and FFTs over long windows; the additional sensitivity over Pearson is marginal at ventd's sample rates (1 Hz typical) and not worth the binary size or the dependency on a non-trivial DSP routine.
- **Mutual information / transfer entropy**: nonparametric and reference-implementation-heavy; the Faes paper itself notes that for monotone, near-linear couplings (which fan-fan aerodynamics is) the linear Granger test dominates on a per-sample basis.
- **Static-pressure differential signatures**: ventd has no pressure sensors. Discarded outright.

### 2.5 Confidence-gating

ventd already has the R8 confidence framework. Coupling detection must be gated by: (a) Layer A confidence ≥ HIGH on both channels; (b) at least N≥256 samples since last calibration; (c) no R12 drift event flagged in the window. Only then can the cross-correlation result be trusted to reflect coupling rather than calibration error.

---

## 3. Control law adjustments

### 3.1 The "do nothing fancy" baseline

A central finding from the survey of industrial fan-array control (Lazic et al. 2018; HP DSC; Open Compute server BMCs): **the dominant production pattern is decentralised SISO with a conservative airflow margin, not joint optimisation**. The reasons are (a) MIMO controller commissioning is a research project per chassis, (b) failure modes of joint control are correlated across fans (one bug stops *all* cooling), and (c) the gains over a well-tuned decentralised loop are typically <10 % at the cost of >10× implementation complexity. The ventd default must remain "treat each fan as independent SISO with conservative margin," and any coupling-aware behaviour is an opt-in correction on top.

### 3.2 Decentralised control with feed-forward decoupling

The right reference point is Skogestad & Postlethwaite ch. 10.6: *decentralised control with static decoupling*. Each channel runs its own loop. When a coupling group is detected with γ_ij significant, the PWM target of channel i is corrected:

  PWM*_i = PWM_i^solo + Σ_{j∈group, j≠i} γ_ij · (RPM_j − RPM_j^baseline)

where γ_ij is a scalar coefficient learned online (§5) and RPM_j^baseline is channel j's value at calibration time. This is a single multiply-add per neighbour per tick — bounded, transparent, and trivially auditable. It is also robust to a stale γ_ij estimate: in the worst case the correction is zero.

This is the **Internal Model Control (IMC) MIMO extension** of Garcia & Morari, restricted to the static (steady-state) decoupling matrix. Garcia & Morari themselves note that for systems with diagonal-dominant RGA, static decoupling captures >80 % of the available improvement.

### 3.3 What we explicitly reject for ventd

- **Joint MPC / DMPC** (Camponogara et al. 2002; Negenborn & Maestre 2014, *Distributed Model Predictive Control Made Easy*; Scattolini 2009 *Architectures for distributed and hierarchical MPC – a review*). DMPC requires per-tick QP solves and inter-agent message passing, both incompatible with a single-binary CGO_ENABLED=0 daemon on a Celeron. The closed-loop performance gain over decentralised + decoupling on this problem class is well-documented to be marginal (Stewart UCSB thesis, *Plantwide Cooperative DMPC*).
- **Game-theoretic / cooperative-Nash framings** (Riverso & Ferrari-Trecate plug-and-play DMPC). Beautiful theory; requires offline LMI solves that ventd cannot ship.
- **Centralised LQR on the joint state.** Exceeds Layer C's locked state shape and would require re-deriving R8/R12/R13 against a new state.

### 3.4 When does the simple answer outperform the fancy one?

Empirical conclusion from the literature: for M ≤ 4 channels with diagonal-dominant RGA (|λ_ii − 1| < 0.5 for all i), decentralised control with optional static decoupling is within 5 % of MPC on energy and within 1 K on peak temperature; the variance of *that gap* under modelling error is larger than the gap itself (Lazic et al. 2018 §5; Liu et al. 2026 MDPI proactive cooling). For ventd's user base — overwhelmingly desktop and small-NAS, M ≤ 6 — this is the right operating point.

---

## 4. State expansion

### 4.1 Storage budget per coupling group

Layer A is locked at k_A coefficients per channel (in the smart-mode spec, typically a 9-knot piecewise-cubic plus residual variance, ≈ 80 bytes). Layer B is k_B = (channels × sensors) ≈ 16 floats per system. Layer C has θ̂ ∈ ℝ^k with covariance P ∈ ℝ^{k×k} per (channel, signature); for k=4 and 8 signatures × 6 channels, that's 6·8·(4+16)·8 B = 7.7 kB — already locked.

The R17 addition is a **coupling matrix Γ ∈ ℝ^{M×M} of γ_ij** per coupling group, plus its sparsity mask S ∈ {0,1}^{M×M}, plus a per-pair sample count and confidence. With M ≤ 8 and at most one coupling group per system in 99 % of homelabs, the budget is:

  Γ:  64 floats × 4 B = 256 B
  S:  64 bits = 8 B
  meta: 64 × 8 B = 512 B
  total: < 1 kB per coupling group

Even at the worst case (M=8, two disjoint groups of 4), the joint coupling state is **<2 kB** — three orders of magnitude under any realistic budget. There is no RAM pressure.

### 4.2 Computational cost

Per tick:
- Compute corrections: M·deg(S) multiply-adds, where deg(S) is the average sparsity degree. For typical M=4 and full coupling, this is 12 madds — sub-microsecond.
- Update γ_ij: O(M²) RLS-style updates *only when calibration mode is enabled*, not every tick.
- Detect coupling: per-pair Pearson + (gated) Granger F-test. Pearson on N=256 samples is ~256 madds per pair. For M=6, that's 15 pairs × 256 ≈ 4 k madds, well under 1 ms on Celeron.

### 4.3 Sparse-coupling approximation

For M > 4, full Γ is wasteful. The detector in §2 produces a sparsity mask S; only the entries S_ij = 1 carry a γ_ij. For the canonical NAS topology (intake-zone {1,2}, exhaust-zone {3,4}, GPU-zone {5,6}) with weak inter-zone coupling, S has ~6 nonzeros out of 36 — an 83 % sparsity, and the per-tick cost drops accordingly. **We adopt the rule that ventd will not allocate γ_ij storage for pairs below the detector threshold, and will treat all unmasked pairs as identically zero.**

### 4.4 Locking guarantee versus R1–R16

The R17 state (Γ, S, meta) is **strictly additive** to the Layer-A/B/C state and is namespaced in a separate persisted store-section (`coupling/v1/`), so it cannot break R12 (drift), R13 (doctor), or R16 (anomaly) deserialisation. R8 confidence reads γ as an additional input but does not feed it back into Layer-A/B/C parameter updates — coupling is a read-only modifier for the controller, not an estimator update.

---

## 5. Calibration discipline

### 5.1 The combinatorics problem

A naive answer would be a full-factorial sweep: for each pair (i, j), drive channel j at L levels while sweeping channel i at L levels. With L=8 and M=6 channels, the calibration time is L²·M(M-1)·t_settle. At t_settle = 10 s and M=6, that is 8²·30·10 s ≈ 5 hours — unacceptable.

### 5.2 The right answer: Resolution-III fractional factorial + online refinement

Drawing on Box, Hunter & Hunter *Statistics for Experimenters* (2nd ed., ch. 7) and the system-identification literature (Ljung, *System Identification: Theory for the User*, 2nd ed., ch. 13–14):

1. **Default calibration remains one-at-a-time** (the existing HWCURVE behaviour). This calibrates Layer A as a SISO function under "all other fans at their idle baseline" condition.
2. **Coupling-extension calibration is gated** on the runtime detector flagging a coupling group. Only then do we run the extra sweep.
3. **The extra sweep is a Resolution-III two-level fractional factorial** in the M coupled channels: 2^(M−p) experiments where 2^p > M. For M=4 this is 4 experiments (each with all-pairs combinations of {low PWM, high PWM} per the Plackett-Burman design); for M=6 it is 8 experiments. At 10 s settling each, this is 40–80 s total on top of HWCURVE — acceptable.
4. **Online identification** continues during normal operation under R8-confidence-gated conditions. The γ_ij is updated by a miniature RLS (k=1 per pair) using the existing Layer-A residual. Forgetting factor λ ∈ [0.99, 0.995] per Vahidi et al. *Recursive Least Squares with Forgetting for Online...* (Clemson). This handles drift without any explicit re-calibration trigger.

### 5.3 Latin-hypercube — considered and discarded

Latin-hypercube sampling is space-filling and would be a natural choice for *fitting a nonlinear* coupling surface. We are fitting a 1-coefficient linear γ_ij, so LHS gives no benefit over fractional factorial, and it is harder for the doctor to explain ("why are these PWM combinations being driven?"). We use fractional factorial.

### 5.4 Parsimony — when is a 1-coefficient term enough?

The relevant theory: linearisation of FNM around an operating point. Let Q_i be channel i's volumetric flow and ω_j channel j's RPM. Then ∂Q_i/∂ω_j is, to first order, constant in a neighbourhood; second-order corrections require pressure-curve curvature, which for axial fans operating away from stall is benign. **One coefficient γ_ij captures >85 % of the coupling effect** in published push-pull data (NSF PAGES; Pandiyan UTA thesis Fig. 16). A second-order (γ_ij + δ_ij·ω_j²) extension is straightforward to add later if HIL data demands it — the architecture supports it without breaking — but R17's recommendation is to ship the 1-coefficient model first.

---

## 6. Coupling group identification (auto-discovery)

### 6.1 Goal

The user must not have to declare "fan 1 and fan 2 are coupled." ventd must learn it.

### 6.2 Algorithm

```
Every CALIBRATION_HORIZON ticks (default 24 h):
  if all channels have R8-confidence ≥ HIGH and no R12 drift event:
    for each ordered pair (i, j), i ≠ j:
       compute Pearson ρ_ij = corr(residual_A_i, RPM_j) over last N samples
       if |ρ_ij| > τ_pearson (default 0.30):
         run bivariate Granger F-test, threshold p < α (default 0.01)
         if pass:
           mark S_ij = 1
    construct undirected graph G with edges where S_ij ∨ S_ji
    extract connected components; each component of size ≥ 2 is a coupling group
    persist as `coupling/v1/groups`
```

### 6.3 Why connected components

The literature on inferring coupling networks from multivariate time series (Papana et al. 2019; Faes et al. 2011) repeatedly shows that **directionality** of a fan-fan link is unreliable in this regime: the graph is functionally undirected because the coupling is mediated by a shared scalar (chassis pressure). We therefore symmetrise S into an undirected graph and use connected components as the group definition, which is robust and matches the physical reality.

### 6.4 What we do *not* use

- **Granger causality directionality** alone, for the reason above.
- **BIOS-reported header location.** Considered — sysfs's `hwmon` does sometimes expose physical-position metadata — but unreliable across vendors and absent on most consumer boards. We use it as a *prior* to seed S only if it is present, never as the sole signal.
- **Clustering on cross-correlations** (e.g., spectral clustering of |ρ_ij|). Overkill at M ≤ 8; connected components on a thresholded graph is equivalent to single-linkage agglomerative clustering with no extra implementation cost.

### 6.5 Cost on Celeron MiniPC

Daily run, M=6, N=512: ~30 Pearson computations + ~6 Granger F-tests = <5 ms total. Negligible.

---

## 7. Specific case scenarios for the spec

For each, the **minimum coupling model** that captures the dominant effect:

### 7.1 Push-pull radiator (M=2)
- Dominant effect: series — Δp adds.
- Model: **virtual single channel.** Constrain PWM_2 = PWM_1 (or a fixed user-set ratio). One Layer-A curve, one Layer-B set, one γ = 0 because the two are aggregated.
- Detection signature: cross-correlation of (PWM_1, RPM_2) ≈ 1.0 even before coupling fit. Auto-detect via PRESET-PUSH-PULL when both channels are commanded by motherboard CPU_FAN+CPU_OPT and their RPM ratio is constant ≈ 1 over the last day.

### 7.2 Tower cooler with stacked fans (M=2, Noctua D15 push-pull)
- Same as 7.1.

### 7.3 NAS chassis with intake/exhaust array (M=4–6)
- Dominant effect: parallel within zones, weak series between zones.
- Model: **two coupling groups** (intake, exhaust); within each group, full Γ_block of size 2×2 or 3×3. Intra-group γ ≈ 0.05–0.20 typically (literature: equivalent fan performance curve model, *Equivalent fan performance curve model for optimizing jet fan selection in longitudinal ventilation systems of ultra-long tunnels*, ScienceDirect 2025); inter-group γ ≈ 0.
- Failure mode of ignoring it: the canonical R17 problem statement — fan 1's CFM at 80 % PWM is wrong because fan 3 has changed.

### 7.4 GPU AIO + case fans (M = 2 zones)
- Dominant effect: weak coupling, different thermal domains.
- Model: **independent.** Detector should output S = 0 for all cross-zone pairs.
- ventd contribution: confirm via R13 doctor that cross-zone γ stays near zero; if it doesn't, flag as a configuration mistake.

### 7.5 Server 1U chassis (M=4–8)
- Dominant effect: strong serial coupling (everything front-to-back through the same impedance).
- Model: **single coupling group of all fans** with full Γ. Plenum-style virtual-aggregate channel optionally added: a synthetic "aggregate RPM" sensor that feeds back to Layer C as the workload signature.
- Practical caveat: most 1U chassis are BMC-controlled and ventd will not directly drive them. Doctor surface to *inform* the user that ventd has detected such a topology and recommend BMC handover.

### 7.6 Workstation with passive PSU (M = 3–5 case fans)
- Dominant effect: every fan participates in a single shared flow domain.
- Model: **single coupling group.** Critical: detect saturation — if any channel is at 100 % PWM, the others must compensate or the PSU will overheat. This bleeds into R16 anomaly territory.

---

## 8. Failure modes

### 8.1 Failure modes of treating coupled fans as independent

1. **Calibration drift cascade.** Layer A's "true" calibration is conditional on neighbour state. A neighbour change is mis-attributed to fan-1 wear (R12), causing spurious recalibration triggers. This is the single most-important reason to ship R17.
2. **Hunting / oscillation.** If fan A and fan B are both controlling against the same temperature with overlapping authority, and Layer-A errors push both above the target, both ramp up; achieved cooling overshoots; both ramp down; repeat. Classic decentralised-control limit cycle. The Skogestad & Postlethwaite *RGA-pairing rule 1* directly addresses this.
3. **Thermal runaway near saturation.** When fan A saturates (100 % PWM), the controller's only remaining authority is fan B, but fan B's effective curve has shifted because A is now at maximum. Independent control will under-respond. (Failure mode documented for server fan banks: NSF PAGES *Impact of Static Pressure Differential* paper, recirculation regime.)
4. **Acoustic beats.** Two fans at very close RPM produce audible beat frequency at |Δω|. Touches R18 (acoustic) — not an R17 problem to solve, but R17 must surface coupled-RPM information so that R18 can use it.
5. **Power waste.** Decoupled control over-provisions because each loop carries its own conservative margin; coupled control can share margin. This is a tertiary concern.

### 8.2 Failure modes of over-modelling coupling

1. **Spurious γ_ij from confounded inputs.** Two fans both ramp under workload. Pearson correlates them, but the cause is workload, not aerodynamics. **Mitigation:** the detector regresses on Layer-A *residuals* (not raw RPMs), which by construction subtract out the workload-driven mean. This is the central reason to gate detection on R8-HIGH confidence in Layer A.
2. **Calibration time blow-up.** Mitigated by the fractional-factorial discipline (§5).
3. **State explosion at large M.** Mitigated by sparsity mask (§4.3).
4. **User confusion.** A user who looks at the doctor surface and sees a γ_ij of 0.13 will not know what to do with it. **Mitigation:** doctor reports coupling as a categorical {NONE, WEAK, MODERATE, STRONG} bucket plus "ventd will treat fans X and Y as a coupled group," not a raw coefficient.
5. **Cascading drift.** A wrong γ_ij applied as feed-forward decoupling can *worsen* control. **Mitigation:** every γ_ij correction is clipped to ±5 % of nominal PWM; if the post-correction PWM is more than 10 % from the pre-correction value, the correction is rejected and an R16 anomaly is raised.

---

## 9. Existing implementations — survey

| System | Coupling awareness | Notes |
|---|---|---|
| Dell iDRAC9 / PowerEdge | Zone-based, **no learned coupling.** Fan-speed offset, thermal profile, min-fan-PWM. | RACADM thermal settings expose offset and minimum, but the algorithm is closed-source and zone-table-driven (Dell PowerEdge custom-cooling whitepaper). |
| HPE ProLiant Smart Storage | Zone-based with PCIe-card-specific offsets; closed table. | Same architecture as Dell. |
| HP Labs Dynamic Smart Cooling (Patel et al. ~2003–2007) | **Centralised, sensor-array driven**, vent-level control of CRAC fans. Compresses fan-fan coupling into a plenum-pressure scalar. | The architectural pattern ventd should emulate at single-chassis scale. |
| Google MPC (Lazic et al. NeurIPS 2018) | Linear MPC over AHU fans + valves with median-aggregated DP/CAT sensors. | Industry best practice for DC scale; explicitly uses median-aggregation to side-step explicit fan-fan models. |
| Asus / MSI motherboard firmware | Per-header curves; **no coupling.** CPU_FAN and CPU_OPT can be linked but only as identical-curve. | Trivial. |
| liquidctl (Aquacomputer Quadro/Octo, NZXT) | Per-channel PWM curve only; **no coupling, no learning.** | Github issues #824 and similar confirm: "operation not supported by the driver" for any cross-channel logic. |
| FanControl (Rem0o, Windows) | Mix curves (max/min/avg/sum/subtract); **no coupling, but offers user-configured curve mixing.** Hysteresis, response-time smoothing, RPM mode. | The most sophisticated free consumer tool; explicitly user-driven, not learned. ventd's R17 contribution over FanControl is *automatic* coupling discovery. |
| Aquasuite (Aquacomputer Octo/Quadro) | Setpoint and curve controllers per channel + virtual sensors (e.g., max/avg of multiple temps). | User must configure; no learning. |
| BACnet building automation | Fan-array sequencing, often with a master plenum-pressure setpoint. (ASHRAE Guideline 36.) | Same plenum-scalar pattern as DC. |
| Industrial fan banks (paper mill, cooling tower) | VFD per fan with master speed-set; staged on/off; no per-fan learning. | Robust, simple, well-understood. |

**The gap ventd fills:** *automatic, learned, single-binary, opt-out coupling-aware control on commodity Linux hosts with no SCADA, no Windows, and no cloud.* No surveyed tool combines (a) auto-detection of which fans are coupled, (b) bounded online learning of coupling coefficients, and (c) confidence-gated, drift-aware feed-forward decoupling. FanControl comes closest but requires manual configuration.

---

## 10. HIL validation strategy

### 10.1 What ventd's fleet can validate

| Host | Useful for |
|---|---|
| Proxmox (5800X+3060) | Multiple chassis fans through a single PWM hub if cabled in; **primary R17 HIL platform.** |
| 13900K | Single-channel; useful as control. |
| MiniPC (Celeron) | Performance budget verification: can the detector run without missing a tick? |
| Steam Deck | Single fan; not relevant to R17 but useful as sanity. |
| Three laptops | Single-fan or thermal-zone-coupled; not useful for R17. |

### 10.2 Synthetic experiments

1. **Two case fans facing each other** (artificial maximum coupling). Two PWM-controllable 120 mm fans mounted ~10 cm apart, axes coaxial, blowing toward each other. At equal RPM, the air recirculates and Layer A's apparent CFM drops to near zero. Cross-correlation should saturate at |ρ| > 0.8. Detector must produce S_12 = S_21 = 1, group = {1, 2}, |γ_12| > 0.5. **Validates extreme coupling detection.**

2. **Push-pull on a CPU cooler.** Stack two identical fans on a Noctua D15. Drive at independent PWMs. Monitor CPU ΔT vs (PWM_1, PWM_2). The achieved cooling should be near-monotone in (PWM_1 + PWM_2) and weakly dependent on (PWM_1 − PWM_2). γ_12 should be ≈ +1 (driving fan 2 has the same effect as driving fan 1). **Validates the canonical M=2 series-coupled topology.**

3. **One channel artificially throttled.** Tape a partial obstruction over fan 1's intake to simulate a clogged filter. Layer-A residual on fan 1 should rise. If fans 1 and 2 are coupled, fan 2's Layer-A residual should also rise (through γ_21). The detector should *not* spuriously increase γ_12 because the residual was caused by an exogenous restriction, not by a neighbour fan — this validates the workload/exogenous decoupling via residual regression. **Crosses with R16 anomaly detection.**

4. **Synthetic decoupled control.** Two fans on the same chassis but with separate ducting (e.g., one in a partitioned compartment). Detector should output S = 0; γ should not be allocated. **Validates the false-positive rejection.**

### 10.3 Where simulation must suffice

ventd does not have access to a 1U server, a NAS chassis with HDD vibration, or a server-room fan-array. For these the validation strategy is:

- Numerical: a small FNM solver (~200 LoC of pure Go) producing synthetic (PWM, RPM, ΔT) traces under known coupling matrices. Regress the detector against ground-truth Γ.
- Replay-based: import published time-series from open data-centre cooling datasets (ANL/Argonne open-data-center, several of the GitHub-hosted Open Compute fan datasets) and run the detector offline.

The combination of (a) Proxmox HIL on artificial maximum coupling and (b) numerical FNM regression is sufficient to ship.

---

## 11. Spec target version

### 11.1 Dependencies on prior R-items

- **Layer A maturity (R-prior, locked):** detector requires R8-HIGH Layer A confidence on both channels, so coupling discovery cannot run before Layer A has converged.
- **R12 drift:** detector must not run during a flagged drift window, lest stale parameters confound the residual.
- **R13 doctor:** doctor surface must explain coupling groups to the user (§12.6).
- **R16 anomaly:** coupling-failure regimes (saturation, recirculation) overlap with anomaly events. R17 must publish a structured event whose taxonomy aligns with R16's.
- **R18 acoustic (future):** R17 group structure is an input; R18 uses |Δω| within a group to predict beats.

### 11.2 Recommended phasing

- **v0.7.x INTERFERENCE (target):** ship the auto-discovery (§6), the doctor surface (§12.6), and the **read-only** group identification. No control-law change yet. This is the lowest-risk delivery and is invaluable telemetry independent of any controller change.
- **v0.8.0 (with R16):** ship the feed-forward decoupling correction (§3.2) gated by R8 confidence, with the R16 anomaly taxonomy already in place to handle saturation regimes safely.
- **v0.9.0+:** consider second-order coupling terms only if HIL data demands it.

This phasing minimises risk: if the detector is wrong (false positives on the desktop fleet), only the doctor surface is affected; the controller's behaviour is unchanged. We earn the right to alter control behaviour in v0.8.0 by then having a quarter of doctor-surface telemetry showing the detector is calibrated.

---

## 12. Spec-ready findings appendix

### 12.1 Coupling model choice

**Choice:** Per-pair scalar γ_ij in a sparse matrix Γ ∈ ℝ^{M×M} with mask S ∈ {0,1}^{M×M}, applied as a **feed-forward static decoupler** on top of independent SISO channel control.

**Rationale:** matches Skogestad & Postlethwaite's static-decoupling pattern; bounded RAM; bounded per-tick CPU; degrades gracefully (γ=0 is independent control); auditable; doesn't violate Layer-A/B/C state lock.

**Rejected:** MIMO-RLS, DMPC, joint LQR, game-theoretic, full-factorial calibration. Each violates one of {Celeron budget, single-binary, GPL-3 dep, locked R1–R16 state}.

### 12.2 State shape and RAM

```
struct CouplingGroup {
    members      []ChannelID         // up to 8
    gamma        [M][M]float32       // sparse via mask
    mask         uint64              // bitset of (i,j) edges
    sample_count [M][M]uint32
    last_update  uint64              // ticks
    confidence   [M][M]uint8         // R8-aligned bucket
}
```

Per-system: ≤ 2 groups in 99 % of cases ⇒ < 2 kB. RAM is not the binding constraint.

### 12.3 Coupling detection algorithm

```
DETECT_COUPLING(window N=512 samples, schedule = 24 h or on R12 quiescence):
  precondition: ∀ channel: R8(LayerA) = HIGH AND no R12-drift in window
  for each pair (i, j) with i < j:
    res_i = residual(LayerA_i over window)
    rho_ij = pearson(res_i, RPM_j)
    if |rho_ij| < 0.30: continue
    F = granger_F(res_i, RPM_j, p=2)
    if F not significant at alpha=0.01: continue
    mark S_ij = 1 (undirected)
  groups = connected_components(S)
  publish groups; raise event coupling.discovered if changed
```

### 12.4 Calibration sweep design

```
HWCURVE_DEFAULT: one-at-a-time PWM sweep per channel (existing behaviour, unchanged)

HWCURVE_COUPLED (gated on group membership):
  design = Plackett-Burman(M_group)        // 2^(M−p) runs, Resolution III
  for each row r in design:
     command (PWM_1, ..., PWM_M_group) = row r at low / high settings
     wait t_settle (default 10 s)
     record (RPM_i, ΔT_i) for all i in group
  fit γ_ij by least-squares on the design matrix
  online refinement: per-pair RLS with k=1, λ ∈ [0.99, 0.995]
```

Total extra calibration cost for M=4: 4 runs × 10 s = 40 s. For M=6: 8 × 10 s = 80 s.

### 12.5 RULE-* binding sketches

- **RULE-COUPLING-01 (group-discovery):** *Coupling groups are auto-discovered. No user configuration is required. The discovery runs at most once per CALIBRATION_HORIZON, gated on Layer A R8-HIGH confidence on all candidate channels.*
- **RULE-COUPLING-02 (additive state):** *The R17 coupling state is namespaced under `coupling/v1/` and is strictly additive to the locked Layer A/B/C state. Loss of `coupling/v1/` MUST NOT degrade SISO control; ventd must continue running with γ ≡ 0 if the file is missing or corrupt.*
- **RULE-COUPLING-03 (control-side gating):** *Feed-forward γ correction is only applied to PWM_i if (a) the group's last detection event is more recent than DRIFT_HORIZON, (b) γ_ij confidence ≥ MEDIUM, (c) the magnitude of the correction is ≤ 5 % of nominal PWM. Otherwise the correction is silently zero.*
- **RULE-COUPLING-04 (saturation interlock):** *When any channel in a coupling group is at 100 % PWM, all γ corrections within the group are suppressed and an R16 anomaly event `coupling.saturation` is raised.*
- **RULE-COUPLING-05 (push-pull preset):** *If two channels are commanded by the same motherboard header pair (CPU_FAN + CPU_OPT) and have a stable RPM ratio over the last DAY, ventd MAY treat them as a virtual single channel rather than as a coupling group. This is the only case where ventd modifies its channel-list.*
- **RULE-COUPLING-06 (doctor visibility):** *All discovered coupling groups, their member channels, and the categorical strength bucket {NONE, WEAK, MODERATE, STRONG} MUST appear in the doctor output. Raw γ values MAY appear behind a verbosity flag.*
- **RULE-COUPLING-07 (calibration-time bound):** *HWCURVE-COUPLED extra time MUST NOT exceed 120 s for any group of M ≤ 8. If the design exceeds this, ventd MUST fall back to HWCURVE-DEFAULT and emit a warning.*
- **RULE-COUPLING-08 (read-only at v0.7):** *In v0.7.x, γ is computed and surfaced but NOT applied to PWM commands. v0.8.0 enables the feed-forward path under RULE-COUPLING-03/04.*
- **RULE-COUPLING-09 (privacy):** *Coupling state is local to the host and never transmitted; it is included in `ventd doctor --redacted` output with channel names but without RPM/temp telemetry.*
- **RULE-COUPLING-10 (sparsity bound):** *S MUST have at most ⌈M·log M⌉ nonzero entries. If the detector flags more, ventd retains the top-|ρ| edges and discards the rest, emitting a warning.*

### 12.6 Doctor surface contract

```
$ ventd doctor coupling
Coupling groups discovered (1):
  group#1: { fan_intake_1, fan_intake_2, fan_hdd } (NAS chassis intake zone)
    member edges:
      fan_intake_1 ↔ fan_intake_2   strength=MODERATE   conf=HIGH   age=11h
      fan_intake_1 ↔ fan_hdd        strength=WEAK       conf=MEDIUM age=11h
      fan_intake_2 ↔ fan_hdd        strength=WEAK       conf=MEDIUM age=11h
    feed-forward: ENABLED (v0.8+)   |  read-only (v0.7)
    last calibration: 11h ago       |  next scheduled: 13h
  RECOMMENDATION: none

Singletons (independent channels, 2):
  fan_cpu, fan_gpu

Suppressed pairs (below detection threshold, 6): show with --verbose
```

### 12.7 HIL validation matrix

| Test | Host | Pass criterion |
|---|---|---|
| T1 facing-fan extreme coupling | Proxmox + 2× 120 mm fans | S_12=S_21=1, |γ|>0.5, group={1,2} within 4 h |
| T2 push-pull on D15 | 13900K with stacked Noctua | virtual-channel preset triggered OR group={1,2} with γ≈1 |
| T3 throttled-fan exogenous | Proxmox, taped intake | residual rises but γ does NOT spuriously inflate |
| T4 cross-zone false positive | Proxmox, partitioned ducts | S=0 for cross-zone pairs after 24 h |
| T5 Celeron CPU budget | MiniPC | detector run ≤ 5 ms, controller tick ≤ 1 ms |
| T6 FNM numerical regression | offline, simulation | |γ_estimated − γ_truth| / γ_truth < 0.20 across 100 random topologies |
| T7 deser/lock invariance | All hosts | corrupt `coupling/v1/` ⇒ controller falls back to SISO without crash |

### 12.8 Estimated CC implementation cost (Sonnet)

| PR | Scope | Est. Sonnet effort |
|---|---|---|
| #1 | Auto-discovery: Pearson, Granger F, connected components, persistence | 1 PR, ~600 LoC + tests |
| #2 | Doctor surface, RULE-COUPLING-06/09, redaction | 1 PR, ~250 LoC |
| #3 | HWCURVE-COUPLED Plackett-Burman calibration & online RLS γ update | 1 PR, ~450 LoC |
| #4 | Feed-forward decoupler with RULE-COUPLING-03/04 saturation interlock (v0.8.0 gated) | 1 PR, ~300 LoC |
| #5 | FNM numerical-regression test harness | 1 PR, ~400 LoC of tests |
| #6 | Push-pull preset (RULE-COUPLING-05) | 1 PR, ~200 LoC |

Total: **6 PRs, ~2.2 kLoC.** Spread across ~6 weeks of solo dev with $300/mo CC budget. PRs 1, 2, 5, 6 belong to v0.7.x; PRs 3, 4 belong to v0.8.0.

### 12.9 Spec target version

- **v0.7.x:** PRs #1, #2, #5, #6 (read-only, doctor-surface, FNM regression, push-pull preset). RULE-COUPLING-01, -02, -05, -06, -07, -08, -09, -10 active.
- **v0.8.0:** PRs #3, #4 (calibration extension, feed-forward decoupler, saturation interlock). RULE-COUPLING-03, -04 promoted from inactive to active.
- **v0.9.0+:** optional second-order γ extension if HIL data demands.

### 12.10 Conclusions actionable for `spec-vN_M_K-coupling.md`

1. The R17 architectural commitment is **per-pair static decoupling with auto-discovered sparse coupling groups**, not MIMO control. This is consistent with industrial practice for fan banks (HP DSC, Google AHU MPC, BACnet) and is the only choice consistent with the locked R1–R16 state and the Celeron / single-binary constraints.
2. Coupling detection runs **daily on R8-HIGH-confidence Layer-A residuals**, uses Pearson + bivariate Granger, and produces an undirected adjacency S whose connected components are the coupling groups.
3. Calibration is extended only for discovered groups, using a **Plackett-Burman Resolution-III fractional factorial** that completes in ≤ 80 s for M ≤ 6.
4. The control law correction is a **bounded (≤ 5 % PWM) feed-forward static decoupler**, suppressed at saturation, gated on R8 confidence, and only enabled in v0.8.0 (read-only in v0.7).
5. The **doctor surface is the v0.7 deliverable**; control-law change is the v0.8 deliverable. This phasing earns the right to alter control behaviour by first publishing the detector's findings to users.
6. Push-pull configurations (CPU_FAN + CPU_OPT pair with stable RPM ratio) get a **preset** that aggregates them into a virtual single channel rather than a coupling group, side-stepping the M=2 series case which decoupling does not capture well.
7. R17 must publish events that align with R16's anomaly taxonomy so that `coupling.saturation`, `coupling.recirculation_suspected`, and `coupling.group_changed` are first-class events.
8. RAM and CPU costs are negligible (<2 kB, <5 ms/day on Celeron). The binding constraint is **implementation discipline**, not resources.

---

*End of R17.*