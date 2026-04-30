# ventd Smart-Mode Research: Acoustic Objective Without Microphones (R18)

**Status:** Research artifact, spec-input quality
**Target spec version:** v0.7.0+ (post-v0.6.0 smart-mode tag)
**Scope:** Replace placeholder `acoustic_cost(channel, RPM, ΔRPM, preset)` in spec-smart-mode §7.1; back the dithering work currently deferred in spec-smart-mode §14; produce a Phase 4 DITHER spec slice that sits between INTERFERENCE and MPC.
**Inputs available:** Per-channel RPM (R8 Tier 0), R8 fallback signal (Tiers 1–6), R17 coupling-group membership, hardware catalog metadata (often missing), spec-16 KV/Blob/Log.
**Inputs explicitly unavailable:** microphones, IMUs, accelerometers, audio capture, panel-resonance ground truth.

---

## 1. Framing the problem honestly

Smart-mode currently treats the Silent preset as nothing more than a clamp on the PWM ceiling. That is a dimensional confusion: PWM duty is a control variable, not a perceptual one, and the mapping from duty to perceived loudness is non-monotonic across a fleet. A 1500 RPM fan at 60 % PWM can be subjectively quieter than the same fan at 40 % PWM if the lower duty drops the rotor into a panel-resonance band, or if a coupled second fan sits 20 RPM away and the pair beats at a frequency the ear treats as roughness rather than as two tones. Lowering the ceiling does not address either case.

R18 is therefore the research that decides what ventd is actually optimising when a user selects Silent or Balanced, given that ventd has *no acoustic transducer of any kind*. The honest answer is that ventd cannot measure sound, so any "acoustic cost" function is a model-based proxy whose validity depends entirely on (a) how well the model captures the dominant perceptual mechanisms a healthy fan in a normal chassis exhibits, and (b) how the optimiser is allowed to fail when the model is wrong. The rest of this document is structured around the four perceptual mechanisms that *are* inferable from RPM telemetry alone — tonal prominence at the blade-pass frequency, harmonic spread into A-weighted bands, beating between coupled rotors, and aggregate spectral mass — and the three mechanisms that *are not*: absolute SPL, bearing-fault classification, and panel-mode prediction without user feedback.

The output of R18 is therefore not an SPL meter. It is a *psychoacoustic proxy score* whose units are dimensionless "acoustic units" (au), calibrated so that a quiet reference operating point of a typical 120 mm 7-blade fan at 800 RPM scores ~1 au, and so that two identical fans coupled in push-pull at 1500 RPM with a 5 RPM speed difference score noticeably higher (because they generate roughness in the 12.5 Hz band, near the fluctuation-strength peak) than the same two fans at 1500 ± 80 RPM (where the 16 Hz beat sits on the boundary between fluctuation strength and roughness, and is much less annoying). The score is *comparative within a host* and is not a substitute for ISO 7779 measurement.

## 2. Blade-pass frequency and harmonic structure

The fundamental tonal content of any axial cooling fan in steady state is the blade-pass frequency, BPF = B · f_rot, where B is the rotor blade count and f_rot = RPM/60 is the shaft frequency in hertz. This relation is firmly established in axial-fan aeroacoustics; tonal peaks at BPF and its low-order harmonics are the dominant rotation-locked features of subsonic fan noise and are extensively documented in the JASA / Applied Acoustics / Mechanical Systems and Signal Processing literature on fan tonal-noise control (e.g. Gerard, Berry & Masson on flow-obstruction interference; Quinlan on multi-fan beating). Higher harmonics (2·BPF, 3·BPF, …) carry significant energy because the blade-passing pressure pulse is not sinusoidal: it is a periodic train of loading impulses, so its spectrum is line-rich. For a typical 7-blade 120 mm chassis fan:

- 800 RPM → BPF = 93.3 Hz, 2·BPF = 186.7 Hz, 3·BPF = 280 Hz
- 1500 RPM → BPF = 175 Hz, 2·BPF = 350 Hz, 3·BPF = 525 Hz
- 2200 RPM → BPF = 256.7 Hz, 2·BPF = 513.3 Hz, 3·BPF = 770 Hz

A 9-blade liquid-cooler fan at 2400 RPM puts BPF at 360 Hz and its third harmonic above 1 kHz, which matters because the third harmonic is the first one to land in the most A-weight-favoured (i.e. perceptually penalised) part of the spectrum. A 5-blade NAS chassis fan at 4500 RPM, by contrast, puts even its fundamental at 375 Hz.

For ventd this means three useful things. First, *given the blade count*, ventd can predict the exact frequencies at which steady-state tonal energy will appear, with no acoustic measurement. Second, it can *predict the perceptual penalty* of those frequencies through A-weighting and equal-loudness considerations (§4). Third, it can *avoid* operating points where any of the first three harmonics line up with a known-bad band — provided "known-bad" has been learned (§6). What ventd cannot do is predict the *amplitude* of those tones, because amplitude depends on inflow distortion, blade loading, tip-clearance flow, casing reflectivity and the room — things that the unsteadiness studies (e.g. the IFTSM/Carolus group at Siegen) have shown can cause 5–6 dB temporal swings even on a calibrated rig. The proxy must therefore be *relative*, not absolute.

