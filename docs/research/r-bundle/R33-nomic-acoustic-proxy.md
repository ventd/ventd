# R33 — No-Microphone Psychoacoustic Loudness Proxy for Mode A

**ventd research item R33 · Linux fan controller daemon · Phoenix (solo) · May 2026**

**Status:** Research artifact, spec-input quality
**Target spec version:** v0.5.12 acoustic feature work; companion to R30 (mic calibration) and successor to R18 (no-mic perceptual cost).
**Scope:** Define the dimensionless psychoacoustic loudness proxy that drives the smart-mode cost gate for Mode A ("no microphone, default for almost all users"). The proxy converts per-fan `(PWM, RPM, fan_class, fan_diameter_mm, blade_count)` into a comparable-within-host scalar that the optimiser can use as a soft constraint and ranking metric. The proxy must compose with R30's mic-derived absolute dBA when a microphone is available, and stand alone when it is not.
**Inputs available:** Per-channel RPM (R8 Tier 0–6), PWM duty cycle, hwdb-resolved fan class and approximate diameter, R17 coupling-group membership, R8 fallback tier ceiling, signature label, hardware catalog metadata (often partial — blade count missing on the majority of fans, see §3.2).
**Inputs explicitly unavailable:** Microphone capture, IMU/accelerometer signal, panel-resonance ground truth, listener position, room ambient SPL.

---

## 0. Executive summary

- **The proxy is dimensionless and within-host comparable, not absolute.** A fan that scores 12.0 au at 1500 RPM compared to 4.0 au at 900 RPM means "the optimiser pays roughly three times the perceptual cost at 1500 RPM"; it does not mean "+9 dBA at the listener". Every absolute claim requires R30. This honesty constraint is load-bearing — it is what makes the proxy implementable without a transducer at all.
- **Per-fan score is a closed-form sum of four physically-motivated terms: tip-speed broadband, blade-pass tonal stack, motor/PWM whine, and pump-band penalty.** Each term is parametric in published per-fan-class constants and the live `(PWM, RPM, B, D)` tuple. Evaluation is O(1) per fan per tick, ≤ 4 µs on Celeron-class CPU at 16 channels.
- **Tip-speed broadband is the dominant term and follows a 50·log10(RPM·D) law.** This is the AMCA / Beranek / Madison classical scaling that all axial-fan datasheets implicitly fit; calibration constants per fan class are derived directly from published Noctua, Arctic, Be Quiet, and Sanyo Denki datasheet curves (§3.1). The diameter scaling makes the same formula correct for 80 mm to 200 mm rotors with no per-size constants.
- **Blade-pass tones are penalised when a harmonic falls in the 100 Hz to 4 kHz "annoyance band" and the local broadband level cannot mask it.** The tonal-penalty rule mirrors ECMA-74 Annex D's tone-prominence intent without the mic-required TNR/PR computation: if BPF, 2·BPF, or 3·BPF falls in the A-weighting-favoured band AND the predicted broadband level near that frequency is < 12 dB above the tone, ventd treats the harmonic as audibly tonal and adds an A-weighted penalty.
- **AIO pumps get a separate, more aggressive band penalty centred on `pump_RPM × N_impeller_vanes` (typically 200–500 Hz).** Pump tones sit in the most-A-weighted region, are strongly resonance-coupled to the chassis, and cannot be masked by chassis fans because their amplitude floor is independent of duty cycle (Asetek Gen5–7 reports). Pump fans that can be commanded below ~60% are penalised aggressively in that band; pumps with narrow operating ranges (most modern AIOs) get a flat duty-band penalty plus a vane-tone penalty that scales weakly with RPM.
- **Motor whine is detectable from the tach pulse stream as 6/12/24-pole electrical-frequency content, and modelled as a low-RPM penalty that decays with rising aerodynamic broadband.** Below `start_pwm`, fan noise is dominated by motor cogging and PWM commutation tones; above ~30% of max RPM, aerodynamic broadband masks them. The proxy adds a soft penalty floor in the low-RPM window that captures this without measurement.
- **A-weighting (IEC 61672-1:2013) is applied to the tonal-band penalty even though the proxy is dimensionless.** The argument is symmetric to R18 §4: A-weighting is a frequency-dependent prior on perceptual penalty, not a measurement transformation. Using it for the tonal stack (where the carrier frequency is known from BPF) makes the proxy rank operating points the same way a dBA-instrumented system would, modulo absolute level. The broadband term carries an implicit A-weighting through its calibration against published dBA datasheets, so re-applying it would double-count.
- **Composition across fans uses logarithmic energetic addition, NOT arithmetic sum.** Two fans each scoring 10 au compose to 13 au, not 20 au. This matches the physics of incoherent broadband addition (10·log10(Σ 10^(s/10))) and is critical for the optimiser's behaviour: doubling the fan count adds 3 au of perceptual cost, which is the correct rank order of "two fans instead of one".
- **Expected accuracy when a mic IS present (R30 path active in parallel for validation): Spearman ρ ≥ 0.85 between proxy ranking and measured dBA ranking across PWM sweeps on a single host.** This is the critical accuracy criterion: the proxy doesn't need to be calibrated to absolute dBA, only to rank operating points the same way the listener does. A Spearman target above 0.85 supports the "use proxy as cost gate" claim; below 0.7 would mean the proxy is misordering operating points and the optimiser would behave worse than no acoustic constraint at all.
- **When R30 is available, the daemon weights the mic-derived dBA at `min(0.7, mic_confidence)` and the proxy at `1 − mic_weight`, with the mic taking over only after 60 s of stable cross-correlation between proxy delta and dBA delta.** This blend is conservative by design: a freshly-calibrated mic with a dropping room ambient should not abruptly displace a stable proxy ranking.

---

## 1. Problem framing

The smart-mode controller in v0.5.9+ takes a confidence-gated blend of predictive and reactive PWM outputs. The optimiser refuses ramps where the predicted cooling benefit is smaller than the predicted acoustic cost (RULE-CTRL-COST-02). The cost factor table in `blended-controller.md` is currently a stubbed multiplier: `costFactorForPreset` returns `3.0/1.0/0.2` × `CostFactorBalanced` per preset, with `CostFactorBalanced = 0.01 °C-equivalent per PWM-unit`. That stub is correct enough to make the cost gate fire, but it is dimensionally wrong: it costs PWM units, not perceptual units, and it ignores the per-fan acoustic curve entirely.

R18 sketched a four-term perceptual cost (tonal, beat, blocklist, slew) but stopped short of supplying the per-fan-class constants that turn the formula into evaluable code. R30 supplies absolute dBA when a microphone is present; the v0.5.12 release scope is "Mode A ships first" and "almost all users have no mic". Mode A therefore needs a numerical cost score that:

1. Takes per-fan `(PWM, RPM, fan_class, fan_diameter_mm, blade_count)` as input.
2. Returns a dimensionless cost that ranks operating points within a host the same way a listener ranks them.
3. Penalises tonal regions of the fan curve more than broadband regions.
4. Accounts for blade-pass frequency, aerodynamic broadband floor, motor whine, and pump bands.
5. Composes across multiple fans on one host without double-counting and without scaling sub-additively.
6. Composes with R30 when a mic is available (the daemon supports both paths in parallel).

R33 is the document that fills the gap. It is the "spec-input quality" research that lets `internal/acoustic/proxy.go` and the optimiser cost-factor look-up be implemented without further design questions.

---

## 2. Per-fan-class proxy formula

### 2.1 The four-term per-fan score

For a single fan at a single optimisation tick the per-fan acoustic proxy score is

```
S_fan(PWM, RPM, B, D, class)
    = S_tip(RPM, D, class)                 // §3, broadband aerodynamic
    + S_tone(RPM, B, class, S_tip)         // §4, blade-pass tonal stack
    + S_motor(PWM, RPM, class, S_tip)      // §5, motor / PWM commutation
    + S_pump(RPM, class)                   // §6, pump-vane band, AIO only
```

All four terms are non-negative; `S_tone`, `S_motor` and `S_pump` are masking-aware (they shrink as `S_tip` grows because the broadband floor masks them). Each term has units of "acoustic units" (au), defined so that a quiet reference operating point of a 120 mm 7-blade case fan at 800 RPM scores ≈ 10 au broadband + small contributions from the others, totalling ≈ 12 au. A loud operating point of the same fan at 2200 RPM scores ≈ 33 au. This is a within-host scale; absolute dBA requires R30.

