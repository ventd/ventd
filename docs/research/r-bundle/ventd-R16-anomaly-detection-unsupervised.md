# R16 — Online Anomaly Detection for Autonomous Fan Controller Telemetry without Supervised Labels

**Status:** Research complete — design-of-record candidate
**Author:** ventd smart-mode research
**Predecessors:** R1–R15 (locked); especially R8 (confidence formula), R11 (sensor noise floor), R12 (Page-Hinkley on RLS residuals for Layer-C parameter drift), R13 (doctor surface contract)
**Successors:** spec-vN_M_K-anomaly-detection.md (target version proposed below)

---

## 0. Executive summary

R16 asks whether ventd can, using only the telemetry already exposed by Layers A/B/C and the passive observation log, detect *system-level* faults (bearing wear, dust occlusion, paste pump-out, AIO pump failure, ambient shift, stalled fan, miswired header) without any supervised label corpus. The answer is **yes, but with a strictly bounded vocabulary of detectors**: a stack of three lightweight tests — a Shewhart-style envelope check on (PWM, RPM, ΔT) residuals, a two-sided Page-Hinkley on the per-channel ΔT residual against the Layer-B prediction, and an EWMA-of-variance check on the RPM residual at near-constant PWM — together cover the eight failure modes in the brief at well under 1 KB/channel, sub-microsecond per-tick cost, and a host-wide false-alarm budget of <1/month achievable through known-distribution thresholding plus persistence gates.