The blade count itself is the major data-quality risk. ventd's hardware catalog usually does not contain it. The mitigation is a default of B = 7 for axial fans 92–140 mm, B = 9 for high-static-pressure radiator fans (which the catalog can often distinguish via the liquidctl driver class or product name regex), B = 5 for small server/NAS fans below 80 mm, and B = 11 for blower-style centrifugal fans found in laptops. These defaults are derived from the published Noctua, EBM-Papst and Sanyo Denki product datasheets and from the acoustic-test inventories in ECMA-74 reference equipment lists; they are correct for the vast majority of the homelab fleet and harmless when wrong, because BPF-based scoring degrades gracefully (see §10).

## 3. Beat-frequency prediction between coupled fans

When two fans run at nearly the same speed and their acoustic outputs sum at a listener position, the result is amplitude modulation at the difference frequency, Δf = |f_BPF,1 − f_BPF,2|. This is the textbook beating phenomenon and is the single most-reported "annoyance" in homelab radiator and case-fan setups. The Noctua technical-bulletin documentation of NF-A12x25 G2 and NH-D15 G2 ships PPA/PPB matched pairs with deliberate ±25–50 RPM offsets specifically to break this beating; the same effect is documented in BYU NOISE-CON 2010 work on fan-array active control (Sommerfeldt group) and in patents on "oscillating speed" anti-beat fan controllers (US 6,257,832, US 6,270,319, US 6,276,900). The phenomenon is real and is one of the few RPM-only-inferable perceptual hazards that ventd can act on.

The perceptual interpretation of Δf is governed by the three-band psychoacoustic structure laid out by Fastl & Zwicker (3rd ed., 2007, ch. 10–11):

- **Δf < ~15 Hz: fluctuation strength.** A slowly-varying loudness fluctuation. Maximum annoyance per asper-equivalent at fmod ≈ 4 Hz (defined as 1 vacil at 1 kHz, 60 dB, 100 % AM). This is the regime where users describe a "throbbing" or "pulsing" sound. It is the *worst* band for perceptual cost per unit of acoustic energy.
- **Δf ≈ 15–300 Hz: roughness.** The modulation is no longer resolved as separate events but as a harsh, granular timbre. Maximum at fmod ≈ 70 Hz (defined as 1 asper). The roughness band is broad and carrier-frequency dependent: for low-frequency carriers (which is the case for fan BPF in the 100–500 Hz range), the peak shifts somewhat lower, but the basic shape holds.
- **Δf > ~300 Hz: separately-audible tones.** The two BPFs are heard as two distinct tones rather than a single modulated one. This is acoustically the *most benign* outcome because the auditory system separates the components and the user perceives them as steady noise.

The second-order subtlety, also from Fastl & Zwicker, is that beating *also* occurs at the harmonic level: if two 7-blade fans run at 1500 and 1505 RPM, their fundamentals beat at 0.583 Hz (well below the fluctuation-strength peak, perceptually a slow swell), but their second harmonics beat at 1.167 Hz, third at 1.75 Hz, and so on; the higher harmonics climb up the fluctuation-strength curve faster than the fundamental and dominate annoyance. ventd must consider at least the first three harmonics when scoring beating between coupled channels.

The R17 multi-channel coupling group output is the natural input here. Within a coupling group ventd already knows which channels share an airflow path; for those channels, and only for those channels, the beat-frequency penalty applies. Channels in different coupling groups (CPU heatsink fan vs rear-chassis exhaust, separated by a baffle or simply by distance) are by definition *not* acoustically coherent in the sense that their pressure waves do not consistently superpose at any single listener position, and the beating model does not apply. The R17 dependency is what makes R18's beating logic crisp rather than handwavy.

## 4. A-weighting and equal-loudness considerations

The A-weighting filter (IEC 61672-1:2013) is the standard frequency weighting applied to broadband sound levels for human-perception approximation. Its third-octave-band attenuation values, for the bands relevant to fan BPF and harmonics, are tabulated as: 50 Hz −30.2, 63 Hz −26.2, 80 Hz −22.5, 100 Hz −19.1, 125 Hz −16.1, 160 Hz −13.4, 200 Hz −10.9, 250 Hz −8.6, 315 Hz −6.6, 400 Hz −4.8, 500 Hz −3.2, 630 Hz −1.9, 800 Hz −0.8, 1000 Hz 0.0, 1250 Hz +0.6, 1600 Hz +1.0, 2000 Hz +1.2, 2500 Hz +1.3 dB. The implication is that a 100 Hz BPF tone of a given physical amplitude is treated as 19 dB quieter than a 1 kHz tone of the same amplitude, while a 2 kHz BPF tone is treated as 1.2 dB *louder*. ventd cannot measure amplitude, but it can apply the A-weighting *as a frequency-dependent prior on perceptual penalty*: BPF and harmonics that fall in the heavily-attenuated low-frequency region get a smaller acoustic-cost contribution than those in the 1–4 kHz peak.