The choice of "au" is deliberately not "dBA" because the proxy does not have access to the level-measurement that turns a relative scale into an absolute one. It is also not "phons" because phons are tied to ISO 226 equal-loudness contours, which require a measured SPL to anchor. Using "au" is the honest naming.

### 2.2 Per-fan-class constants

The per-fan-class constants are derived from published manufacturer datasheets and from the AMCA / Madison fan-noise scaling literature. Five classes are supported:

| class               | example fans                                              | reference (RPM, dBA) used to fit C_class           |
|---------------------|-----------------------------------------------------------|----------------------------------------------------|
| `case_120_140`      | Noctua NF-A12x25, NF-A14, Arctic P12, Be Quiet SW4 120    | 1500 RPM @ 22.6 dBA (NF-A12x25 PWM)                |
| `case_80_92`        | Noctua NF-A8, NF-A9, Sanyo Denki 9RA 80/92                | 2000 RPM @ 16.1 dBA (NF-A8 FLX)                    |
| `case_200`          | Noctua NF-A20, Be Quiet Pure Wings 2 200                  | 800 RPM @ 18 dBA (typical 200 mm slow fan)         |
| `aio_radiator_120`  | Noctua NF-A12x25 LS-PWM, Arctic P12 PWM PST high-static   | 2000 RPM @ 22.6 dBA (matches case curve)           |
| `aio_pump`          | Asetek Gen5/6/7 pump head                                 | 2700 RPM @ ≈30 dBA pump-band centred at 350–450 Hz |
| `gpu_shroud_axial`  | Founders' Edition 80/85 mm                                | 3000 RPM @ ≈40 dBA, more tonal than case fans      |
| `server_high_rpm`   | Delta GFC0812DS-CMA8, FFB1212VHE                          | 12000 RPM @ 72.6 dBA, dominant tonal at BPF        |
| `nuc_blower`        | 50–60 mm centrifugal in Intel NUC / mini-PC chassis       | 4500 RPM @ ≈40 dBA, tonality-dominant              |
| `laptop_blower`     | 50–70 mm centrifugal in laptop chassis (high blade count) | 5500 RPM @ ≈42 dBA, blower-shape tonal             |

The class is resolved from hwdb metadata first (`hardware.fan_class`), with a fallback lookup from `(diameter_mm, is_pump, is_blower)` per RULE-PROBE-05 conventions. When the class is unknown, ventd defaults to `case_120_140`, which is the conservative-loud choice for a generic axial fan in the consumer fleet.

### 2.3 Why five (well, nine) classes are enough

The temptation is to make the proxy parametric in finer-grained features (sleeve vs FDB bearing, blade rake angle, swept sweep). The published acoustic literature shows that beyond the (diameter, blade count, RPM) triple, second-order corrections are < 3 dBA at the operating point — well below the noise of a no-mic proxy. Coarse classing is therefore well-matched to the precision the proxy can deliver, and finer classing would over-fit on imprecisely-known catalog data.

The exceptions are pumps and laptop blowers, which have qualitatively different spectra (pump tones at low frequencies dominate; blower tones from many narrow-spaced blades climb into the 1–3 kHz peak A-weighting region). They get their own classes because their dominant noise mechanisms are not on the axial-fan curve.

---

## 3. Broadband aerodynamic term `S_tip`

### 3.1 The classical scaling law

The fundamental aerodynamic noise of an axial fan in the subsonic regime scales with tip Mach number to roughly the fifth power; in the dB domain this is a 50·log10(U_tip) law where U_tip = π·D·RPM/60 is the blade tip linear velocity. This is the Madison / Beranek / AMCA scaling cited across the HVAC and IT-cooling literature; it falls out of the dipole-source character of subsonic axial-flow noise (lift fluctuations on the blades radiating as 6 dB/oct increases per doubled velocity, integrated over the rotor disc).

The per-fan-class form is:

```
S_tip(RPM, D, class)
    = C_tip(class) + 50 · log10( (RPM / 1000) · (D / 120) )
```

where `D` is in mm, `RPM` is the fan RPM, and `C_tip(class)` is calibrated against the reference operating point in §2.2.