The expensive, attractive options from the streaming-anomaly-detection literature (Half-Space Trees, incremental LOF, Mondrian forests, robust covariance) are **rejected** for v0.7.x on three independent grounds: RAM, the absence of vibration sensors (which is what the rotating-machinery PHM literature actually relies on), and the catastrophic-confounder problem (workload, ambient, preset changes look identical to faults in a model that does not exploit Layer-C's signature decomposition).

The dominant design constraint is not detector accuracy — it is **decomposition**: anomaly detection must run *inside* the (channel, signature) shard that Layer-C already maintains, so that a confounder which shifts the joint distribution does not contaminate the per-shard residual. This insight lifts cleanly from HVAC FDD literature (Chen et al., LBNL review; Mirnaghi & Haghighat 2020) and from condition-monitoring practice codified in ISO 13373-2 (signature analysis under known operating mode). It is, in fact, the same decomposition the HVAC community calls "operating-mode-aware FDD."

R16 ships as **v0.8.0**, after one minor release cycle of stabilising the Layer-A/B/C convergence telemetry shipped in v0.7.0. No new fields are required in the passive observation log; the existing schema (PWM, RPM, ΔT, controller_state, event_flags, signature_label) is sufficient. Doctor surfaces are split into one generic `RECOVER-ANOMALY-CHANNEL` plus four mode-flavoured advisory surfaces (`-STALL`, `-COUPLING-LOSS`, `-RPM-VARIANCE`, `-AMBIENT-SHIFT`). Implementation cost is estimated at one Sonnet PR of roughly 1,200–1,800 LoC including HIL fixtures.

---

## 1. Detector taxonomy

The streaming-anomaly literature is mature enough that a small number of algorithms recur in essentially every survey (Gama et al. 2014; Aggarwal 2017; the River and scikit-multiflow framework documentation; Aminikhanghahi & Cook 2017 on online change-point detection). The candidates listed in the brief partition cleanly into four families.

### 1.1 Sequential change-point tests (univariate-on-residual)

**CUSUM (Page 1954).** The cumulative-sum statistic Sₙ = max(0, Sₙ₋₁ + xₙ − μ₀ − k) crosses a threshold h when the post-change mean is shifted by ≥ k. Asymptotically optimal (Lorden 1971) under known pre/post-change distributions, with average run length (ARL₀) tightly controllable by h. Detection delay ≈ log(ARL₀)/(KL divergence). State: 1 float per side (two-sided variant: 2 floats). Cost: O(1)/tick. Suitability for non-stationary baselines: poor unless coupled with a slow baseline tracker; consumer-hardware ageing breaks the "known μ₀" assumption.

**Page-Hinkley test (Page 1954; Mouss et al. 2004; Gama 2010).** A specialisation of CUSUM with adaptive mean. Maintains mₙ = Σ(xᵢ − x̄ᵢ − δ) and Mₙ = min(m₁..mₙ); flags drift when mₙ − Mₙ > λ. Already in use in ventd at R12 for parameter drift on RLS residuals — reusing the same primitive at the system-anomaly layer is cheap and architecturally clean. State: 3 floats. Cost: O(1)/tick. Detection delay for a step of size Δ in a Gaussian residual with σ: ≈ λ/(Δ−δ) ticks. Sangraha360 and the river-ml documentation give the standard formulation; Bifet et al. (2010 MOA paper) characterise its empirical false-alarm behaviour on streams.

**Window-limited / Kernel CUSUM (Xie, Moustakides & Xie 2022; Flynn & Yoo 2019).** Generalises CUSUM to non-parametric pre/post distributions via MMD or sliding windows. Useful when residuals are non-Gaussian. RAM cost grows with window length; not appropriate at ventd's 1 KB/channel budget.

**Data-Adaptive Symmetric CUSUM (Ahad, Davenport & Xie 2022).** Symmetrises CUSUM so a single threshold catches both upward and downward shifts in mean *and* variance — directly relevant to ventd because bearing wear shows as a variance increase, while ambient shift is mostly a mean shift. Cost is identical to CUSUM. Worth reading; not necessarily worth shipping when two one-sided Page-Hinkley tests on signed residuals achieve the same outcome with code already on disk.

### 1.2 Smoothing / control-chart families

**EWMA on residuals (Roberts 1959).** zₙ = αxₙ + (1−α)zₙ₋₁; flag when |zₙ − μ| > Lσ_z. State: 1 float plus the threshold. Detects small persistent shifts cheaply; widely used in HVAC FDD (LBNL review, Chen et al. 2023) and notably in Shen et al.'s cooling-fan-bearing health indicator (PMC3574677), where comblet-filtered vibration RMS is smoothed with EWMA to track lubricant starvation. ARL behaviour is closed-form (Lucas & Saccucci 1990).

**Shewhart / z-score on (PWM, RPM, ΔT).** The simplest detector: flag any tick where any component lies outside a learned ±kσ envelope. False-alarm rate per tick = 2(1−Φ(k)). At k=4 and 0.5 Hz, expected false alarms ≈ 1 every 18 days per scalar — already at the host budget for one channel. Combining with an n-of-m persistence rule (e.g. 6 of last 30 ticks) drops the per-channel ARL₀ into months. State: μ, σ per dimension (≤ 24 bytes); cost O(1).

**Western Electric / Nelson rules (WECO 1956; Nelson 1984).** Eight zone-pattern rules over a Shewhart chart. Each rule has a known false-alarm rate; combined ARL₀ for the original four rules is ~91 (Champ & Woodall 1987), substantially worse than CUSUM/EWMA for sustained small shifts. WECO is excellent when humans read the charts; for an automated daemon they are dominated by EWMA + a single-point 3σ rule. Their main merit here is interpretability in the doctor output ("rule 5: 4 of 5 ticks > +1σ on RPM_residual"), which makes the doctor message human-debuggable.

### 1.3 Multivariate / model-based

**Isolation Forest / Half-Space Trees (Liu et al. 2008; Tan, Ting & Liu 2011).** HS-Trees are the canonical streaming one-class detector — an ensemble of random binary trees that partition the space into half-spaces and score points by mass. River and scikit-multiflow (Montiel et al. 2018; Tan et al.'s original IJCAI paper) show ~25 trees of depth 15 work well for moderate-dimensional streams. RAM: ~25 × 2¹⁵ × 8 B ≈ 6 MB if leaves store mass counters. Even with depth 8 (Aditya-go1 / Wetzig variants) we are at tens of KB per channel. **Rejected at the 1 KB/channel budget.** Worth revisiting if ventd ever ships a per-host (not per-channel) multivariate detector.

**Mondrian forests (Lakshminarayanan, Roy & Teh 2014).** Online tree ensemble with theoretically nicer Bayesian update properties. Same RAM problem; additionally adds GPL-incompatible licensing concerns in some implementations.

**Online LOF / ILOF / MILOF / DILOF / TADILOF (Pokrajac et al. 2007; Salehi et al. 2016 MILOF; Na et al. 2018 DILOF; Han et al. 2020 TADILOF; Hu et al. 2025 EILOF).** Density-based outlier detection that handles concept drift. ILOF and its descendants achieve competitive accuracy but require k-NN structures (KD-tree / cover tree). RAM is window×d×float plus index — kilobytes minimum, more typically tens of KB. Computational cost per update is O(k log W). Rejected for the same reasons as HS-Trees.

**Hoeffding trees / VFDT (Domingos & Hulten 2000).** Online classifier requiring labels; not relevant to label-free anomaly detection. Mentioned for completeness because they often co-appear in streaming-ML surveys.

**Robust covariance (MCD — Rousseeuw 1984; Stahel-Donoho — 1981).** Computes a robust mean and covariance, then flags Mahalanobis-distance outliers. The streaming variants (Hubert, Rousseeuw & Verdonck 2012; online MCD by Cator & Lopuhaä 2010) require O(d²) state and O(d²) per-update work. d=3 here so O(d²) = 9 floats, eminently affordable. **This is the only multivariate option that fits the budget.** However, robust covariance assumes elliptical normal-ish data; ventd's (PWM, RPM, ΔT) joint distribution is multimodal across signatures, so MCD must be run *inside* a signature shard (see §5).

**PCA reconstruction error (T² / Q statistics; Hotelling 1947; Jackson & Mudholkar 1979).** The workhorse of HVAC FDD (Mirnaghi & Haghighat 2020; the LBNL review; the 2024 ScienceDirect studies on light-commercial AFDD). With d=3 the PCA basis is a single eigen-decomposition; the SPE/Q statistic flags variance not captured by the dominant component. Equivalent in this dimension to robust-covariance Mahalanobis with a regularised covariance. Notable but not a separate algorithm at d=3.

### 1.4 Window-based change detectors

**ADWIN (Bifet & Gavaldà 2007; ADWIN-U for unsupervised, Springer 2025).** Maintains a variable-length sliding window of exponential histograms; cuts when the difference between sub-window means exceeds a Hoeffding-bound threshold. Memory O(log n), provable false-positive bound δ/n. Excellent for slow drift but poor for fast spikes; the steady-stream optimisation of Grulich et al. (EDBT 2018) shows it is also somewhat memory-heavy in practice (kilobytes per channel for long windows). Possible second-tier detector.

**KSWIN (Raab, Heusinger & Schleif 2020).** Kolmogorov-Smirnov windowing for distribution shift. Strong detection but requires storing two windows of samples; same RAM concern.

### 1.5 Comparative table

| Detector | RAM/ch | CPU/tick | False-alarm control | Non-stationary | Warm-startable | Verdict for ventd v0.8.0 |
|---|---|---|---|---|---|---|
| CUSUM (1-side) | 8 B | 1 add, 1 cmp | Excellent (closed form) | Needs slow μ tracker | Yes | Possible; redundant with PH |
| **Page-Hinkley** | 24 B | 4 ops | Good (empirical) | Moderate | **Yes (R12 reuses)** | **Adopt** |
| **EWMA** | 16 B | 2 ops | Good (closed form) | Excellent | Yes | **Adopt for variance track** |
| **Shewhart 3σ + n-of-m** | 24 B/dim | <10 ops | Tunable to ARL₀ ≥ 30 d | Poor | Yes | **Adopt as gate** |
| WECO 8-rule | 80 B | ~30 ops | Mixed (rule-dependent) | Poor | Yes | Use rule-2/5 only, not full set |
| HS-Trees | 6 MB+ | ~600 ops | Empirical | Good | No | **Reject (RAM)** |
| Mondrian forest | 1 MB+ | similar | Empirical | Good | No | Reject |
| Online LOF | 5–50 KB | O(k log W) | Empirical | Good | Partial | Reject (RAM) |
| Hoeffding tree | n/a (supervised) | — | — | — | — | N/A |
| Robust cov / MCD (d=3) | 72 B | ~50 ops | Closed form (χ²) | Moderate | Yes | **Adopt as optional Tier-2** |
| ADWIN | 0.5–2 KB | O(log n) | Provable | Excellent | Partial | Tier-2 only |

The chosen stack — Shewhart envelope, Page-Hinkley on residuals, EWMA on residual variance, optional MCD per-shard — fits inside ~256 B/channel, leaving headroom inside the 1 KB budget for shard bookkeeping.

---

## 2. Specific failure modes and their telemetry signatures

The brief lists eight modes. The classical condition-monitoring literature (ISO 13373-1:2002; ISO 13373-2:2016; ISO 17359:2018 on general procedures; Randall 2011 *Vibration-based Condition Monitoring*) is heavily vibration-centric. ventd has no accelerometer; the only window into rotating-machinery health is the tachometer pulse and the consequent thermal coupling. The PHM literature on cooling fans specifically (Oh, Azarian & Pecht 2011 — *Physics-of-failure analysis of cooling fans*; Shen et al. 2013 — *Health Assessment of Cooling Fan Bearings* PMC3574677; Drame et al. 2022 — automotive ECU fan PHM, PHM Society) addresses exactly this constraint and is therefore the relevant prior art.

### 2.1 Fan bearing degradation

**Mechanism.** Lubricant starvation → ball-race contact loss of damping → speed instability under steady drive. Shen et al. demonstrate this on accelerated-life fan-bearing tests: the pre-failure signature is a slow rise in the EWMA of vibration RMS, and a *less reliable but nonzero* signature in shaft-speed variance. Oh & Pecht's FMMEA notes that motor/bearing wear in BLDC fans manifests as commutation-noise-induced RPM jitter even before rotation-speed drop.

**ventd-observable signature.** At constant PWM in steady-state inside one signature shard, σ(RPM) increases monotonically over weeks–months while μ(RPM) stays close to the Layer-A curve. Detection statistic: EWMA of squared RPM residual against Layer-A predicted RPM, divided by the R11 noise-floor σ². Time scale: weeks (slow) to days (acute). Confounders: a new oily/dusty environment (transient), short-duration signature transitions.

### 2.2 Dust accumulation (blade)

**Mechanism.** Mass loading on the impeller plus aerodynamic disturbance. Net effect: at fixed PWM, achieved RPM drops modestly (5–15%) and current draw rises; airflow (which ventd cannot measure) drops more steeply.

**Signature.** Layer-A residual μ̂(RPM | PWM, sig) becomes negative-biased. Page-Hinkley on signed residual catches this in days–weeks. Confounder: bearing wear can also reduce RPM, but bearing wear does *not* shift the long-run mean; it shifts variance. So mean-shift PH and variance-EWMA discriminate.

### 2.3 Heatsink / radiator dust occlusion

**Mechanism.** Reduced airflow through fins → reduced thermal coupling β between fan output and CPU temperature.

**Signature.** Layer-B residual: at given (PWM, RPM, ambient) the realised ΔT exceeds the predicted ΔT by a sustained positive offset. PH on the Layer-B residual catches this on a horizon of days as long as the user occasionally exercises the workload signature. Confounder: thermal paste pump-out has the *same* sign on this residual. They are distinguished only by where the ΔT excess sits — pump-out shows on the sensor closest to the die; heatsink occlusion is uniform — and by whether airflow seems normal (RPM tracks Layer-A).

### 2.4 CPU thermal-paste pump-out

**Mechanism.** Differential CTE between IHS and cold-plate over hot-cold cycles extrudes paste laterally (Wikipedia *Thermal paste*; igorslab.de technical writeup; Tom's Hardware aggregated user reports). Time scale: months–years on stock paste, weeks on aggressive overclocks.

**Signature.** Same ΔT positive offset as §2.3 but *only* during high-power signatures (where the gradient drives flow). Layer-C signature decomposition discriminates: pump-out hits the high-load signature first while idle signatures look normal. Heatsink dust hits all signatures roughly proportionally.

### 2.5 AIO pump failure / coolant degradation

**Mechanism.** Pump RPM drop or coolant air-saturation reduces heat transport from cold-plate to radiator.

**Signature.** If ventd has the pump tach as a channel: direct stall/RPM drop visible. If only the radiator fans are wired through ventd: a sudden, large step in Layer-B residual (CPU package temp rises sharply at unchanged radiator-fan duty). PH catches this in tens of seconds because the magnitude of the step is large (Δ ≫ σ). Important: the failure mode for an AIO is bimodal — slow degradation (hard to distinguish from §2.3/§2.4) or abrupt failure (easy).

### 2.6 Heatsink dust occlusion

Same as §2.3 (collapsing the brief's "fan blade dust" and "heatsink dust" — the telemetry cannot distinguish them, only the corrective action can).

### 2.7 Ambient temperature shift

**Mechanism.** Room A/C failure or seasonal change shifts the equilibrium ΔT measurement upward.

**Signature.** *Cross-channel correlated* Layer-B residual: every fan's ΔT residual moves up together by approximately the same amount, *and* the chassis/board sensor (if available — see R6) confirms ambient rise. This is the ONLY mode in the list that affects all channels simultaneously and additively — exactly the kind of signal the cross-shard global detector is good at, and exactly the kind of signal a per-shard detector would mistakenly attribute to *every* fan failing at once.

### 2.8 Stalled fan

**Mechanism.** Fan completely stops rotating despite non-zero PWM — bearing seizure, blocked, or controller-side disconnection.

**Signature.** RPM = 0 (or below R11 stall floor) for ≥ N consecutive ticks at PWM > stall PWM. Trivial detector, deterministic, no statistical machinery needed; this is what every BMC and motherboard EC already does (OpenBMC `phosphor-fan-presence/sensor-monitor`; the Supermicro/Dell/HPE threshold sensors via IPMI/Redfish).

### 2.9 PWM connector reseated to wrong header

**Mechanism.** User physically moves the fan to a different header.

**Signature.** Sudden, *abrupt* change in the entire (PWM, RPM, ΔT) joint distribution: the Layer-A curve suddenly does not predict RPM, *and* the Layer-B coupling becomes invalid. Easiest discrimination: a Page-Hinkley on the RPM residual *and* ΔT residual *both* trip simultaneously within seconds, while no other channel's residuals move. The proper response is invalidate cache and recalibrate, not raise a hardware alarm.

### 2.10 Summary signature table

| Mode | Layer-A residual | Layer-B residual | RPM variance | Cross-channel? | Time scale | Discriminator |
|---|---|---|---|---|---|---|
| Bearing wear | ≈0 | ≈0 | ↑ (slow) | per-channel | weeks–months | EWMA-σ² |
| Blade dust | ↓ mean | small ↑ | small ↑ | per-channel | weeks | PH on RPM mean |
| Heatsink dust | ≈0 | ↑ uniform across sigs | ≈0 | per-channel | weeks | PH on ΔT, all sigs |
| Paste pump-out | ≈0 | ↑ on high-load sig only | ≈0 | per-channel | months | PH on ΔT, high-sig only |
| AIO pump fail (slow) | ≈0 | ↑↑ on high-load | ≈0 | one channel | days | PH on ΔT |
| AIO pump fail (abrupt) | ≈0 | ↑↑↑ step | ≈0 | one channel | seconds–minutes | PH (large Δ) |
| Ambient shift | ≈0 | ↑ on all channels equally | ≈0 | **all channels** | minutes–hours | global cross-shard PH |
| Stalled fan | n/a | ↑↑↑ | n/a (RPM=0) | one channel | ≤ R11 timeout | deterministic stall rule |
| Wrong header | ↑ step | ↑ step | ↑ step | one channel | seconds | joint-residual PH |

This table is the design contract for §6.

---

## 3. False-alarm budget

Target: ≤ 1 actionable false alarm per host per month. With ~3 channels and ~3 signatures the host runs ~9 shards plus 1 global detector; per-detector ARL₀ in ticks must be ≥ 30 d × 86400 s × 0.5 Hz / 10 ≈ 1.3 × 10⁵ ticks. That is achievable for closed-form detectors as follows:

- **Shewhart on Gaussian residual at threshold k=4** with persistence 6-of-30: per-tick FA = 2(1−Φ(4)) ≈ 6.3×10⁻⁵; persistence factor at random gives ARL₀ well above 10⁷ ticks per scalar dimension, so we have a ~10² safety factor over the requirement.
- **Page-Hinkley with λ chosen for ARL₀ = 10⁵** when residual is N(0, σ²): standard formula λ ≈ σ × √(2 log ARL₀) × (some factor); empirically Mouss et al. and the river docs recommend λ = 50 σ for moderate sensitivity. ventd should *measure* ARL₀ on each fleet member during HIL (§9) rather than rely on the closed form, because residuals are not exactly Gaussian.
- **EWMA-σ² with α=0.005 and L=4**: ARL₀ ≈ 5×10⁴ ticks (Lucas & Saccucci 1990 ARL tables for one-sided EWMA on variance). Adequate per-channel.
- **Combined (any-of-N detectors fires):** Bonferroni-bound the per-host rate; with N=10 detectors each at ARL₀ = 10⁶ we get a host ARL₀ of 10⁵ ticks ≈ 56 days. Within budget.

Two further tricks tighten the budget:

1. **Persistence gates.** Every detector requires its statistic to remain over threshold for a minimum dwell of M ticks (default M=20 ≈ 40 s) before raising. This eliminates virtually all transient outliers caused by sensor glitches (which R11 already characterises as bursty).

2. **Cooldown after a real or false alarm.** When a detector raises, suppress repeated raises for a cooldown (default 1 hour). This caps the *experienced* alarm rate even if the underlying detector is over-firing.

Together, the engineered ARL₀ at the user-visible level is dominated by the cooldown and is ≈ 30 d / (number of independent fault-like phenomena per month) — well within 1/month for healthy hosts.

---

## 4. Cold-start problem

The detectors all assume known μ, σ of residuals. ventd has no such priors at install time; calibration must run first. The discipline is:

> **Anomaly detection runs only when, for the (channel, signature) shard in question, R8 confidence ≥ τ_anom AND tick count in the shard ≥ N_min AND time since last Layer-C parameter-drift event > T_settle.**

Concretely, drawing from R8 (confidence formula) and R12 (parameter-drift event):

- `τ_anom = 0.7` (R8 confidence; tunable). This corresponds in R8's calibration to a covariance trace below the design target.
- `N_min = 600 ticks` (~ 20 minutes of in-shard data at 0.5 Hz).
- `T_settle = 24 h` after the last Page-Hinkley parameter-drift event in this shard. R12 already raises that event; R16 simply consumes it.

Below these gates the shard's anomaly detector emits nothing — not even a "low-confidence" signal — because we cannot meaningfully distinguish "anomaly" from "still learning." The doctor surface should report `anomaly: status=warming-up` rather than `unknown`, so the user can see calibration progress.

This gate is identical in structure to the "training mode" / "monitoring mode" split in classical SPC (Montgomery 2009 *Statistical Quality Control*, ch. 6) where Phase I establishes limits and Phase II enforces them, and to the warm-up window in HVAC FDD systems (Mirnaghi & Haghighat 2020). The novelty is only that R8/R12 already provide the "Phase I complete" signal cleanly.

---

## 5. Confounder discrimination

The single most important architectural decision in R16 is **per-shard versus host-global decomposition**. Layer-C in spec-smart-mode keeps RLS state θ̂ and covariance P per (channel, signature). The signature label is itself a soft-classification of workload, so a workload change → a signature transition → a new Layer-C shard, with its own θ̂. This means:

- A detector that runs *inside* the shard is automatically immune to workload change (it never sees data outside the signature it was trained on).
- A detector that runs *across* shards on the *raw* (PWM, RPM, ΔT) tuple will mistake a signature change for a fault every time. This is precisely the failure mode that the HVAC FDD literature identifies as "operating-mode dependence" and addresses by running detectors per-mode (Yan et al. 2014; Mirnaghi & Haghighat 2020 §3.3; Chen et al. 2023 LBNL).

So per-shard is the default. But ambient shift (§2.7) is a counter-example: it affects all shards equally and therefore *cannot* be detected by per-shard residual statistics that subtract a learned per-shard mean. The discrimination is:

> **Per-(channel, signature) detectors catch faults whose effect is signature-dependent. A per-channel cross-signature aggregator catches faults that affect a single channel uniformly across its signatures. A per-host cross-channel aggregator catches faults that affect all channels (ambient shift, A/C failure, OS thermal-throttle change).**

Three tiers, three scopes:

| Scope | Detector | Catches | Excludes |
|---|---|---|---|
| (channel, signature) | PH on Layer-A and Layer-B residual; EWMA-σ² on RPM | Bearing wear, blade dust, paste pump-out (per signature), wrong header | Workload change (already separated by signature label) |
| (channel, all-signatures) | Cross-shard mean of each per-shard residual; PH on that mean | Heatsink occlusion, blade dust uniform, AIO pump fail | Per-signature pump-out (already caught above) |
| host | Mean across channels of each Layer-B residual; PH on that | Ambient shift, A/C failure | Per-channel anything |

This structure is essentially the *Shi et al. (2017)* multi-level CUSUM hierarchy and lifts cleanly to ventd. The total detector count is (N_channels × N_sigs + N_channels + 1), which for a typical desktop (3 channels, 3 sigs) is 13 — well within the per-tick computational envelope at <2 µs/detector on a Celeron.

A subtler confounder: **user changes silent preset.** This is observable from the daemon's own controller_state log line; the detector should subscribe to the preset-change event and *reset* the per-shard PH statistics on it (since the operating point and possibly the target temperature change). R12 already has this primitive.

---

## 6. Doctor surface contract

R13 fixes that doctor surfaces are user-facing and that a `RECOVER-*` item must have an actionable next step. The decision space here is one big surface or many small ones. The recommended design:

**One generic surface, four mode-flavoured advisories.**

```
RECOVER-ANOMALY-CHANNEL[N]   severity={info, warn, critical}
  detector: "shewhart" | "page-hinkley" | "ewma-variance" | "stall"
  scope:    "shard" | "channel" | "host"
  signature: "<sig-label>"  # if scope=shard
  evidence: free-text rendering of which residual moved, by how much, since when
  hypothesis: one of {STALL, COUPLING-LOSS, RPM-VARIANCE, AMBIENT-SHIFT, RECONNECT, UNKNOWN}
  next_step: "run doctor depth-3" | "vacuum heatsink" | "reseat connector" | ...
```

The reasons for the design:

- A single surface keeps the doctor surface vocabulary small (R13 contract). Severity tier and `hypothesis` carry the differentiation.
- The `hypothesis` field is *advisory* — the daemon does NOT claim "your bearings are dying." It says "the RPM-variance detector tripped; bearing wear is one explanation, lubrication issue another; recommended action: schedule a fan inspection." This is the discipline that the HDD-SMART research community has had to learn the hard way (Murray et al. 2005 JMLR; Pinheiro et al. Google paper 2007; the Backblaze series): label-free anomaly detectors *predict deviance, not root cause*.
- The four-bucket mode flavouring matches the discriminator column of the §2.10 table. Adding granularity beyond that (separate paste-pump-out vs heatsink-dust surfaces) is unsafe because the telemetry cannot reliably discriminate them — it would imply ground truth ventd does not have.
- Interaction with the live-metrics surface: every anomaly surface entry references a corresponding event in the 24-h envelope-aborts ring, so the user can correlate. A single counter `anomaly_events_24h` on the live-metrics surface is sufficient.

`RECOVER-ANOMALY-WARMING-UP` is *not* a recovery item; it is a status surface. The doctor renders it with priority below all real recovery items.

---

## 7. Existing implementations — what does the field actually do?

A literature-and-source survey of existing fan/thermal controllers shows that R16 is genuinely novel territory in the open-source consumer space, and only modestly explored even in server-class firmware.

**OpenBMC `phosphor-fan-presence`** (github.com/openbmc/phosphor-fan-presence). The `sensor-monitor` and `fan-monitor` daemons monitor fan tachs against fixed YAML/JSON-defined thresholds; on threshold trip, an event log is written and (optionally) a power-off timer started after a configurable delay (`SHUTDOWN_ALARM_HARD_SHUTDOWN_DELAY_MS`, default 23 s). There is no learned baseline, no statistical detection, and no concept of "compared to history." The IBM Everest tracking issue (#2456 in ibm-openbmc/dev) explicitly contemplates only proportional control with floor/ceiling and per-fan error logging on stall. The Intel approach (per the Redfish thermal-status discussion in ibm-openbmc/dev #450) is to use D-Bus threshold interfaces for sensor health. **Conclusion: OpenBMC has only deterministic threshold rules.**

**Dell iDRAC / HPE iLO / Supermicro IPMI.** These expose sensor health and *threshold* events via Redfish/IPMI and integrate with their respective enterprise management platforms (OpenManage, OneView, SuperDoctor). Marketing materials (chandigarhmetro, paessler.com PRTG integration writeup, metanethosting comparison) reference "predictive analytics" but technical documentation reduces this to:
- threshold-based sensor monitoring,
- fan-power-derivative tracking (US Patent 7,142,125 "Fan monitoring for failure prediction" — IBM, 2006 — is the canonical reference),
- a USPTO patent (10,584,708) on sequential per-fan stop-and-test routines for redundant systems.

There is no published open algorithm comparable to R16's per-shard PH-on-residual approach.

**liquidctl** (github.com/liquidctl/liquidctl). Provides direct device control for AIOs and PSUs; explicitly out of scope for monitoring beyond pump/fan tach exposure. No anomaly detection.

**fancontrol / lm-sensors.** Static rules; no learning.

**Linux thermald** (github.com/intel/thermal_daemon; manpages.debian.org/unstable/thermald). Trip-point and ACPI-driven cooling devices. No fan health analytics — issues #49 and #211 in the upstream repo, plus the Ubuntu kernel-power-mgmt wiki page, confirm thermald's model is purely zone/trip-point driven.

**Windows FanControl by Rem0o.** Curve-based with optional offsets and mixes; no learned baseline, no anomaly detection, no health surface.

**SpeedFan.** Thresholds and curves; long-since-unmaintained.

**Server OEM "predictive failure" (PFA) on fans.** Most modern BMCs implement a "fan low" / "fan critical" / "fan failed" tri-state but not a learned anomaly detector. The only published consumer-grade work is the cooling-fan PHM literature cited in §2 (Shen et al. 2013; Oh & Pecht 2011; Drame et al. 2022) which uses **vibration sensors** that ventd does not have.

**Conclusion: ventd's R16 design — a learned-baseline, residual-based, per-shard online anomaly detector running in user-space on a single binary — has no direct precedent in the open-source fan-controller ecosystem. The closest analogues are the HVAC FDD literature (which inspired the per-mode decomposition) and the HDD SMART analytics literature (which inspired the unsupervised, Mahalanobis/IsolationForest-style scoring). This is the differentiator the brief describes.**

---

## 8. Privacy and observation log

ventd's passive observation log (spec-v0_5_4) currently exposes (per tick): PWM, RPM, ΔT, controller_state, event_flags, signature_label. This is sufficient.

R16 detector inputs:

1. PWM, RPM, ΔT — directly available.
2. Layer-A predicted RPM given (PWM, signature) — derived in-memory from Layer-A.
3. Layer-B predicted ΔT given (PWM, RPM, ambient if available, signature) — derived.
4. Residuals = observed − predicted — derived.
5. signature_label — already logged.
6. controller_state and event_flags — used for resetting detector state on preset change (see §5) and for excluding ticks during regulated transients (envelope-abort ticks must not feed the detector).

**No new fields are required.** No process names, /home access, or new identifying telemetry. AppArmor profile unchanged.

The detector *outputs* are new: per-shard counters (`anomaly_pages`, `anomaly_ewma_var_RPM_residual`) emitted into the existing live-metrics surface. These are aggregate scalars and do not constitute identifying information beyond what the current log already records.

A subtle consideration: the doctor's `evidence` free-text field could in principle leak workload information through signature labels. Since signature labels are already in the log, this is not a regression. The `next_step` strings are static templates and reveal nothing.

The privacy threat model in spec-smart-mode therefore continues to hold without amendment.

---

## 9. HIL validation strategy

Available fleet: Proxmox host (5800X + 3060), MiniPC (Celeron), Steam Deck, three laptops, 13900K. None are intentionally faulty. The test matrix:

| Fault mode | Fleet member | Method | Realism | Expected detector |
|---|---|---|---|---|
| Stalled fan | Proxmox, 13900K | Pull power on chassis fan during run | High (instant) | Stall rule + PH-Layer-A spike |
| Wrong header reseat | Proxmox, 13900K | Hot-swap fan to different header (post-calibration) | High | Joint PH (RPM + ΔT) |
| Blade dust | Steam Deck, laptops | Tape-occlude intake to ~30% | Moderate (only mass-loading, not aero) | PH on RPM mean |
| Heatsink dust | MiniPC, laptops | Cardboard restrictor over outlet for 2 h | Moderate | PH on ΔT, all sigs |
| Bearing wear | None | **Cannot be physically simulated short-term** | n/a | RPM variance EWMA — synthetic injection in unit test only |
| Paste pump-out | None | Cannot simulate without recreating CTE history | n/a | Synthetic injection only |
| AIO pump abrupt fail | (None has AIO) — recommend acquiring one cheap AIO for test rig | Disconnect pump SATA | High | PH-Layer-B large step |
| Ambient shift | All | Heat gun near intake (carefully, +5–10°C ambient for 30 min) or AC off in test room | High | Cross-channel global PH |

HIL coverage matrix conclusions:

- **Six of nine modes are testable on existing fleet.**
- **Bearing wear and paste pump-out cannot be physically reproduced on a development timescale.** They must be validated through (a) unit-test synthetic injection — corrupt RPM with σ-increasing process noise; corrupt ΔT with a slow upward ramp on the high-load signature only — and (b) post-deploy user reports.
- **AIO pump failure** requires acquiring a budget AIO ($60–80) for the test rig. This is recommended as a one-time expense before v0.8.0 cuts.
- The "wrong header" test is destructive of the calibration cache and should be run only on a dedicated test fleet member with a one-shot reset between runs.

The post-deploy user-report path needs a structured doctor-bundle: when a user reports "my fan went bad and ventd did not flag it" or "ventd flagged a problem and there was none," the doctor bundle should include the anomaly detector state, the last 7 days of residual histograms, and the per-shard θ̂ trajectory. R13's doctor depth-3 already captures most of this; R16 should add the EWMA-σ² history and the PH-statistic trajectory to depth-3 output.

---

## 10. Spec target version

Two arguments and a recommendation.

**Ship early (v0.7.0):** It's the differentiator that turns ventd from "learning controller" into "noticing controller." All competitive work (FanControl, fancontrol, liquidctl, even OpenBMC) stops at thresholds — even a basic R16 implementation would be category-leading. A solo developer finishes the spec faster while it is fresh.

**Ship late (v0.8.0 or later):** A noisy detector erodes user trust faster than no detector. Anomaly-detection thresholds must be tuned across hardware classes (server PWM fans behave differently from notebook blower fans differently from AIO pumps). Without a stable v0.7.0 baseline of Layer-A/B/C convergence telemetry, false-alarm tuning is a moving target. The HDD-SMART community spent ~15 years (Hughes 2002 → Murray 2005 → Backblaze 2013–) calibrating thresholds across drive families. ventd cannot afford a comparable false-alarm-on-launch experience.

**Recommendation: v0.8.0.** v0.7.0 should ship Layer-A/B/C convergence dashboards, the R8 confidence values exposed on the live-metrics surface, and the R12 parameter-drift events on the doctor surface. v0.7.x point releases tune those across the fleet. v0.8.0 then ships R16 detectors *gated by the v0.7.x-validated confidence machinery*, with conservative initial thresholds (k=4 Shewhart, λ=50σ PH, Tier-2 MCD/cross-shard *off* by default for the first two point releases, enabled by config).

This deferral is cheap (one minor release cycle) and dramatically reduces the chance of a launch-day false-alarm storm.

---

# Appendix — Spec-ready findings

## A. Algorithm choice with rationale

**Tier-1 (always on, v0.8.0):**

1. **Stall rule.** Deterministic: RPM < R11.stall_floor for ≥ 6 ticks at PWM > R11.stall_pwm → `RECOVER-ANOMALY-CHANNEL[N] severity=critical hypothesis=STALL`. Reason: trivially correct, matches OpenBMC behaviour, no statistics required.

2. **Shewhart envelope on Layer-A and Layer-B residuals, k=4, persistence 6-of-30.** Per-(channel, signature). Reason: closed-form ARL₀, smallest possible state, catches gross violations.

3. **Two-sided Page-Hinkley on Layer-A residual (RPM mean) and Layer-B residual (ΔT mean).** Per-(channel, signature). λ tuned per fleet member during HIL to ARL₀ ≥ 10⁵ ticks. Reason: detects sustained small shifts (dust, paste pump-out, bearing-wear early), reuses R12 implementation, 24 B/state.

4. **EWMA on squared Layer-A residual (RPM variance proxy), α=0.005, L=4.** Per-(channel, signature). Reason: catches bearing-wear signature even when RPM mean is unaffected; closed-form ARL.

5. **Cross-channel mean of Layer-B residuals + Page-Hinkley.** Host-scope. Reason: discriminates ambient shift from per-channel coupling loss.

**Tier-2 (opt-in, v0.8.x, behind config flag):**

6. **Per-(channel) cross-signature mean residual + PH.** Channel-scope. Catches heatsink occlusion as separate from per-signature paste pump-out.

7. **Online MCD / Mahalanobis on (PWM, RPM, ΔT) residuals.** Per-(channel, signature). 72 B state, χ²(3) threshold for tunable ARL. Catches multivariate anomalies that any univariate detector misses.

**Tier-3 (deferred, post-v0.8):**

8. **Half-Space Trees per host.** Only if RAM budget revisited.
9. **ADWIN-U on the host-scope ΔT residual mean.** As a slow-drift sanity check.

## B. Field additions to passive observation log

**None required.** Existing fields (PWM, RPM, ΔT, controller_state, event_flags, signature_label) are sufficient. Detector internal state lives in memory only and is not persisted to the log.

Detector-output additions to the *live-metrics* surface (already contemplated by R13, distinct from the observation log):
- `anomaly_events_24h: int` — count of `RECOVER-ANOMALY-*` events raised in the rolling 24-hour window.
- `anomaly_warming_up: bool` — true if any shard is below confidence/data gate.
- `anomaly_last_event_ts: int64` — timestamp of last detector raise.

## C. RULE-* binding sketches

```
RULE-ANOMALY-01-STALL
  pre:   PWM[N] > R11.stall_pwm AND RPM[N] < R11.stall_floor
         FOR 6 consecutive ticks
  post:  raise RECOVER-ANOMALY-CHANNEL[N], hypothesis=STALL, severity=critical
  note:  bypasses warm-up gate (always armed)

RULE-ANOMALY-02-RPM-MEAN-DRIFT
  pre:   shard(N, sig) confidence ≥ 0.7 AND ticks_in_shard ≥ 600
         AND (PH_+ on RPM_residual OR PH_- on RPM_residual) trips
         AND no controller_state preset change in last T_settle
  post:  raise RECOVER-ANOMALY-CHANNEL[N], hypothesis=RPM-VARIANCE,
         severity=warn, scope=shard, signature=sig

RULE-ANOMALY-03-DT-MEAN-DRIFT
  pre:   same gates as 02, on Layer-B residual ΔT
         AND signature_label has been observed ≥ N_min times in last 7 days
  post:  if drift seen on high-load sig only → hypothesis=COUPLING-LOSS (paste/AIO)
         if drift seen on all sigs of channel → hypothesis=COUPLING-LOSS (dust/heatsink)

RULE-ANOMALY-04-RPM-VARIANCE-RISE
  pre:   EWMA(RPM_residual²) > L · σ_R11²
         AND RPM mean residual within Shewhart envelope
  post:  raise hypothesis=RPM-VARIANCE (likely bearing wear), severity=info

RULE-ANOMALY-05-AMBIENT-SHIFT
  pre:   cross-channel mean of Layer-B residuals exceeds host-PH threshold
         AND |residual| approximately equal across channels (within 1.5σ)
  post:  raise host-scope RECOVER-ANOMALY-HOST, hypothesis=AMBIENT-SHIFT,
         severity=info

RULE-ANOMALY-06-RECONNECT
  pre:   PH on RPM_residual AND PH on ΔT_residual on same channel within 60 s
         AND magnitude of step > 5σ on both
  post:  raise hypothesis=RECONNECT, severity=warn,
         next_step="invalidate channel calibration and rerun"
```

All rules subject to global cooldown of 3600 s after any raise on the same channel.

## D. Doctor surface contract

```
RECOVER-ANOMALY-CHANNEL[N]      # per-channel
RECOVER-ANOMALY-HOST            # cross-channel (ambient)
status-ANOMALY-WARMING-UP[N,sig] # informational, not a recovery item

Each carries:
  severity ∈ {info, warn, critical}
  detector ∈ {stall, shewhart, page-hinkley, ewma-variance, mahalanobis}
  hypothesis ∈ {STALL, COUPLING-LOSS, RPM-VARIANCE, AMBIENT-SHIFT, RECONNECT, UNKNOWN}
  scope ∈ {shard, channel, host}
  evidence: structured (statistic name, value, threshold, ticks-over-threshold, since-when)
  next_step: static template string keyed by hypothesis
```

Rendered priority: critical > warn > info > status. Live-metrics surface shows `anomaly_events_24h`. Doctor depth-3 dumps PH/EWMA trajectories for the last 7 days per shard.

## E. Cold-start gate predicate

```
def anomaly_armed(channel, signature) -> bool:
    return (
        layer_c_confidence(channel, signature) >= 0.70  # R8 confidence
        and ticks_in_shard(channel, signature) >= 600
        and seconds_since_last_r12_drift_event(channel, signature) >= 86_400
        and not in_envelope_abort(channel)
        and not preset_change_in_last(60)
    )
```

Stall rule (RULE-ANOMALY-01) bypasses this gate.

## F. HIL validation matrix

| Fault | Method | Fleet | Detector | Acceptance |
|---|---|---|---|---|
| Stall | Pull fan power | Proxmox, 13900K | Stall rule | Raise within 12 s |
| Wrong header | Move connector | Proxmox | RECONNECT | Raise within 30 s |
| Blade dust | Tape intake 30% | Steam Deck, 2 laptops | RPM PH | Raise within 7 days simulated time |
| Heatsink dust | Outlet restrictor 2 h | MiniPC, 1 laptop | ΔT PH all-sigs | Raise within 6 h |
| AIO pump fail | Disconnect pump | Test rig (acquire AIO) | ΔT PH (large) | Raise within 3 min |
| Ambient shift | Heat gun / AC off | All | Host PH | Raise within 1 h |
| Bearing wear | Synthetic σ inject | Unit test | EWMA-σ² | Raise within 5,000 simulated ticks |
| Paste pump-out | Synthetic ramp on high-sig | Unit test | ΔT PH high-sig only | Raise within 14 simulated days |

False-alarm acceptance: < 1 raise per host per 30 days of unstressed operation across the fleet, measured during a 60-day pre-release soak.

## G. Estimated CC implementation cost

Single PR, Sonnet, target ~1,200–1,800 LoC:

- Detector primitives package (PH, EWMA, Shewhart, MCD-3): 300–400 LoC. Page-Hinkley primitive likely already exists from R12 — refactor into shared `detector` package.
- Per-shard, per-channel, host orchestrator: 200–300 LoC.
- RULE-ANOMALY-* bindings into existing rules engine: 100–150 LoC.
- Doctor surface rendering & evidence formatting: 150–200 LoC.
- Live-metrics counters and surfacing: 50–80 LoC.
- Unit tests with synthetic fault injection (bearing-wear, paste-pump-out): 200–300 LoC.
- HIL fixtures (connector swap detector, ambient simulator script): 100–200 LoC.
- Spec doc spec-v0_8_0-anomaly-detection.md: ~600 lines markdown.

Estimated CC budget: ~$80–120 of the monthly $300 — comfortably within budget for one PR with 2–3 review cycles.

## H. Spec target version

**v0.8.0.** v0.7.0 ships Layer-A/B/C convergence telemetry, R8 confidence on live-metrics, R12 parameter-drift events on doctor. v0.7.1–v0.7.3 tune those across the fleet. v0.8.0 introduces R16 with Tier-1 detectors enabled and Tier-2 detectors gated behind a config flag for opt-in operators. v0.8.1+ migrates Tier-2 to default-on after fleet evidence is collected.

## I. Conclusions actionable for spec-vN_M_K-anomaly-detection.md

1. Adopt the four-detector Tier-1 stack (stall, Shewhart, two-sided PH, EWMA-σ²) plus the host-scope cross-channel PH for ambient. Total state ≤ 256 B/channel.
2. Run all statistical detectors per (channel, signature) shard to inherit Layer-C's workload-aware decomposition; explicitly add a host-scope detector for ambient shift, the only fault that disappears under shard decomposition.
3. Gate every detector with R8 confidence ≥ 0.70 plus R12 settle and ticks-in-shard ≥ 600. Stall rule bypasses gate.
4. Single doctor surface family `RECOVER-ANOMALY-CHANNEL[N]` (+ `RECOVER-ANOMALY-HOST`) with `hypothesis` field as advisory differentiator. Do not claim root cause.
5. No new fields in the passive observation log. New live-metrics counters only.
6. False-alarm budget: design ARL₀ ≥ 10⁵ ticks/detector; persistence (6-of-30) and cooldown (1 h) hold experienced rate < 1/host/month.
7. HIL covers six of nine modes physically; bearing wear and paste pump-out are validated via unit-test synthetic injection plus post-deploy user-report bundle including 7-day residual histograms.
8. Ship as v0.8.0 after one v0.7.x stabilisation cycle; conservative initial thresholds (k=4 Shewhart, λ=50σ PH); Tier-2 multivariate (MCD) opt-in only.
9. Reuse R12 Page-Hinkley primitive — do not implement a separate copy. Refactor into a `pkg/detector` shared module before R16 lands.
10. Reference prior art: ISO 13373-1/-2 for the signature-analysis-under-known-mode framework; Shen et al. 2013 (PMC3574677) for the EWMA-on-bearing-health idea; Mirnaghi & Haghighat 2020 / LBNL Chen et al. for the per-mode FDD decomposition; Bifet & Gavaldà 2007 (ADWIN) and Page (1954) / Mouss et al. (2004) (PH) for the change-point primitives; Murray, Hughes & Kreutz-Delgado (JMLR 2005) and the Backblaze-trained anomaly literature for the discipline of label-free deviance reporting in consumer hardware. None of these references force GPL-incompatible code; all algorithms are implementable from scratch in Go from the published descriptions.

— *end of R16* —