Two caveats from the standards literature must be acknowledged in the cost function and in the doctor surface. First, A-weighting is derived from the 40-phon equal-loudness contour and underestimates perceived annoyance of low-frequency content at the *higher* sound levels typical of a server rack at full tilt; this is part of why C-weighting and Z-weighting exist (also in IEC 61672-1) and why ECMA-418-2 (the Sottek hearing-model basis used by ECMA-74 Annexes G/H) is replacing simple A-weighting for IT-equipment tonality. ventd does not need ECMA-418-2 fidelity (we have no microphone) but it does need to admit that A-weighting *underweights* low-frequency tonality and apply a small low-frequency correction term in the Silent preset. Second, the equal-loudness contours of ISO 226:2003 (and the 2023 revision, with at most 0.6 dB difference) are flatter at higher SPL: at 80–90 phon a 100 Hz tone is much closer to a 1 kHz tone than at 30–40 phon. Since ventd cannot know the absolute SPL, the cost function uses the 40-phon shape, which is the conservative (annoyance-maximising) choice for the Silent preset.

What A-weighting *cannot* do: it cannot capture roughness, fluctuation strength, tonal prominence, or any other suprathreshold psychoacoustic dimension. A signal can have a low dB(A) and still be intolerable because of a prominent 175 Hz tone or a 4 Hz beat. R18 therefore uses A-weighting as one of *four* additive terms in the cost, never as the score by itself.

## 5. Bearing growl: what is and is not inferable from RPM alone

The literature on rolling-element bearing diagnostics — Randall & Antoni's 2011 tutorial in Mechanical Systems and Signal Processing, Antoni's spectral-kurtosis and kurtogram papers, Heng & Nor's 1998 statistical-analysis paper in Applied Acoustics, and the subsequent envelope-spectrum / cyclostationarity literature — converges on a clear conclusion: bearing-fault classification (inner race BPFI, outer race BPFO, ball-spin BSF, fundamental-train FTF) requires *high-frequency vibration or acoustic emission*, typically sampled at tens of kHz, with envelope demodulation and either kurtosis-based or order-tracking analysis. None of the standard bearing-fault frequencies are accessible without an accelerometer, AE sensor, or microphone. ventd has none of these.

What ventd *does* have on Tier-0 channels is per-channel RPM telemetry at the tach-pulse rate. From this, three weak but non-zero indicators can be extracted:

1. **Tach jitter (inter-pulse-interval variance).** A healthy fan in steady state produces extremely regular tach pulses; the inter-pulse interval has tight variance dominated by the controller-PWM quantisation and the encoder geometry. A bearing developing wear injects torque ripple at the shaft frequency, which modulates instantaneous angular velocity and therefore inter-pulse interval. This is detectable as an *increase* in inter-pulse-interval variance, normalised against R11 sensor noise floor. It is a *trend* indicator only — useful for "this fan has changed over months" but useless for real-time classification.
2. **PWM-to-RPM curve drift.** R9/R10's ARX model already learns the steady-state PWM→RPM map. A bearing degrading toward seizure shifts this curve (more PWM needed for the same RPM); this shows up as RLS innovation persistently above the R8 conf_C floor on the static-gain term. Again, trend only.
3. **R8 fallback degradation.** On Tiers 1–6 (no tach), the only signal is whatever proxy R8 is using. None of these are sensitive to bearing condition in any predictable way.

R18's honest position is therefore: ventd surfaces a *bearing-health hint* in the doctor output — a "this fan's tach jitter has increased 3x over its baseline; consider replacement" line — but **does not** modify the acoustic cost function based on bearing condition and **does not** classify fault type. The right response to a hint is a user replacing the fan, not the controller silently working around it. This is consistent with Randall & Antoni's repeated point that diagnostic claims require diagnostic-grade signals, and avoids the well-known pitfall of statistical surrogates (kurtosis on tach jitter, etc.) being driven by load changes rather than bearing wear.

## 6. Resonance avoidance via user feedback

Panel resonance, duct standing waves, hard-drive sympathetic vibration ("rust-bucket NAS" effect), and case-mounted-fan structural coupling are all observed phenomena in the homelab acoustic literature and the EBM-Papst / Noctua technical bulletins. None of them is predictable from RPM alone, because they are all properties of the *mechanical system the fan is mounted in*, not of the fan. ventd cannot predict, from a fresh install on a new chassis, that 1180–1240 RPM is a bad band on this user's particular Define 7. With no microphone, there is no way to learn it autonomously.

The only realistic mechanism is *user feedback*. spec-16's KV store can hold a per-channel `acoustic_blocklist[]` of (rpm_low, rpm_high, weight) triples that the cost function treats as additive penalties. Population is a simple doctor command (`ventd doctor acoustic block <channel> <rpm_low> <rpm_high>`) plus, optionally, a "hold this RPM, was that bad?" interactive flow analogous to laptop-display backlight calibration. The data is per-host (because chassis changes) and per-channel (because mount points differ), and is small (tens of bytes per entry, dozens of entries per host worst case).

The optimiser treats the blocklist as a soft constraint: the cost function adds a smooth bump (raised-cosine over the band) rather than a hard wall, because hard walls cause limit-cycle oscillation against the thermal controller. The Balanced preset uses a smaller weight on blocklist penalties than Silent. The Performance preset ignores them entirely.