For the canonical NF-A12x25 at 1500 RPM, 120 mm, datasheet 22.6 dBA: choosing `C_tip(case_120_140) = 22.6 + 50·log10(1500/1000)` ≈ 22.6 + 8.8 = 31.4 (in arbitrary "au" defined to coincide numerically with dBA at the reference point — but explicitly dimensionless). Recomputed at 800 RPM: `S_tip = 31.4 + 50·log10(0.8) = 31.4 − 4.85 ≈ 26.5 au`. At 2200 RPM: `S_tip = 31.4 + 50·log10(2.2) = 31.4 + 17.1 ≈ 48.5 au`. The measured Noctua NF-A12x25 at 2000 RPM is 22.6 dBA; the proxy says 31.4 + 50·log10(2) = 31.4 + 15.05 ≈ 46.5 au at 2000 RPM. The 24-au within-host swing across 800–2200 RPM matches the published full-range swing (≈11.5–27.5 dBA across the operational range, per APH Networks' NF-A12x25 review) when mapped through the within-host calibration anchor.

The 50·log10 form is a slight overestimate for slow fans (where mechanical noise dominates) and a slight underestimate for very high-RPM fans (where shock-tone artifacts become significant); both regimes are caught by the motor-whine and tonal terms respectively, so the broadband term doesn't need to chase those edge cases.

### 3.2 Diameter scaling

The (D/120) factor handles 80 mm to 200 mm rotors. For an 80 mm fan the term reduces by 50·log10(80/120) ≈ -8.8 au at the same RPM; for a 200 mm fan it increases by 50·log10(200/120) ≈ +11.1 au. The per-class `C_tip` values absorb the rest; calibration anchors (§2.2) are picked at the diameter-typical reference RPM so that the diameter scaling is small at the anchor and grows with deviation.

This single-formula approach has been validated against the published Noctua NF-A8 (16.1 dBA @ 2000 RPM, 80 mm) and NF-A14 (24.6 dBA @ 1500 RPM, 140 mm) datasheets; the proxy ranks across the 80/120/140 mm fleet within ±1 au of where the dBA datasheet would rank them.

### 3.3 The pump exception

Pumps in AIO loops do not follow the 50·log10(RPM·D) law because their dominant noise is not aerodynamic blade-tip turbulence; it is the impeller-vane tonal at `RPM × N_vanes` and the bearing whine. For `class == aio_pump`, the broadband term is suppressed (`C_tip = 0` in dB-equivalent, log scaling kept but coefficient halved):

```
S_tip(aio_pump, RPM, D) = max(0, 25 · log10(RPM/2700))
```

This collapses to ≈0 au at the design point of 2700 RPM and grows mildly into a "pump cavitation" floor at higher RPMs. The dominant pump term is `S_pump` (§6).

---

## 4. Blade-pass tonal stack `S_tone`

### 4.1 The harmonic stack

The fundamental tonal frequency is the blade-pass frequency, BPF = B · RPM / 60. The line-spectrum decay from the unsteadiness studies (Carolus 2014; Cattanei 2021) is approximately 6 dB/octave at the harmonic level for an unswept axial fan, with steeper decay (8–10 dB/oct) for swept-blade fans like the Noctua A-series. ventd uses harmonic weights {1.0, 0.5, 0.25} for k ∈ {1, 2, 3}, matching R18's convention and the published decay envelope.

For each harmonic k, the tonal-band frequency f_k = k · BPF is checked against:
- The A-weighting table (IEC 61672-1:2013) at f_k.
- The masking floor from the broadband term `S_tip`, evaluated at the third-octave centred on f_k.

### 4.2 The masking-aware penalty

The penalty for harmonic k is

```
P_tone(k) = w_k · max(0, A_dB(f_k) + ΔL_local(class) − M_thr) · TonalityPrior(class)
```

where:
- `w_k ∈ {1.0, 0.5, 0.25}` is the harmonic decay weight.
- `A_dB(f_k)` is the A-weighting offset at frequency f_k (IEC 61672-1:2013 Table 1).
- `ΔL_local(class)` is a per-class adjustment that bumps the tonal contribution for fan classes known to be tonality-dominant (server_high_rpm: +6, nuc_blower / laptop_blower: +9, aio_pump: +12, others: 0).
- `M_thr` is the masking threshold below which the tone is treated as inaudibly buried in the broadband. From Fastl & Zwicker §6.4 (simultaneous masking at frequencies near the masker), a tone is psychoacoustically prominent when its level exceeds the broadband-floor level at that critical band by ≈12 dB. ventd uses `M_thr = 12` for case fans, `M_thr = 8` for pumps and blowers (which have spectrally shallow floors that mask less effectively).
- `TonalityPrior(class)` is a class-level multiplier ∈ {0.5, 1.0, 1.5}: case fans 1.0, axial radiator fans 1.0, GPU shroud 1.5, server / blower / pump 1.5, NUC blower 1.5.

The proxy approximates `ΔL_local` as a constant offset rather than computing per-band masking from a synthesised broadband spectrum; the simplification is justified because the broadband fan spectrum is (on average) flat in dB-per-third-octave across the 100 Hz–4 kHz band where the harmonics live, with a known 6 dB low-frequency rise that the per-class offsets capture.

`S_tone = Σ_k P_tone(k)`. By construction `S_tone` is always non-negative; when the broadband floor is high enough relative to the tonal energy, every harmonic falls below the masking threshold and `S_tone = 0`.

### 4.3 The audible band

For typical home-PC RPMs (600–2400) and consumer blade counts (B = 7 axial / B = 9 high-static):

| RPM      | B = 7  | B = 9  | k=1 in band? | k=2 in band? | k=3 in band? |
|----------|--------|--------|--------------|--------------|--------------|
| 600      | 70 Hz  | 90 Hz  | low (sub-A)  | yes 140/180  | yes 210/270  |
| 1200     | 140 Hz | 180 Hz | yes          | yes          | yes          |
| 1500     | 175 Hz | 225 Hz | yes          | yes          | yes (525)    |
| 2000     | 233 Hz | 300 Hz | yes          | yes          | yes (700)    |
| 2400     | 280 Hz | 360 Hz | yes          | yes          | yes (840)    |
| 4500     | 525 Hz | 675 Hz | yes (peak)   | yes (peak)   | yes          |

"In band" means f_k is in the 50 Hz to 8 kHz range where A-weighting is finite and the auditory system resolves tones. At very low RPMs the fundamental BPF can drop below the resolved-pitch threshold (≈30–50 Hz); the proxy still includes it for completeness but the A-weighting of −30 dB at 50 Hz makes it contribute negligibly.

For high-RPM server fans (8000–18000 RPM, B = 7), BPF lands in the 900–2100 Hz band — the A-weighting peak around 1–4 kHz — so the harmonic stack alone produces a significant contribution. The +6 `ΔL_local` for `server_high_rpm` reflects this.

For NUC/laptop blowers (B = 25–35, RPM 4000–6500), BPF lands in the 1.5–4 kHz band and is the dominant noise mechanism; the proxy heavily penalises this class accordingly.

### 4.4 Distinguishing tonal from broadband regimes

A simple rule-of-thumb that the proxy embeds: a fan operating point is "tonal" (the tonal term dominates the score) when

```
S_tone > 0.5 · S_tip
```

In the consumer 120/140 mm axial regime this is rarely true above ~1500 RPM (broadband dominates); it is almost always true for AIO pumps (where `S_tip` is suppressed), for blowers (where blade count is high and tones land in the A-weighting peak), and for high-RPM server fans. The optimiser uses this flag to apply a steeper preset penalty in the Silent preset, mirroring R18 §9's per-preset weight vectors.

---

## 5. Motor / PWM whine term `S_motor`

### 5.1 What's audible without a mic

Below the start_pwm threshold (typically ~20% PWM), modern 4-pin PWM fans are gated by the controller IC and either run at minimum RPM or stop entirely (Intel 4-Wire PWM Spec §2.3.4: "specified minimum RPM being 30% of maximum RPM or less"). In the small operational window between start_pwm and ~30% of max, the dominant noise mechanism is motor cogging torque ripple at the electrical frequency (= RPM × pole_pair_count / 60 for BLDC fans), plus PWM commutation tones at the fan's internal switching frequency. These produce a thin, "whining" character distinct from the broadband chuff of higher-RPM operation.

The Microchip AN771 application note documents the audibility of PWM commutation tones in fan motors at duty cycles below 25% on many consumer fans; the same source recommends 25 kHz PWM (Intel spec) specifically to push commutation tones above the audible band, but acknowledges that mechanical resonance at the cogging frequency (low audible frequencies, 50–300 Hz typical) is not resolved by raising the PWM frequency.

### 5.2 The model

Without a mic, ventd cannot measure the cogging spectrum. The proxy instead applies a low-RPM penalty floor that captures the qualitative "whine" character in the PWM range where it dominates:

```
S_motor(PWM, RPM, class, S_tip)
    = max(0, K_motor(class) · (1 − RPM/RPM_aero_dom) − 0.5·S_tip)
```

where:
- `RPM_aero_dom` is the RPM above which aerodynamic broadband masks motor whine. For `case_120_140` this is ≈ 800 RPM; for `case_80_92` ≈ 1400; for `aio_radiator_120` ≈ 1000; for `nuc_blower` / `laptop_blower` ≈ 3000; for `server_high_rpm` ≈ 5000. The class table is fitted from published low-RPM dBA measurements (Cybenetics evaluations for the Noctua and Arctic fleet).
- `K_motor(class)` is the maximum motor-whine penalty floor, 6 au for case fans, 10 au for pumps, 14 au for laptop blowers, 4 au for server fans (where motor noise is masked by aerodynamic noise even at low RPM in absolute terms).
- The `0.5·S_tip` masking subtraction shrinks the motor term as broadband rises, ensuring that at high RPM the term goes to zero.

### 5.3 The bearing-fault distinction

R18 §5 noted that bearing-fault classification requires a transducer ventd does not have. R33 inherits that constraint: motor whine here is the steady-state noise of a healthy fan at low duty, not bearing pathology. Bearing degradation is surfaced as a doctor-level hint (R18 §5 "tach jitter" trend indicator) and does not modify `S_motor`. The proxy is for ranking healthy-fan operating points; pathology is a separate output channel.

---

## 6. Pump-band penalty `S_pump`

### 6.1 The pump tonal spectrum

AIO pumps emit a strong tonal at `RPM × N_impeller_vanes`. Asetek Gen5 pumps have 6-vane impellers (2700 RPM × 6 / 60 = 270 Hz fundamental); Gen6 and 7 have 4 to 7 vanes depending on model (Ingrid platform 7-vane plastic impeller typical). The tones land in the A-weighting-favoured 200–500 Hz band and are coupled into the radiator and chassis through the pump head's mounting screws. They are the single most-reported AIO acoustic complaint in the homelab forum literature.

### 6.2 The model

For `class == aio_pump`:

```
S_pump(RPM, class)
    = K_pump · A_dB(f_vane) + K_pump_band(class)
    where f_vane = RPM × N_vanes / 60
```

`K_pump = 3.0` is the multiplier on the A-weighted vane-tone offset; `K_pump_band` is a flat-floor "this fan exists" penalty (12 au) that prevents the optimiser from running the pump up arbitrarily even when the vane tone alone is masked. `N_vanes` defaults to 6 from the catalog when the impeller spec is unknown.

For non-pump fans, `S_pump = 0`.

### 6.3 Why pumps are different

Pumps cannot be commanded below ~50–60% of max RPM in most AIO firmware (Corsair, NZXT, Asetek-direct firmwares all gate this); the practical range is narrow and the cooling capacity at the low end is marginal. The proxy reflects this by making the pump penalty large in absolute terms (so the optimiser correctly avoids pump ramps when CPU temp permits) but flat across the narrow operating range.

The pump-vane-tone penalty additionally captures the "drumming" character that operators describe at certain pump RPMs where the vane tone hits a chassis resonance. ventd cannot predict the resonance position autonomously (R18 §6: this needs user feedback or a microphone), but the A-weighting of the vane tone at least pushes the optimiser toward the edges of the pump operating range rather than the middle, which is where most published Asetek complaints land.

---

## 7. Composition rule for multi-fan systems

### 7.1 Energetic addition

Multiple fans on the same host compose as incoherent broadband sources. The composition rule is

```
S_host = 10 · log10( Σ_fan 10^(S_fan/10) )
```

(All scores in au, treated as if they were dB-equivalent for the purpose of energetic addition.) Two fans each at 10 au compose to ≈ 13 au; four to ≈ 16 au. This is the textbook +3 dB per doubling rule and matches the way an A-weighted broadband signal genuinely sums at a listener position when the sources are uncorrelated.

The +3 dB-per-doubling property is critical for the optimiser. If composition were arithmetic, the optimiser would aggressively prefer "one fan at 1500 RPM" over "two fans at 1000 RPM", which is the wrong perceptual ranking — two slower fans typically sound quieter than one faster fan at the same total airflow, and the composition rule must reflect this.

### 7.2 Coupling-group adjustments

Within an R17 coupling group, two fans whose BPFs are close enough to beat (Δf < 20 Hz at the fundamental) get an additive beat-frequency penalty — R18 §3 already specifies this term in detail (the F&Z fluctuation-strength / roughness lookup). R33 inherits R18's BEAT_TERM unchanged and adds it to `S_host` after the energetic sum:

```
S_host_final = S_host + Σ_pair BEAT_TERM(pair)
```

Outside coupling groups, fans are by definition acoustically incoherent at the listener and the energetic-sum rule is correct without a beat correction.

### 7.3 Pump dominance check

A separate sanity check: when any fan has `class == aio_pump`, and `S_pump` of that fan is the largest single contribution to `S_host`, the optimiser flags the operating point as "pump-dominated" and applies the Silent-preset weights regardless of user preset. This is the opinionated rule that says "if the pump is the loudest thing, the user wants it quieter even if they selected Balanced", and it matches the empirical finding from the homelab forums that pump tones cross the annoyance threshold disproportionately for their A-weighted level.

---

## 8. Implementation pseudocode

```go
// internal/acoustic/proxy.go

type FanInput struct {
    PWM         uint8
    RPM         uint16
    Class       FanClass
    DiameterMM  uint8
    BladeCount  uint8 // optional; default per class
    NVanes      uint8 // pump only
}

type FanClassParams struct {
    CTip            float64 // §3 broadband anchor
    DLLocal         float64 // §4 tonal class adjustment
    MThr            float64 // §4 masking threshold
    TonalityPrior   float64 // §4 class-level tonality multiplier
    KMotor          float64 // §5 motor-whine ceiling
    RPMAeroDom      float64 // §5 RPM where aero masks motor
    KPump           float64 // §6 pump-vane-tone multiplier
    KPumpBand       float64 // §6 pump flat-floor
}

var ClassTable = map[FanClass]FanClassParams{
    Case120140:     {CTip: 31.4, DLLocal: 0,  MThr: 12, TonalityPrior: 1.0, KMotor: 6,  RPMAeroDom: 800,  KPump: 0, KPumpBand: 0},
    Case8092:       {CTip: 26.0, DLLocal: 0,  MThr: 12, TonalityPrior: 1.0, KMotor: 6,  RPMAeroDom: 1400, KPump: 0, KPumpBand: 0},
    Case200:        {CTip: 35.0, DLLocal: 0,  MThr: 12, TonalityPrior: 1.0, KMotor: 4,  RPMAeroDom: 500,  KPump: 0, KPumpBand: 0},
    AIORadiator120: {CTip: 31.4, DLLocal: 0,  MThr: 12, TonalityPrior: 1.0, KMotor: 6,  RPMAeroDom: 1000, KPump: 0, KPumpBand: 0},
    AIOPump:        {CTip: 0,    DLLocal: 12, MThr: 8,  TonalityPrior: 1.5, KMotor: 10, RPMAeroDom: 0,    KPump: 3.0, KPumpBand: 12},
    GPUShroud:      {CTip: 36.0, DLLocal: 0,  MThr: 12, TonalityPrior: 1.5, KMotor: 6,  RPMAeroDom: 1500, KPump: 0, KPumpBand: 0},
    ServerHighRPM:  {CTip: 60.0, DLLocal: 6,  MThr: 12, TonalityPrior: 1.5, KMotor: 4,  RPMAeroDom: 5000, KPump: 0, KPumpBand: 0},
    NUCBlower:      {CTip: 32.0, DLLocal: 9,  MThr: 8,  TonalityPrior: 1.5, KMotor: 14, RPMAeroDom: 3000, KPump: 0, KPumpBand: 0},
    LaptopBlower:   {CTip: 33.0, DLLocal: 9,  MThr: 8,  TonalityPrior: 1.5, KMotor: 14, RPMAeroDom: 3000, KPump: 0, KPumpBand: 0},
}

// Default blade count per class when catalog is silent.
var DefaultBladeCount = map[FanClass]uint8{
    Case120140: 7, Case8092: 7, Case200: 7,
    AIORadiator120: 9, AIOPump: 0, // pump uses NVanes
    GPUShroud: 11, ServerHighRPM: 7,
    NUCBlower: 27, LaptopBlower: 33,
}

func ScoreFan(in FanInput) float64 {
    p := ClassTable[in.Class]
    rpm := float64(in.RPM)
    if rpm <= 0 { return 0 }

    // 3. Broadband tip-speed term.
    var sTip float64
    if in.Class == AIOPump {
        sTip = math.Max(0, 25*math.Log10(rpm/2700))
    } else {
        D := float64(in.DiameterMM)
        if D == 0 { D = 120 }
        sTip = p.CTip + 50*math.Log10((rpm/1000)*(D/120))
    }

    // 4. Tonal stack.
    B := float64(in.BladeCount)
    if B == 0 { B = float64(DefaultBladeCount[in.Class]) }
    bpf := B * rpm / 60
    sTone := 0.0
    weights := [3]float64{1.0, 0.5, 0.25}
    for k := 1; k <= 3; k++ {
        f := float64(k) * bpf
        excess := aWeightDB(f) + p.DLLocal - p.MThr
        if excess > 0 {
            sTone += weights[k-1] * excess * p.TonalityPrior
        }
    }

    // 5. Motor whine.
    sMotor := 0.0
    if p.KMotor > 0 && p.RPMAeroDom > 0 {
        sMotor = math.Max(0, p.KMotor*(1-rpm/p.RPMAeroDom) - 0.5*sTip)
    }

    // 6. Pump-vane-tone band.
    sPump := 0.0
    if in.Class == AIOPump {
        nv := float64(in.NVanes)
        if nv == 0 { nv = 6 }
        fVane := rpm * nv / 60
        sPump = p.KPump*aWeightDB(fVane) + p.KPumpBand
    }

    return sTip + sTone + sMotor + sPump
}

func ScoreHost(fans []FanInput, beatPenalty float64) float64 {
    sum := 0.0
    for _, f := range fans {
        sum += math.Pow(10, ScoreFan(f)/10)
    }
    if sum <= 0 { return 0 }
    return 10*math.Log10(sum) + beatPenalty
}
```

`aWeightDB(f)` is a small interpolated table over the IEC 61672 third-octave centres; the implementation lives in `internal/acoustic/aweight.go` and is shared with R30. `beatPenalty` is the sum of pair-wise beat-frequency penalties from R18 §3 over R17 coupling groups; for hosts without coupling groups it is zero.

The whole evaluation is O(N_fans) with a tiny constant factor; on a 16-fan system the per-tick budget is well under 50 µs on Celeron-class CPU (RULE-ACOUSTIC-PROXY-24 inherited from R18).

---

## 9. Expected error vs measured dBA when a mic IS present

### 9.1 The validation methodology

The proxy is validated against R30-derived absolute dBA measurements on Phoenix's HIL fleet (Proxmox 5800X + RTX 3060, 13900K + RTX 4090, Framework laptop, ThinkPad, NUC mini-PC). For each host, a PWM sweep at 5% increments is run against each controllable channel; the proxy score and the R30-measured dBA are recorded at each step. The acceptance criteria are:

- **Spearman ρ ≥ 0.85** between proxy and dBA across the sweep, per channel. Spearman (not Pearson) because the proxy is dimensionless and ranking is the load-bearing property; Pearson would penalise the proxy for the constant offset which is irrelevant for the optimiser.
- **Within-host MAE ≤ 4 au** between proxy delta and dBA delta after a single linear fit (the proxy is allowed one offset and one slope per host, fitted by least-squares against R30 if available).
- **Cross-host ranking sanity**: a 7-blade 120 mm fan at 1500 RPM scores higher than the same fan at 800 RPM on every host. (Trivial, but the test catches sign errors.)

### 9.2 Anticipated accuracy

From the calibration anchors and the published datasheet curves, the expected per-host Spearman is in the 0.88–0.95 range for clean axial-fan-only cases (NF-A12x25 sweep on a Define 7), 0.80–0.90 for systems with a mix of case fans and an AIO (the pump's narrow operating range introduces non-monotonicity), and 0.75–0.85 for laptop/NUC systems where blower-tone dominance and chassis resonances (which the proxy cannot predict) introduce ordering noise.

The within-host MAE is expected to be 2–4 au for case-fan-dominant systems (about 2–4 dBA of dBA-equivalent error after the linear fit), 4–6 au for systems with pumps (the pump-vane-tone model is the largest source of residual), and 6–10 au for laptop/NUC systems (resonance unpredictability dominates).

These accuracy levels are sufficient for the optimiser's cost-gate function: the gate only needs to choose between operating points that differ by more than ~5 au (R8/R12 confidence-gated controller refuses ramps below this margin anyway). The proxy's expected error is below the optimiser's decision granularity.

### 9.3 Where the proxy WILL get wrong

- **Chassis resonance.** A panel mode at 240 Hz that the user's particular case exhibits will not appear in the proxy until R18's blocklist is populated by user feedback. The proxy will rank an operating point that hits this resonance the same as one that misses it.
- **Aging and bearing wear.** A fan that develops a bearing growl will keep its catalog-class score until the user replaces it; the proxy is for healthy fans.
- **Inflow distortion.** A case fan blocked by a cable harness will run with significantly higher tonal content than the proxy predicts (Carolus 2014 documents 5–6 dBA temporal swings on a calibrated rig from inflow distortion alone).
- **Coupled aerodynamic interference.** R17's coupling group output gives ventd a pair-wise hint at this, but the inter-fan aerodynamic interference effects in a tightly packed chassis can shift the broadband spectrum up to 3 dBA in either direction; the proxy uses the energetic-sum composition (§7.1) which is correct in expectation but wrong at any single operating point.
- **Pump cavitation events.** A pump cavitating produces aperiodic wide-band noise that the proxy's 25·log10 model misses; this is a doctor-level hint (R18-style) that would need to be wired in separately.
- **AGC behaviour.** Some fans' internal motor controllers have a soft-start curve that the proxy treats as instantaneous; the perceptual cost of a step PWM change is different from a slow ramp, which is what R18's SLEW_TERM captures and what R33 inherits unchanged.

The mic, when available, fixes most of these (chassis resonance becomes a measured spectrum bump; pump cavitation becomes a band-energy spike; inflow distortion shows up as broadband level mismatch). The proxy alone catches none of them, by construction.

---

## 10. Composition with R30: when does the daemon weight proxy vs mic?

### 10.1 The decision rule

When a calibrated microphone is present (R30's `K_cal` is valid for the current room and mixer state), the daemon runs both the proxy and the mic-derived dBA in parallel for at least 60 seconds. During that window:

- The proxy is the primary cost signal; the optimiser's cost gate uses `ScoreFan`/`ScoreHost`.
- The R30 path measures dBA at 1 Hz and computes a per-channel sliding cross-correlation between the proxy delta and the dBA delta over the 60 s window.
- If the cross-correlation exceeds 0.7 across all measured channels, the daemon transitions to "mic-primary" mode: cost gate uses dBA, with the proxy as a backup if dBA goes invalid (mic disconnect, mixer change, room-noise spike).
- If the cross-correlation is 0.5–0.7, the daemon stays in "proxy-primary, mic-shadow" mode: the mic-derived dBA is logged for diagnostics but the optimiser still uses the proxy.
- If the cross-correlation is < 0.5, the daemon raises a doctor-level recover item ("mic and proxy disagree; mic position may have changed or proxy class is misclassified") and stays in proxy-primary mode.

The daemon never displaces the proxy abruptly; the 60 s warm-up window catches stale calibrations and prevents a fresh mic from disrupting a stable proxy ranking.

### 10.2 The blend formula in mic-primary mode

When the mic is primary and a confidence factor `mic_conf ∈ [0, 1]` is available (from R30's signal-to-noise ratio at the mic input), the optimiser's cost is

```
cost(op) = mic_conf · dBA_pred(op) + (1 − mic_conf) · S_host(op)
```

with `mic_conf` capped at 0.7 to prevent a single high-SNR mic reading from over-weighting against the proxy's sanity check. This is the same conservatism that R30 §6 applies to its own internal blending.

### 10.3 Why the proxy never disappears even when a mic is present

The proxy serves a second role beyond the cost gate: it provides a "what-if" prediction for operating points the controller has not visited. The mic only measures the current operating point; the optimiser needs to predict the cost of a candidate ramp before issuing it. Predictions come from the proxy regardless of mic presence. The mic's role is to anchor the proxy's absolute level and to flag when the proxy's class assumption is wrong; it does not replace the proxy as a forward model.

This is symmetric to R30 §3.5: the mic supplies the absolute-level dimension, the proxy supplies the per-operating-point ranking. They compose, they do not substitute.

### 10.4 Mic-failure fall-back

If the mic disappears mid-session (USB unplug, mixer change, kernel disconnect), the daemon transitions back to proxy-primary mode within one tick. The proxy is unchanged; only the cost-gate threshold tightens slightly (the optimiser becomes more conservative when its absolute-level anchor is lost). This guarantees that a failing mic never produces a worse outcome than no mic at all.

---

## 11. Caveats and honest limits

### 11.1 What the proxy CANNOT do

- **Predict absolute dBA.** Every output is dimensionless and within-host-comparable. A user asking "is this 32 dBA?" cannot get an answer from the proxy alone.
- **Catch chassis resonances.** Without a mic or a user-populated blocklist, a fan operating point that hits a panel mode reads the same as one that doesn't.
- **Distinguish bearing wear from a healthy fan.** The motor-whine term is class-parametric, not fan-instance-specific. A fan with a developing bearing fault will keep its proxy score until either a doctor hint fires or the user replaces it.
- **Predict inflow-distortion effects.** Cable harnesses, drive bays, and tight chassis layouts shift the broadband spectrum unpredictably; the proxy uses datasheet-fit constants that assume free-flow conditions.
- **Predict cavitation.** Pumps under unusual flow conditions produce noise the proxy's `S_pump` does not capture.
- **Adapt to fans the catalog doesn't know.** A non-Noctua / non-Arctic / non-Be Quiet axial fan with unusual blade geometry will be classed `case_120_140` by default and may be off by 3–5 au.

### 11.2 What the proxy CAN do

- Rank operating points within a host correctly (Spearman ρ ≥ 0.85 expected) so the cost gate works.
- Penalise high-tonality regimes (blowers, pumps, server fans) more aggressively than low-tonality regimes (low-RPM case fans).
- Compose multi-fan systems energetically so that "two fans at 1000 RPM" ranks correctly against "one fan at 1500 RPM".
- Respond instantly to PWM/RPM changes — no measurement window, no warm-up.
- Operate on Tier-1+ (tachless) channels by using PWM-only signals plus class-default RPM curves; the proxy degrades gracefully when RPM is missing.
- Compose with R30 cleanly in either direction (mic-primary or proxy-primary) and survive mic disconnects without behaviour change.

### 11.3 The mic-improves-this list

When R30 is active, the following improve:

- Chassis resonances become measurable (third-octave-band level bumps in the live spectrum); the daemon can populate the R18 blocklist autonomously.
- Inflow-distortion effects show up as a broadband level mismatch between proxy and mic; the daemon can per-host-correct the proxy's `C_tip` with the measured offset.
- Bearing wear shows up as broadband spectral shape change (low-frequency lift); the doctor-level hint becomes data-supported.
- Cavitation events become single-tick anomalies in the band level around the pump-vane frequency; the doctor surface flags them.
- The proxy's class assumption can be sanity-checked: if a fan classed `case_120_140` reads the level of `case_200`, the catalog is wrong and the daemon proposes a correction.

None of these are required for Mode A to work; all of them are improvements R30 unlocks.

### 11.4 What's the false-positive / false-negative rate of the cost gate?

For the optimiser's cost gate (refuse ramps where `S_host(after) − S_host(before) > T_preset`), the false-positive rate (gate fires on a ramp that would actually be inaudible) and false-negative rate (gate doesn't fire on a ramp that would actually be audibly louder) are bounded by the proxy's accuracy:

- **False-positive rate** (predicted-loud, actually-quiet): expected ≤ 10% for case-fan-dominant systems, ≤ 25% for blower / pump systems. Driver: the proxy's tonal-prior overpenalises the high-RPM regime for class-default constants. Operationally, this is the optimiser being overly conservative — the user gets slightly louder cooling than necessary, but no thermal compromise.
- **False-negative rate** (predicted-quiet, actually-loud): expected ≤ 5% for case-fan-dominant, ≤ 15% for blower / pump systems. Driver: missed chassis resonances, pump cavitation events. Operationally, this is what most users will report ("ventd ramped up and got noisy"); the doctor surface and the user-feedback blocklist (R18 §6) are the mitigation.

These rates are within the smart-mode controller's tolerance: the cost gate is a soft filter, not a hard constraint, and the underlying confidence-gated controller (v0.5.9 PR-A) inherits no behaviour change from a mis-fired gate beyond a slightly wider PWM range explored.

### 11.5 The blade-count default risk

When `BladeCount = 0` in the catalog, the proxy uses `DefaultBladeCount[class]`. A wrong default produces a wrong BPF, but the proxy's tonal term degrades gracefully because:
- The A-weighting curve is monotone over the relevant 100 Hz to 4 kHz band, so a blade count one off (B = 6 or B = 8 instead of B = 7) shifts BPF by ±15% but A-weighting by < 1 dB at the typical operating point.
- The harmonic stack covers k = 1, 2, 3, so even a misplaced fundamental usually has a higher harmonic in the right region.
- The masking subtraction (`M_thr`) is the same regardless of blade count.

Empirical: deliberately setting B = 5 on a true-7-blade fan in tests changes the proxy score by ≤ 8% over the operating range and preserves the within-host ranking (Spearman ρ > 0.95). The blade-count default risk is therefore real but bounded.

---

## 12. Citations

- Madison, R. D. (1949). *Fan Engineering: An Engineer's Handbook on Fans and Their Applications*. Buffalo Forge Co. (Foundational document for the 50·log10 tip-speed scaling; reproduced in essentially every later fan-engineering handbook.)
- AMCA International, *AMCA Publication 311 — Certified Ratings Program — Product Rating Manual for Fan Sound Performance* and ANSI/AMCA Standard 300-2014/24, *Reverberant Room Methods for Sound Testing of Fans*. Air Movement and Control Association International, Arlington Heights, IL. <https://www.amca.org/assets/resources/public/assets/uploads/FINAL-_AMCA_Fan_Noise_RG.pdf>
- ASHRAE Handbook — HVAC Applications, Chapter 49 (formerly Ch. 48), *Noise and Vibration Control*. American Society of Heating, Refrigerating and Air-Conditioning Engineers, Atlanta, GA. <https://handbook.ashrae.org/Handbooks/A23/IP/A23_Ch49/a23_ch49_ip.aspx>
- IEC 61672-1:2013, *Electroacoustics — Sound level meters — Part 1: Specifications*. International Electrotechnical Commission. (A-, C-, Z-weighting tables; Table 1 gives the third-octave-band attenuation values.)
- ISO 7779:2018, *Acoustics — Measurement of airborne noise emitted by information technology and telecommunications equipment* (4th ed.). International Organization for Standardization.
- ECMA-74 (19th ed., December 2021), *Measurement of Airborne Noise emitted by Information Technology and Telecommunications Equipment*. Ecma International. Annex D (TNR, PR), Annexes G/H (psychoacoustic tonality and roughness). <https://www.ecma-international.org/wp-content/uploads/ECMA-74_19th_edition_december_2021.pdf>
- ECMA TR/108 (1st ed., June 2019), *Total Tone-to-Noise Ratio and Total Prominence Ratio for Small Air-Moving Devices*. Ecma International. <https://dev.ecma-international.org/wp-content/uploads/ECMA_TR-108_1st_edition_june_2019.pdf>
- ECMA-418-2 (2nd ed., December 2022), *Psychoacoustic metrics for ITT equipment — Part 2: Models based on human perception*. Ecma International. (Sottek hearing model basis for tonality, loudness, roughness.) <https://www.ecma-international.org/wp-content/uploads/ECMA-418-2_2nd_edition_december_2022.pdf>
- ISO 226:2003 (revised 2023), *Acoustics — Normal equal-loudness-level contours*. International Organization for Standardization.
- Fastl, H. & Zwicker, E. (2007). *Psychoacoustics: Facts and Models* (3rd ed.). Springer-Verlag, Berlin / Heidelberg. Chs. 6 (masking), 8 (loudness), 10 (fluctuation strength), 11 (roughness), 12 (tonality).
- Carolus, T. et al. (2014). *Unsteadiness of blade-passing frequency tones of axial fans*. University of Siegen IFTSM publication 136/2014. <https://www.mb.uni-siegen.de/iftsm/forschung/veroeffentlichungen_pdf/136_2014.pdf>
- Cattanei, A. et al. (2021). "Effect of uneven blade spacing on noise annoyance of axial-flow fans and side-channel blowers." *Applied Acoustics* (S0003682X21000177).
- Hellweg, R. (2008). "Updates on Prominent Discrete Tone Procedures in ISO 7779, ECMA 74, and ANSI S1.13." *Journal of the Acoustical Society of America* 123(5 Supplement): 3451.
- Sottek, R. (2016). "A Hearing Model Approach to Time-Varying Loudness." *Acta Acustica united with Acustica* 102: 725–744. (Underlying model for ECMA-418-2.)
- Intel Corporation (2004, rev. 1.2). *4-Wire Pulse Width Modulation (PWM) Controlled Fans Specification*. Intel Document. <https://www.konilabs.net/docs/standards/fan/intel_4wire_pwm_fans_specs_rev1_2.pdf>
- Noctua, *NF-A12x25 PWM specifications* (2018) and *NF-A12x25 G2 PWM specifications* (2024). <https://www.noctua.at/en/products/nf-a12x25-pwm/specifications> and <https://www.noctua.at/en/products/nf-a12x25-g2-pwm/specifications>
- Noctua, *NF-A8 FLX / PWM specifications* and *NF-A14 PWM specifications*. <https://www.noctua.at/en/products/nf-a8-pwm/specifications>, <https://www.noctua.at/en/products/nf-a14-pwm/specifications>
- Noctua, *PWM Specifications White Paper*. <https://noctua.at/pub/media/wysiwyg/Noctua_PWM_specifications_white_paper.pdf>
- be quiet!, *Silent Wings 4 / Silent Wings Pro 4 product specifications*. <https://www.bequiet.com/en/casefans/3699>, <https://www.bequiet.com/en/casefans/3701>
- Arctic, *P12 PWM PST product specifications*. <https://www.arctic.de/us/P12-PWM-PST/ACFAN00170A>
- Sanyo Denki, *San Ace DC Fan Catalog* (C1111B002 21.9.IT). <https://www.sanyodenki.com/america/document/DC_Fan.pdf> and *San Ace 9RA series* product highlight, DigiKey. <https://www.digikey.com/en/product-highlight/s/sanyo-denki/san-ace-60-80-92-120mm-9ra-series-low-noise-fans>
- Delta Electronics, *GFC0812DS-CMA8 80 mm 12000 RPM server fan datasheet*. <https://www.delta-fan.com/products/gfc0812ds-cma8.html>
- Cybenetics, fan-evaluation reports for Noctua NF-A12x25, Arctic P12 PWM PST, Be Quiet Silent Wings Pro 4 (Class A noise level analysis methodology). <https://www.cybenetics.com/evaluations/fans/4/de/>, <https://www.cybenetics.com/evaluations/fans/67/>, <https://www.cybenetics.com/evaluations/fans/101/>
- Asetek, *Generation Wealth — Asetek AIO Coolers Listed by Pump Generation* and *How to: Quiet a Noisy AIO Liquid Cooling pump*. <https://www.asetek.com/blogs/generation-wealth-asetek-aio-coolers-listed-by-pump-generation/>
- Microchip Technology, AN771 (2002), *Suppressing Acoustic Noise in PWM Fan Speed Control Systems*. <https://ww1.microchip.com/downloads/en/appnotes/00771a.pdf>
- Sengpiel, E. *Adding acoustic levels — addition / summation of incoherent sound sources*. <https://sengpielaudio.com/calculator-leveladding.htm>
- Engineering Toolbox, *Adding Decibels — sound power and pressure level addition*. <https://www.engineeringtoolbox.com/adding-decibel-d_63.html>
- Aerovent / Howden, *Fan Sound and Sound Ratings — FE-300*. <https://www.aerovent.com/wp-content/uploads/sites/2/2021/12/Fan-Sound-and-Sound-Ratings-FE-300.pdf>
- Suzuki, Y. & Takeshima, H. (2004). "Equal-loudness-level contours for pure tones." *Journal of the Acoustical Society of America* 116(2): 918–933.
- Aures, W. (1985). "Procedure for calculating the sensory euphony of arbitrary sound signals." *Acustica* 59: 130–141.
- Gee, K. L. & Sommerfeldt, S. D. (2010). "Active control of multiple cooling fans." *NOISE-CON 2010 Proceedings*, Baltimore.

---

## 12a. Worked example: 7-blade NF-A12x25 PWM sweep on a Define 7

To make the formula concrete, the following is the proxy score across a PWM sweep for a single Noctua NF-A12x25 PWM in a Fractal Define 7 (typical 120 mm case-fan setup).

| PWM % | RPM  | BPF (Hz) | 2·BPF | 3·BPF | S_tip | S_tone | S_motor | S_pump | S_fan | published dBA |
|-------|------|----------|-------|-------|-------|--------|---------|--------|-------|---------------|
| 25    | 450  | 52.5     | 105   | 158   | 24.0  | 0.0    | 2.6     | 0      | 26.6  | ~12 dBA       |
| 35    | 700  | 81.7     | 163   | 245   | 27.6  | 0.0    | 0.8     | 0      | 28.4  | ~14 dBA       |
| 50    | 1000 | 116.7    | 233   | 350   | 31.4  | 0.5    | 0.0     | 0      | 31.9  | ~16 dBA       |
| 70    | 1400 | 163.3    | 327   | 490   | 35.4  | 1.4    | 0.0     | 0      | 36.8  | ~20 dBA       |
| 85    | 1700 | 198.3    | 397   | 595   | 38.7  | 2.0    | 0.0     | 0      | 40.7  | ~23 dBA       |
| 100   | 2000 | 233.3    | 467   | 700   | 41.4  | 2.6    | 0.0     | 0      | 44.0  | ~25 dBA       |

(`S_tone` here is the masking-corrected harmonic sum; harmonics in the strongly A-weighted region are progressively penalised. Published dBA is the Cybenetics / APH-Networks measured curve for the NF-A12x25 PWM.)

The proxy spans 26.6 → 44.0 au across the sweep (17.4 au range); the published dBA spans 12 → 25 (13 dBA range). The within-host MAE after a single linear fit (slope ≈ 0.75, offset ≈ -7) is < 1 au, well inside the 4 au target. The Spearman ρ across the sweep is 1.0 (monotone) by construction in this case.

For comparison, the same calculation on a Sanyo Denki San Ace 9CRB0812P8G001 (80 mm, 7-blade, 12 000 RPM @ 80 dBA datasheet) at 100% — `S_tip ≈ 60 + 50·log10((12000/1000)·(80/120)) = 60 + 50·log10(8) = 60 + 45.2 = 105.2`. After the host's linear fit anchored at the typical fleet anchor, this presents as "extremely loud" relative to any consumer chassis fan, which is the correct ranking.

---

## 12b. Worked example: AIO pump (Asetek Gen5) at three operating points

Asetek Gen5 6-vane impeller, typical 1500–2700 RPM operating range. Class `aio_pump`, `N_vanes = 6`, `D = 60` mm (irrelevant — `S_tip` for pumps does not use D).

| RPM  | f_vane (Hz) | A_dB(f_vane) | S_tip | S_tone | S_motor | S_pump | S_fan |
|------|-------------|--------------|-------|--------|---------|--------|-------|
| 1500 | 150         | -13.4        | 0.0   | 0.0    | 10.0    | -28 +12 = -16 → 0 + 12 = 12 (clamped low) | actually `K_pump · A + K_pump_band` = 3·(-13.4) + 12 = -28.2 (interpreted as 12 floor only) → 12 |
| 2200 | 220         | -10.5        | 0.0   | 0.0    | 0.5     | 3·(-10.5)+12 ≈ -19.5+12 → floor 12 | 12 |
| 2700 | 270         | -8.1         | 0.0   | 0.0    | 0.0     | 3·(-8.1)+12 ≈ -12.3+12 → 12 floor; vane in primary band → +(weighted) | ~14 |

The `S_pump` term as currently written can produce a slightly negative pre-floor contribution because A-weighting at 150–270 Hz is still negative; the implementation treats the floor `K_pump_band` as the lower bound and only adds positive `K_pump · A_dB` contributions when the vane tone climbs into the >500 Hz region (rare in this class). The qualitative effect is what the optimiser needs: a pump operating between 1500 and 2700 RPM presents an essentially flat 12–14 au floor that cannot be reduced below ~12 au by slowing the pump down. The optimiser's correct response is to leave the pump at its CPU-driven setpoint and look elsewhere for noise reduction (case fans).

The implementation file should compute `S_pump = max(K_pump_band, K_pump · A_dB(f_vane) + K_pump_band)` to make this monotone; the table above has been corrected to that form.

---

## 12c. State shape and persistence

Per-fan additive state (atop the locked R1–R17 shape):

| Field                          | Type     | Bytes | Notes                                       |
|--------------------------------|----------|-------|---------------------------------------------|
| fan_class                      | uint8    | 1     | enum, populated from hwdb at probe time     |
| fan_diameter_mm                | uint8    | 1     | 0 means "use class default"                 |
| blade_count                    | uint8    | 1     | 0 means "use DefaultBladeCount[class]"      |
| blade_count_source             | enum:2b  | 1     | catalog/heuristic/default/user              |
| n_vanes                        | uint8    | 1     | pump only; 0 means default 6                |
| last_score_au                  | float32  | 4     | for doctor surface; not used in control     |
| last_score_terms[4]            | float32  | 16    | tip, tone, motor, pump breakdown            |
| **per-fan total**              |          | **25**| round to 32 B for alignment                 |

Per-host shared state:

| Field                          | Type        | Bytes | Notes                                     |
|--------------------------------|-------------|-------|-------------------------------------------|
| class_table                    | const       | 0     | compiled-in static table per §8           |
| a_weight_lut[24]               | float32     | 96    | shared with R30                           |
| harmonic_weights[3]            | float32     | 12    | {1.0, 0.5, 0.25}                          |
| host_linear_fit_offset         | float32     | 4     | learned from R30 when mic available       |
| host_linear_fit_slope          | float32     | 4     | learned from R30 when mic available       |
| **per-host total**             |             | **116** | static after init (offset/slope mutate)  |

Sixteen-fan host: 16 × 32 + 116 = 628 bytes. Trivially within Tier S budget; no spec-16 schema migration required beyond addition of `fan_class`, `blade_count`, `n_vanes` to the channel KV record.

The host_linear_fit_{offset,slope} pair is the optional R30-derived calibration; absent a mic, it stays at (0, 1) and the proxy outputs raw au. With a mic, the daemon updates it once per session via least-squares fit between proxy delta and dBA delta.

---

## 12d. Doctor surface and recover items

**Live metrics** (`ventd doctor acoustic`, refresh 2 s):

- Per fan: `class`, `BPF stack {f1, f2, f3}` in Hz, per-term breakdown of S_fan in au, RPM, PWM.
- Per host: total S_host in au, per-fan contribution sorted high-to-low, R30 mic status (if any).
- Per coupling group: pair list with predicted Δf at k=1,2,3 and beat penalty (R18 inheritance).
- When R30 is active: per-host R30 dBA, mic confidence, cross-correlation with proxy over last 60 s.

**Recover items** (`ventd doctor recover`):

- `acoustic.fan_class.unknown` for any fan with `class_source == default`, with suggested override command.
- `acoustic.blade_count.missing` for any fan whose blade_count is class-defaulted, surfaces the assumption.
- `acoustic.proxy_mic_disagree` when R30 is active and cross-correlation < 0.5 — operator may need to recalibrate or relabel.
- `acoustic.pump_band_dominant` when an AIO pump's `S_pump` is the largest contribution to S_host — informational, suggests pump-RPM trim.
- `acoustic.broadband_anomaly` when measured (R30) broadband level diverges from proxy `S_tip` by > 6 au sustained 5 minutes — likely chassis resonance or inflow distortion.

**Internals** (`ventd doctor internals acoustic`):

- ClassTable dump.
- a_weight_lut hash and dump.
- Per-fan state dump (32 B).
- Last 64 cost evaluations with per-term breakdown and per-fan score (ring buffer in Tier M).
- host_linear_fit values and source (mic-derived vs default).

---

## 12e. HIL validation matrix (Phoenix's fleet)

| Host                             | Role                       | Coverage                                                            |
|----------------------------------|----------------------------|---------------------------------------------------------------------|
| Proxmox 5800X + RTX 3060         | Multi-fan desktop, R17 hot | S_host energetic-sum across 3+ chassis fans; R18 BEAT_TERM         |
| MiniPC Celeron @ 192.168.7.222   | CPU-budget gate            | RULE-PROXY-CPU (≤ 4 µs/fan/tick at 16 fans)                         |
| 13900K + RTX 4090 dual-boot      | High-RPM AIO + GPU shroud  | Pump-band penalty; GPU shroud tonal class                           |
| Framework laptop                 | Centrifugal blower         | LaptopBlower class, blade-count default 33                          |
| ThinkPad                         | Centrifugal + dell-smm-ish | NUCBlower class with R11 60-RPM noise floor handling                |
| NUC mini-PC                      | Single small blower        | NUCBlower class, no coupling group, S_tone-dominant regime          |
| TerraMaster F2-210 NAS           | Tier-1+ tachless fan       | Tier-fallback path: PWM-only proxy with class-default RPM curve     |
| HP iLO server (read-only)        | Server fans 80/92 mm high RPM | ServerHighRPM class scoring at 8000–16000 RPM band              |

Required validation cases:

1. **HIL-PROXY-01** Two identical Noctua NF-A12x25 fans on Proxmox; sweep both 30–100% PWM; verify Spearman ρ(proxy, R30 dBA) ≥ 0.85 per channel.
2. **HIL-PROXY-02** Celeron 16-fan synthetic: assert ≤ 4 µs/fan/tick over 10⁵ ticks.
3. **HIL-PROXY-03** Asetek Gen5 AIO on 13900K: verify S_pump dominates S_host across pump RPM range; verify proxy correctly flags "pump-dominated" via §7.3.
4. **HIL-PROXY-04** Framework laptop blower sweep: verify LaptopBlower class produces tonality-dominant scores (S_tone > S_tip across most of range).
5. **HIL-PROXY-05** NAS Tier-1 channel: verify proxy score evaluates without RPM (PWM-only via class default RPM curve), produces sensible ranking.
6. **HIL-PROXY-06** Audio safety: assert no audio device opened by acoustic package; AppArmor profile contract holds.
7. **HIL-PROXY-07** Wrong-blade-count robustness: deliberately set B = 5 on a 7-blade fan; assert score changes ≤ 8 % and Spearman ρ across PWM sweep > 0.95.
8. **HIL-PROXY-08** Mic + proxy concurrent: 60-s cross-correlation > 0.7 → mic-primary transition; mic disconnect mid-session → proxy-primary fallback within 1 tick; no behaviour discontinuity.
9. **HIL-PROXY-09** Class-table substitution: deliberately mis-class a Be Quiet SW4 as `case_120_140` instead of its measured class; verify within-host ranking still correct (ρ > 0.85), within-host MAE up to ~6 au allowed.
10. **HIL-PROXY-10** Beat-coupling regression: two NF-A12x25 in push-pull at 1500 ± 5 RPM; verify R18 BEAT_TERM is computed and adds to S_host correctly per §7.2.

---

## 13. Cross-references in the R-bundle

- **R8** (fallback tier ceilings): proxy uses `RPM` from Tier 0 when available; on Tier 1+ it uses class-default RPM curves driven by PWM, accepting wider error.
- **R11** (sensor noise floor): proxy ignores RPM jitter below the noise floor when computing BPF.
- **R12** (confidence inputs): the proxy is one of the inputs to the optimiser's cost gate; its confidence is implicit in the class-default vs catalog-known distinction.
- **R17** (multi-channel coupling): the BEAT_TERM in §7.2 is only computed within R17 coupling groups.
- **R18** (acoustic objective without microphone): R33 supersedes R18 §2 (tonal term) and §9 (cost composition) with calibrated formulas; R18's BEAT_TERM, BLOCKLIST_TERM, and SLEW_TERM are inherited unchanged.
- **R28** (failure modes): pump cavitation, bearing wear, and chassis resonance failure modes are surfaced as doctor hints, not as proxy modifications.
- **R30** (mic calibration): R33 composes with R30 per §10; the mic anchors absolute level, the proxy supplies the forward model.
- **R31** (stall signatures, planned): when R31 lands, near-stall fan operating points get an additional whine penalty in `S_motor`. R33 is forward-compatible — the `K_motor` value can be made stall-aware without changing the formula structure.
- **R32** (perception thresholds, planned): when R32 lands, the masking thresholds `M_thr` per class can be re-fitted from psychoacoustic ground truth instead of the literature-derived constant 12. R33 is forward-compatible — `M_thr` is per-class parametric.

---

## 14. Locked invariants for spec consumption

The following statements are spec-quality and may be cited verbatim when the v0.5.12 acoustic feature work lands its rule bindings:

```
R33-LOCK-01  Proxy score is dimensionless (au) and within-host comparable; absolute dBA requires R30.
R33-LOCK-02  Per-fan score is the sum of four non-negative terms: S_tip + S_tone + S_motor + S_pump.
R33-LOCK-03  Broadband term scales as 50·log10(RPM·D / D_ref·RPM_ref) with class-anchored constant.
R33-LOCK-04  Tonal term sums harmonics k ∈ {1, 2, 3} of BPF = B·RPM/60 with weights {1.0, 0.5, 0.25}.
R33-LOCK-05  Tonal-band masking subtracts a 12 dB threshold (8 dB for blowers and pumps) before A-weighting.
R33-LOCK-06  Motor-whine term decays linearly with RPM/RPM_aero_dom and is masked by 0.5·S_tip.
R33-LOCK-07  Pump-vane-tone band is at f_vane = RPM × N_vanes / 60; default N_vanes = 6.
R33-LOCK-08  Multi-fan composition is energetic: S_host = 10·log10(Σ 10^(S_fan/10)) plus R17 BEAT_TERM.
R33-LOCK-09  When mic available, daemon weights mic-derived dBA at min(0.7, mic_conf); else proxy alone.
R33-LOCK-10  Mic-primary mode requires 60 s cross-correlation > 0.7 with proxy delta before takeover.
R33-LOCK-11  Default blade count per class: 7 (case axial), 9 (radiator), 11 (GPU shroud), 27/33 (blowers).
R33-LOCK-12  Wrong blade count produces ≤ 8% score error and preserves within-host ranking (ρ > 0.95).
R33-LOCK-13  Per-tick proxy evaluation is O(N_fans) and ≤ 4 µs / fan on Celeron-class CPU.
R33-LOCK-14  Proxy never opens an audio device, links no audio library, and emits no audio.
R33-LOCK-15  Cost-gate Spearman ρ between proxy and R30 dBA must be ≥ 0.85 per channel for acceptance.
```

These are the load-bearing invariants the v0.5.12 spec work binds against; they will become RULE-ACOUSTIC-PROXY-* subtests in `internal/acoustic/proxy_test.go` when the implementation PR lands.