This is the section to be most epistemically honest about. ventd cannot do panel-resonance avoidance well; it can only do it *at all*. The feature exists primarily because (a) the alternative is silent failure when the chassis is bad, (b) the data structure is cheap, and (c) users who care about the Silent preset are exactly the users willing to spend ten minutes telling ventd which RPM bands their case hates.

## 7. Acoustic dithering for push-pull radiators

Acoustic dithering is the deliberate introduction of small, controlled differences in commanded RPM between two or more fans in the same coupling group, with the goal of pushing their beat frequency *out of* the fluctuation-strength band and into either DC (matched fans, beat = 0) or the separately-audible band (beat > 300 Hz at the BPF, which usually means RPM offsets the user would notice as airflow imbalance — not practical). The realistic target is to push the *fundamental BPF* beat into a less-bad part of the perceptual map: out of the 2–8 Hz fluctuation-strength peak, ideally to either ≤ 1 Hz (perceptually a slow swell) or ≥ 100 Hz (deep into roughness, where the auditory system tolerates it because cooling fans already sound rough). The Noctua PPA/PPB pairing is a worked example of the latter strategy at small offsets.

For a 7-blade fan pair at nominal 1500 RPM, a 50 RPM offset gives a fundamental BPF beat of 7 · 50/60 = 5.83 Hz — still in the fluctuation-strength region, but past the 4 Hz peak and dropping. A 100 RPM offset gives 11.67 Hz, within striking distance of the fluctuation-to-roughness transition. A 200 RPM offset gives 23.3 Hz, comfortably in roughness — but at the cost of unequal airflow. The cost-versus-offset curve is non-monotonic and depends on blade count and operating speed; ventd's optimiser, given the blade counts and the R17 coupling group, can solve for it explicitly.

The dithering policy that R18 recommends is:

1. **Within an R17 coupling group of size ≥ 2**, identify pairs of channels with overlapping operating ranges and similar steady-state RPMs.
2. **Compute the predicted beat frequency** at the current operating point for each pair, using BPF = B · RPM/60 and the harmonic stack up to k = 3.
3. **If any predicted beat across {1, 2, 3} · BPF falls inside the [0.5 Hz, 20 Hz] high-cost window**, request an offset of one channel by Δ such that the *minimum* beat across the harmonic stack moves out of the high-cost window. The offset is small (typically 30–80 RPM), is shared with the thermal controller as a soft preference (not a constraint), and is *static* — it does not oscillate, because oscillating offsets are themselves a fluctuation source.
4. **Cap total dithering authority** at a Silent-preset-dependent maximum (e.g. Silent ≤ 6 % of nominal, Balanced ≤ 3 %, Performance = 0).
5. **Defer to thermal**: if the thermal controller cannot meet target with the dithered setpoint, the dither is released first.

The dithering is therefore a constraint *inside* R17's coupling-group output, not an override of it. The data flow is R17 → R18 dither preferences → smart-mode optimiser, additive only. R17 state shape is unchanged.

## 8. AC vs battery preference modulation (R19 territory)

For laptops on battery, smaller fans, and lower SPL ceilings, the perceptual cost of the same operating point is qualitatively different: the user is closer to the device, the ambient is usually quieter, and any tonal prominence is more exposed. There is also a power-vs-acoustic preference that a user on AC may want set differently from on battery (more acoustic headroom on AC, more thermal headroom on battery, or vice versa). This is real and worth solving.

It is **not** R18's job. R19 is the AC/battery / power-source-aware preset modulation research; R18 explicitly defers all power-source coupling to R19. The R18 cost function is parametric in a `preset_weight_vector` that R19 can swap based on power source, but R18 does not itself read battery state, AC state, or any ACPI surface. This keeps R18 testable in isolation on a desktop HIL rig.

## 9. Putting it together: the four-term acoustic cost

The acoustic cost for a single channel at a single optimisation tick is the sum of four terms:

```
acoustic_cost(ch, RPM, ΔRPM, preset) =
      w_T(preset) · TONAL_TERM(ch, RPM)        // §2 + §4
    + w_B(preset) · BEAT_TERM(ch, group, RPM)  // §3 + §7
    + w_R(preset) · BLOCKLIST_TERM(ch, RPM)    // §6
    + w_S(preset) · SLEW_TERM(ch, ΔRPM)        // see below
```

**TONAL_TERM** sums A-weighted, low-frequency-corrected perceptual weights at BPF, 2·BPF, 3·BPF, where BPF = B · RPM/60. The low-frequency correction is a +6 dB shelf below 200 Hz to compensate for A-weighting's known under-penalisation of low-frequency tonality at moderate-to-high levels; this is a Silent-preset-only adjustment. The harmonic weights are 1.0, 0.5, 0.25 (decreasing geometrically; this matches the empirical line-spectrum decay of well-designed axial fans in the unsteadiness literature and is an acknowledged simplification).

**BEAT_TERM** iterates over pairs (ch, ch') in the same R17 coupling group, computes the harmonic-stack beat frequencies, and applies a perceptual-cost lookup keyed on Δf using a piecewise model of the Fastl & Zwicker fluctuation-strength and roughness curves (peak 1.0 at 4 Hz, monotone decay to 0.1 by 0.5 Hz, monotone decay to 0.3 at 70 Hz, decay to 0.05 by 300 Hz, 0 above 500 Hz). The lookup table is 64 entries, log-spaced from 0.1 Hz to 1 kHz. Static, shared, no per-channel state.

**BLOCKLIST_TERM** evaluates the per-channel blocklist as raised-cosine bumps. Empty by default. Populated via doctor command. Persists in spec-16 KV.

**SLEW_TERM** penalises |ΔRPM| because rapid setpoint changes are themselves perceived as fluctuation. This is a small term and mostly serves to keep the optimiser from chattering; it is not a primary perceptual mechanism.

The preset weight vectors are:

| Preset      | w_T | w_B | w_R | w_S |
|-------------|-----|-----|-----|-----|
| Silent      | 1.0 | 1.0 | 1.0 | 0.5 |
| Balanced    | 0.5 | 0.7 | 0.5 | 0.3 |
| Performance | 0.1 | 0.2 | 0.0 | 0.1 |

These are starting points subject to HIL tuning. The thermal benefit term is unchanged from the existing smart-mode optimiser; R18 only replaces the acoustic side.

## 10. Honest limits

- **No absolute SPL.** Every output of R18 is comparative within a host. ventd cannot tell a user "this is 32 dB(A)". Anyone wanting an absolute number must use a measurement device.
- **No bearing fault classification.** Tach jitter is a trend indicator only; doctor surface emits a hint, the cost function does not act on it.
- **No autonomous resonance avoidance.** Without a microphone there is no way to learn panel modes; the blocklist is user-populated and admittedly incomplete.
- **Blade-count-defaults dependency.** Wrong blade count produces wrong BPF, but the proxy degrades gracefully because A-weighting is monotone in frequency over the relevant fan range, and beating logic only requires *consistent* (not absolute) BPF estimates within a coupling group.
- **Tier 1–6 (tachless) channels.** Beat prediction requires both channels in a group to have RPM. On Tier-1+ channels, BEAT_TERM is skipped and only TONAL_TERM, BLOCKLIST_TERM, SLEW_TERM contribute. This is correct: with no RPM there is no BPF estimate.
- **Fluctuation strength below 0.5 Hz.** Below ~0.5 Hz beat frequency the auditory system stops integrating into a perceptual percept and starts hearing it as separate events ("two fans, alternately louder"); this is essentially a perceptual deadband and is modelled as cost → 0, which is correct in the limit of matched fans (Δf → 0) but incorrect for, e.g., 0.1 Hz beats where the listener can still hear the slow swell. We accept this as a known mismodelling.

## 11. Actionable conclusions

- **Replace the placeholder.** The cost function in spec-smart-mode §7.1 should be replaced with the four-term form above. Silent should no longer mean "lower the PWM ceiling"; it should mean "set w_T=w_B=w_R=1.0 in the optimiser".
- **Promote DITHER from Phase 4 deferral to Phase 4 spec slice.** R18 supplies the theory; the implementation slot is between INTERFERENCE and MPC, consuming R17.
- **Add a doctor surface.** Live metrics for predicted BPF stack, beat predictions per coupling group, blocklist entries; recover items for "blade count missing, defaulted to N"; internals for cost-term breakdowns at each tick.
- **Add a one-time calibration flow (optional).** A `ventd doctor acoustic calibrate` interactive command that walks RPM and asks the user to flag bad bands; populates the blocklist in spec-16 KV. Optional in v0.7.0, default off.
- **Defer to R19.** AC/battery modulation is R19. R18 ships preset-weight-vector parametricity but does not read power-source state.

---

# Appendix A: Spec-Ready Findings

## A.1 Algorithm choice + rationale

**Choice:** Closed-form, lookup-table-backed, four-term additive perceptual proxy evaluated at each smart-mode optimiser tick. No FFT, no IIR filtering, no audio path.

**Rationale:**
- Closed-form because each term is O(blade-harmonics) or O(coupling-group-size²), both bounded small.
- Lookup tables (A-weighting at 24 third-octave centers; F&Z fluctuation-strength/roughness at 64 log-spaced Δf points) because (a) the underlying data are themselves measured/standardised tables, (b) lookup is faster than evaluating IEC 61672-1 transfer functions live, (c) Celeron-class CPU budget rules out anything heavier.
- Additive (rather than multiplicative or max) because each term has a well-defined zero (no harmonic in band, no coupled neighbour, empty blocklist, zero ΔRPM) and the optimiser benefits from convex-ish gradients.
- No frequency-domain processing because we have no signal in the time domain. We are evaluating predictions, not measurements.

**Rejected alternatives:**
- Full Sottek hearing model (ECMA-418-2): correct tool for measured signals, useless without a signal.
- Online learning of acoustic cost from operating data: there *is* no acoustic operating data without a mic.
- Per-channel ARX-augmented bearing-fault model: violates Randall & Antoni's signal-bandwidth requirement. We refuse to fake it.

## A.2 State shape and memory budget

Per-channel additive state (atop the locked R1–R17 shape):

| Field                          | Type        | Bytes | Notes                                      |
|--------------------------------|-------------|-------|--------------------------------------------|
| blade_count                    | uint8       | 1     | from catalog or default                    |
| blade_count_source             | enum:2bit   | 1     | catalog/heuristic/default/user             |
| tach_jitter_baseline_us        | uint32      | 4     | EWMA of inter-pulse-interval std-dev       |
| tach_jitter_current_us         | uint32      | 4     | current EWMA                               |
| acoustic_blocklist_count       | uint8       | 1     | up to 16 entries                           |
| acoustic_blocklist[≤16]        | (u16,u16,u8)| 80    | (rpm_lo, rpm_hi, weight) × 16              |
| **per-channel total**          |             | **91**| round to 96 B for alignment                |

Per-system additive state:

| Field                          | Type        | Bytes | Notes                                      |
|--------------------------------|-------------|-------|--------------------------------------------|
| preset_weight_vector           | float32×4×3 | 48    | 3 presets × 4 weights, R19 may override    |
| a_weight_lut[24]               | float32     | 96    | third-octave A-weighting, IEC 61672-1      |
| psycho_lut[64]                 | float32     | 256   | F&Z fluctuation/roughness vs Δf            |
| harmonic_weight[3]             | float32     | 12    | 1.0, 0.5, 0.25                             |
| **per-system total**           |             | **412**| static, shared, read-only after init      |

For a 16-channel system: 16 × 96 + 412 = **1,948 B**, comfortably inside Tier S (16 KiB) for the per-channel structures and trivially inside Tier M for the shared LUTs. Well under R10's d_B ≤ 18 covariance budget envelope. No spec-16 schema migration needed beyond addition of `acoustic_blocklist` to the channel KV record.

## A.3 RULE-* invariant bindings (1:1 with subtests)

```
RULE-ACOUSTIC-PROXY-01  acoustic_cost is non-negative for all valid inputs
RULE-ACOUSTIC-PROXY-02  acoustic_cost is monotone non-decreasing in RPM at fixed coupling group, blocklist, ΔRPM
RULE-ACOUSTIC-PROXY-03  TONAL_TERM uses BPF = B · RPM / 60 with B from catalog or class-default
RULE-ACOUSTIC-PROXY-04  TONAL_TERM evaluates harmonics k ∈ {1,2,3}, weights {1.0, 0.5, 0.25}
RULE-ACOUSTIC-PROXY-05  TONAL_TERM applies IEC 61672-1 A-weighting at the harmonic frequency
RULE-ACOUSTIC-PROXY-06  TONAL_TERM applies +6 dB low-frequency shelf below 200 Hz under Silent preset only
RULE-ACOUSTIC-PROXY-07  BEAT_TERM is computed only over pairs in the same R17 coupling group
RULE-ACOUSTIC-PROXY-08  BEAT_TERM evaluates beat at k·BPF for k ∈ {1,2,3} for each pair
RULE-ACOUSTIC-PROXY-09  BEAT_TERM uses the F&Z piecewise lookup with peak 1.0 at Δf=4 Hz and decay specified in psycho_lut
RULE-ACOUSTIC-PROXY-10  BEAT_TERM is zero for Tier-1+ (tachless) channels in the pair
RULE-ACOUSTIC-PROXY-11  BLOCKLIST_TERM is a sum of raised-cosine bumps over user-provided (rpm_lo, rpm_hi, weight) entries
RULE-ACOUSTIC-PROXY-12  BLOCKLIST_TERM bumps are smooth (C¹) at boundaries
RULE-ACOUSTIC-PROXY-13  SLEW_TERM is proportional to |ΔRPM| with preset-dependent gain
RULE-ACOUSTIC-PROXY-14  Silent preset weights satisfy w_T = w_B = w_R = 1.0
RULE-ACOUSTIC-PROXY-15  Performance preset weights satisfy w_R = 0.0
RULE-ACOUSTIC-PROXY-16  Dithering authority is bounded ≤ preset_dither_cap (Silent 6%, Balanced 3%, Performance 0%)
RULE-ACOUSTIC-PROXY-17  Dithering yields to thermal controller when thermal innovation exceeds R8 conf_C floor for >2 ticks
RULE-ACOUSTIC-PROXY-18  Dithering offset is static during a stable thermal regime (no oscillating offset)
RULE-ACOUSTIC-PROXY-19  Bearing-health hint emits to doctor when tach_jitter_current_us > 3 × tach_jitter_baseline_us for >5 minutes
RULE-ACOUSTIC-PROXY-20  Bearing-health hint never modifies acoustic_cost
RULE-ACOUSTIC-PROXY-21  R18 reads no power-source state; preset_weight_vector is owned by R19
RULE-ACOUSTIC-PROXY-22  Wrong / missing blade_count produces a bounded score error and never panics
RULE-ACOUSTIC-PROXY-23  Per-channel state is exactly 96 B (post-alignment); per-system shared state is exactly 412 B
RULE-ACOUSTIC-PROXY-24  Cost evaluation completes in ≤ 50 µs per tick on Celeron-class CPU at 16 channels, 4 coupling groups
RULE-ACOUSTIC-PROXY-25  Acoustic cost function emits no audio, opens no audio device, and links no audio library
```

Each RULE binds 1:1 to a Go subtest of the same name in `internal/acoustic/`.

## A.4 Doctor surface contract

**Live metrics** (`ventd doctor acoustic`, refresh 1 s):
- Per channel: BPF stack {f1, f2, f3} in Hz, per-harmonic A-weight contribution in dB, tach-jitter ratio (current/baseline)
- Per coupling group: pair list with predicted Δf at k=1,2,3 and beat penalty
- Per channel: blocklist entry count, last-hit entry id
- Total acoustic cost, broken down by term, by channel, with preset

**Recover items** (`ventd doctor recover`):
- `acoustic.blade_count.missing` for any channel with `blade_count_source=default`, with suggested override command
- `acoustic.bearing_hint` for channels with sustained tach-jitter elevation
- `acoustic.coupling_unknown` for channels in a coupling group with no other RPM-bearing members (BEAT_TERM disabled)

**Internals** (`ventd doctor internals acoustic`):
- a_weight_lut and psycho_lut hash and dump
- preset_weight_vector dump
- per-channel state dump (95 B)
- last 64 cost evaluations with per-term breakdown (ring buffer in Tier M)

**Subcommands:**
- `ventd doctor acoustic block <ch> <rpm_lo> <rpm_hi> [weight]` — adds blocklist entry
- `ventd doctor acoustic unblock <ch> <id>` — removes blocklist entry
- `ventd doctor acoustic calibrate <ch>` — interactive sweep + flag bad bands
- `ventd doctor acoustic explain <ch>` — pretty-print why this RPM scored what it scored

## A.5 HIL validation matrix (Phoenix's fleet)

| Host                                      | Role                       | Coverage                                                             |
|-------------------------------------------|----------------------------|----------------------------------------------------------------------|
| Proxmox 5800X + RTX 3060                  | Multi-fan desktop, R17 hot | BEAT_TERM with 3+ chassis fans; calibrate flow on hard-mounted fans  |
| MiniPC Celeron @ 192.168.7.222            | CPU-budget gate            | RULE-ACOUSTIC-PROXY-24 (≤50 µs/tick at 16 ch, 4 groups)              |
| 13900K + RTX 4090 dual-boot               | High-RPM radiator AIO      | 9-blade liquidctl path; harmonic stack into A-weighting positive band|
| Framework laptop                          | Centrifugal blower         | Single-channel, no coupling group, B=11 default path                 |
| ThinkPad                                  | Centrifugal + dell-smm-ish | R11 noise floor 60 RPM regime; SLEW_TERM tuning                      |
| Steam Deck                                | Single small fan           | Tier-1 fallback BEAT skip; TONAL_TERM only                           |
| Third laptop (TBD)                        | Spare                      | Repeat coverage                                                      |
| TerraMaster F2-210 NAS                    | Tier-1+ tachless fan       | RULE-ACOUSTIC-PROXY-10 (BEAT zero for tachless), TONAL on R8 fallback|

Required validation cases:
1. **HIL-ACOUSTIC-01** Two identical 7-blade fans on Proxmox at 1500 RPM ± 0/5/50/100/300 RPM offsets; verify cost function ranks them in the order 5 > 0 > 100 > 300 > 50 (5 RPM → 0.583 Hz beat at fundamental, very near fluctuation peak; 50 RPM → 5.83 Hz, falling edge of FS; 100 RPM → 11.67 Hz; 300 RPM → 35 Hz, deep roughness, less annoying than slow fluctuation).
2. **HIL-ACOUSTIC-02** Celeron 16-ch synthetic bench: assert ≤50 µs/tick over 10⁵ ticks.
3. **HIL-ACOUSTIC-03** TerraMaster: assert TONAL_TERM evaluates with R8 Tier-N RPM estimate and BEAT_TERM is bypassed.
4. **HIL-ACOUSTIC-04** Steam Deck: assert single-channel cost evaluates without coupling-group lookup.
5. **HIL-ACOUSTIC-05** Calibrate flow: simulate a user marking 1180–1240 RPM bad on Proxmox case fan; assert blocklist entry persists across daemon restart, scoring penalises that band, optimiser routes around it.
6. **HIL-ACOUSTIC-06** AppArmor profile audit: assert no audio device opened, no `/dev/snd*` access, no PulseAudio/PipeWire D-Bus.
7. **HIL-ACOUSTIC-07** Bearing-hint trigger: inject synthetic tach-jitter elevation; assert doctor recover emits hint and acoustic_cost is *unchanged*.
8. **HIL-ACOUSTIC-08** Wrong-blade-count robustness: deliberately set B=5 on a 7-blade fan; assert score changes by < 30 % and ranking across operating points is preserved (Spearman ρ > 0.9).

## A.6 Estimated CC cost (Sonnet, single PR)

Phoenix targets $10–30 per spec execution. Breakdown:

| Slice                                          | LoC   | CC Cost  |
|------------------------------------------------|-------|----------|
| `internal/acoustic/cost.go` (four-term core)   | ~400  | $4–6     |
| `internal/acoustic/luts.go` (A-weight + F&Z)   | ~200  | $1–2     |
| `internal/acoustic/dither.go` (R17 consumer)   | ~250  | $3–5     |
| `internal/acoustic/blocklist.go` (KV CRUD)     | ~150  | $1–2     |
| `internal/acoustic/jitter.go` (bearing hint)   | ~150  | $1–2     |
| Doctor surface wiring (`internal/doctor/`)     | ~200  | $2–3     |
| Subtests for RULE-ACOUSTIC-PROXY-01..25        | ~600  | $5–7     |
| HIL harness + cases 01..08                     | ~500  | $4–6     |
| spec-smart-mode §7.1, §14 rewrite              |       | $1–2     |
| **Total**                                      | ~2450 | **$22–35** |

Slightly above the $30 ceiling; Phoenix can split into two PRs: (1) cost-function + LUTs + tonal/blocklist + tests (~$14), (2) dither + bearing hint + doctor + HIL (~$15), each comfortably in budget.

## A.7 Spec target version

**v0.7.0** is the target. Justification:
- v0.6.0 ships smart-mode capability tag with placeholder acoustic cost; R18 is by definition post-v0.6.0.
- R18 is additive to the locked R1–R17 state; no migration.
- DITHER moves from Phase-4-deferred to Phase-4-shipped.
- The spec-smart-mode §14 deferral is removed and replaced with the four-term cost spec.

## A.8 Citations (published academic and standards literature)

- Fastl, H. & Zwicker, E. (2007). *Psychoacoustics: Facts and Models* (3rd ed.). Springer-Verlag, Berlin / Heidelberg. Chs. 8 (loudness), 10 (fluctuation strength), 11 (roughness).
- ISO 226:2003, *Acoustics — Normal equal-loudness-level contours*. International Organization for Standardization. (Revised as ISO 226:2023; differences ≤ 0.6 dB.)
- ISO 532-1:2017, *Acoustics — Methods for calculating loudness — Part 1: Zwicker method*. International Organization for Standardization.
- ISO 532-2:2017, *Acoustics — Methods for calculating loudness — Part 2: Moore-Glasberg method*.
- ISO 7779:2018, *Acoustics — Measurement of airborne noise emitted by information technology and telecommunications equipment* (4th ed.).
- IEC 61672-1:2013, *Electroacoustics — Sound level meters — Part 1: Specifications*. International Electrotechnical Commission. (A-, C-, Z-weighting tables.)
- ECMA-74 (19th ed., December 2021), *Measurement of Airborne Noise emitted by Information Technology and Telecommunications Equipment*. Ecma International. Annex D (TNR, PR), Annexes G/H (psychoacoustic tonality and roughness).
- ECMA TR/108 (1st ed., June 2019), *Total Tone-to-Noise Ratio and Total Prominence Ratio for Small Air-Moving Devices*. Ecma International.
- ECMA-418-1 / ECMA-418-2, *Psychoacoustic metrics for ITT equipment* (Sottek hearing model basis).
- Hellweg, R. (2008). "Updates on Prominent Discrete Tone Procedures in ISO 7779, ECMA 74, and ANSI S1.13." *Journal of the Acoustical Society of America* 123(5 Supplement): 3451.
- Randall, R. B. & Antoni, J. (2011). "Rolling element bearing diagnostics — A tutorial." *Mechanical Systems and Signal Processing* 25(2): 485–520.
- Antoni, J. (2007). "Fast computation of the kurtogram for the detection of transient faults." *Mechanical Systems and Signal Processing* 21(1): 108–124.
- Heng, R. B. W. & Nor, M. J. M. (1998). "Statistical analysis of sound and vibration signals for monitoring rolling element bearing condition." *Applied Acoustics* 53(1–3): 211–226.
- Gerard, A., Berry, A. & Masson, P. (2013). "Use of a beat effect for the automatic positioning of flow obstructions to control tonal fan noise: Theory and experiments." *Journal of Sound and Vibration* (ScienceDirect S0022460X13002824).
- Gee, K. L. & Sommerfeldt, S. D. (2004) and follow-on BYU group. "Active control of multiple cooling fans." *NOISE-CON 2010 Proceedings*, Baltimore.
- Carolus, T. et al. (2014). "Unsteadiness of blade-passing frequency tones of axial fans." University of Siegen IFTSM publication 136/2014.
- Cattanei, A. et al. "Effect of uneven blade spacing on noise annoyance of axial-flow fans and side-channel blowers." *Applied Acoustics* (S0003682X21000177).
- Suzuki, Y. & Takeshima, H. (2004). "Equal-loudness-level contours for pure tones." *Journal of the Acoustical Society of America* 116(2): 918–933. (Underlying ISO 226:2003 / 2023 data.)
- Aures, W. (1985). "Procedure for calculating the sensory euphony of arbitrary sound signals." *Acustica* 59: 130–141. (Roughness model used by ECMA-74 Annex G predecessors.)

(Noctua and EBM-Papst technical bulletins on push-pull beating, blade count, and bearing types are referenced in the body for design-context purposes and are not relied on for any quantitative claim; all numerical thresholds in the cost function trace to the standards and academic literature above.)