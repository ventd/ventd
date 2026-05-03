# R31 — Acoustic stall-verification signatures for `ventd calibrate --acoustic`

**Status:** Research artifact, spec-input quality.
**Target spec version:** v0.7.x (post-mic) — informs a future `--acoustic` flag for `ventd calibrate`.
**Predecessor:** R28 (R28-master "dummy tach" failure class) and R18 (psychoacoustic proxy without a microphone).
**Companion when mic is present:** this document — the failure modes that the tach signal cannot catch and that a single USB microphone, sampled at 16 kHz mono, *can* catch.

**Inputs available at calibration time:** one USB mic (16 kHz mono, A/B-grade — not a measurement microphone), per-channel RPM telemetry from R8 Tier 0/1, blade count when known (defaults from R18 §2: B=7 for 92–140 mm axial, B=9 for radiator fans, B=5 for small server, B=11 for laptop blowers), thermal sensor stream, ambient SPL captured during a deliberate 10 s "silence sweep" at the start of calibration (all controllable channels at min responsive PWM).

**Inputs explicitly unavailable:** accelerometers, AE (acoustic-emission) sensors, hydrophones, microphone arrays, anechoic chamber, 1/3-octave SLM, IEPE preamps, calibrated reference mics. A consumer USB mic at 16 kHz mono has a Nyquist ceiling of 8 kHz, ~60–70 dB SNR in the office band, no calibration to dB SPL, and its frequency response is ±5 dB across 100 Hz–6 kHz at best.

---

## 1. Executive summary

Bearing wear, blade flutter and AIO pump cavitation are the three failure classes R28 identified as "dummy tach" — the tach reports a plausible RPM while something is wrong with the rotor. The conclusions below are bounded by what an uncalibrated 16 kHz mono USB mic in a chassis-adjacent position can hear.

### 1.1 Bearing stall / impending bearing failure

- **Most reliable of the three.** Bearing wear is the most acoustically distinctive of the three because it produces *impulsive* (high-crest-factor) energy and characteristic-frequency periodicity, which the literature has 60+ years of treatment of (envelope analysis, kurtosis, spectral kurtosis).
- **Time-domain signature:** crest factor (peak/RMS) ≥ 4–5 on a healthy bearing rises to 8–15 as wear progresses; kurtosis ≥ 4 (healthy bearings sit around 3 by definition of Gaussian noise). On a dry sleeve bearing the dominant cue is a *raised broadband floor* with a low-frequency rumble (<200 Hz) and 1×–3×RPM tonal modulation.
- **Spectral signature:** broadband 1–4 kHz lift and *sidebands* at ±BPFO/±BPFI around a structural resonance (the resonance itself is usually outside the 16 kHz mic's clean band, but the envelope-modulation sidebands fall back into the audible band — see §4). For consumer fans the bearing characteristic frequencies sit in the 30–500 Hz band (worked example: §4.1.4).
- **Detection threshold:** a *worn* bearing typically lifts the broadband 1–4 kHz band by 6–12 dB(A) over a healthy reference of the same fan at the same RPM. Below ~3 dB rise the signal is in the noise of inflow turbulence and casing reflection.
- **False-positive risk:** dust accumulation on blades, partially blocked intake, a flexed fan grille, a cable touching the fan — all raise the 1–4 kHz floor by 2–6 dB without a real bearing fault. Mitigation: use the *envelope spectrum* (§4) for peak detection, not just band energy.
- **Time-to-detect:** 5–15 s of audio gives a stable estimate of crest factor, kurtosis, and the broadband rise. The envelope-spectrum peak takes a longer window (15–30 s) because the modulation rate is low (BPFO/BPFI ≈ 30–500 Hz, but the envelope-of-envelope cycle is set by shaft frequency, 1–50 Hz).

### 1.2 Blade flutter / near-stall AOA

- **Catchable, but only at the operating point where it occurs.** Flutter is a narrow-band aeroelastic instability that appears at a *specific* PWM/RPM, typically just before aerodynamic stall, and disappears outside it. The detector must sweep through the operating range (which `ventd calibrate` does anyway) and look for *transient* tonal energy in a narrow band around BPF.
- **Time-domain signature:** *amplitude modulation* of the BPF tone at the rotating-stall frequency (30–50 % of running frequency), producing a beating that sounds to a human like a "warble" or "throb". Crest factor stays moderate (4–7); the signature is in the *modulation envelope* of the BPF carrier, not in impulsive transients.
- **Spectral signature:** sidebands at BPF ± f_stall, where f_stall ≈ 0.3–0.5 × f_rot. For a 7-blade 120 mm fan at 1500 RPM, BPF = 175 Hz, f_rot = 25 Hz, f_stall ≈ 7.5–12.5 Hz. The sidebands appear at ~163 Hz and ~187 Hz around the 175 Hz BPF carrier. The flutter narrowband peak is 6–15 dB above the surrounding broadband.
- **Detection threshold:** sideband prominence (peak vs local floor) ≥ 6 dB sustained for ≥ 2 s. Below that, the variability of inflow turbulence and small RPM jitter (tach reports nominal RPM but rotor speed varies by ±2 % cycle-to-cycle) generates false positives.
- **False-positive risk:** *high*. Two coupled fans beating against each other (R18 §3) produce identical BPF sidebands. Inflow distortion from a cable or duct reflection produces narrow tonal peaks that resemble flutter sidebands. Mitigation: require the sideband to appear at a known fraction of f_rot (0.3–0.5×) and to track f_rot when RPM changes.
- **Time-to-detect:** flutter is *operating-point specific*. The PWM dwell at the suspect operating point must be ≥ 5 s; the sideband detector then needs ~2 s of continuous evidence. Practical detection budget: 10–15 s per probed PWM step.

### 1.3 AIO pump cavitation

- **Detectable, but with caveats.** Cavitation produces a broadband spectrum that *extends to high frequencies* and has a low-frequency pulsing component. The 16 kHz mic captures most of the discriminating spectrum.
- **Time-domain signature:** mid-frequency hiss (sounds like rushing water or steam) with low-frequency pulsing. Crest factor is *lower* than bearing fault (3–5) because cavitation is a near-Gaussian random process from many small bubble collapses, not impulsive transients. The signature is in the *spectral shape*, not in impulses.
- **Spectral signature:** broadband lift in the 1–3 kHz band (literature: cavitation is most reliably detected in the 1000–3000 Hz region for centrifugal pumps because that band is least affected by operating conditions outside cavitation), plus pulsing at the pump shaft frequency and impeller blade-pass frequency. AIO pumps typically run at 2400–4800 RPM with 2–4 impeller vanes, putting impeller BPF at 80–320 Hz.
- **Detection threshold:** a fully-cavitating pump lifts the 1–3 kHz band by 8–15 dB over the healthy reference. *Incipient* cavitation (the regime the literature calls "inception") lifts it by 2–4 dB and is essentially undetectable with a 16 kHz mono mic — the AE-sensor incipient-cavitation literature operates in the 0.5–1 MHz range, far above what we have.
- **False-positive risk:** *moderate*. Trapped air bubbles ("gurgling") in a *non-failed* AIO produce similar broadband + pulsing acoustics; the user-facing distinction is "bubbles dissipate within a few minutes after startup; cavitation persists". The detector cannot distinguish these in a single calibration session.
- **Time-to-detect:** 10–20 s of audio at the pump's normal operating PWM. Cavitation is steady-state, not transient, so the detector benefits from longer averaging.

### 1.4 Honest scope of the feature

`ventd calibrate --acoustic` should be advertised as a **stall-verification** feature, not a fault-classification feature. The output is one of three states per channel:

1. *No anomaly* (acoustic energy at the expected operating point matches a healthy reference within tolerance).
2. *Anomaly suspected* (one or more of the three signatures above triggers above its threshold). The user is shown which signature triggered and is told to investigate.
3. *Insufficient SNR to verify* (ambient SPL is too high, or the chassis mounting position is too far from the suspect channel; the calibration runs without the acoustic verifier and falls back to tach-only).

State (3) is the load-bearing one. Most homelab deployments will hit it. The detector must not produce false-positive alarms that send users on a wild goose chase.

---

## 2. Per-failure-mode characterisation table

| Property                   | Bearing stall                                                | Blade flutter                                                        | AIO pump cavitation                                                       |
|----------------------------|--------------------------------------------------------------|----------------------------------------------------------------------|---------------------------------------------------------------------------|
| **Signal class**           | Impulsive + broadband lift                                   | Modulated tonal                                                      | Broadband + low-freq pulsing                                              |
| **Crest factor (healthy)** | 3–5                                                          | 4–6                                                                  | 3–4                                                                       |
| **Crest factor (faulty)**  | 8–15                                                         | 5–8                                                                  | 4–6                                                                       |
| **Kurtosis (healthy)**     | ~3                                                           | ~3                                                                   | ~3                                                                        |
| **Kurtosis (faulty)**      | 4–10+                                                        | 3.5–5                                                                | 3–3.5                                                                     |
| **Dominant band(s)**       | 1–4 kHz broadband + envelope sidebands at BPFO/BPFI (30–500 Hz, depends on geometry) | Narrowband around BPF (BPF ± f_stall, where f_stall ≈ 0.3–0.5 × f_rot) | 1–3 kHz broadband lift + pump-BPF and harmonics (80–320 Hz)               |
| **Bandwidth**              | Broadband (1–4 kHz) for raw signal; tonal in envelope spectrum | Narrowband (~10–30 Hz wide around BPF)                               | Broadband (~2 kHz wide) + discrete tones at impeller BPF                  |
| **SNR over healthy**       | 6–12 dB(A) band rise                                         | 6–15 dB sideband prominence                                          | 8–15 dB band rise (severe); 2–4 dB (incipient — undetectable here)        |
| **Pulsing periodicity**    | None for sleeve-bearing rumble; envelope at shaft freq for ball bearings | At rotating-stall freq (~0.3–0.5 × f_rot, so 5–15 Hz for consumer fans) | At pump shaft freq (40–80 Hz) and impeller BPF (80–320 Hz)                |
| **Transient envelope**     | Sharp impulses (rise time < 1 ms, fall time 5–20 ms) on ball bearings; quasi-Gaussian on sleeve | Slow amplitude modulation of BPF tone (rise/fall in 10s of ms) | Many small bubble-collapse bursts averaging to near-Gaussian              |
| **Detect time @ 16 kHz mono** | 5–15 s (broadband + crest); 15–30 s for envelope spectrum    | 5–15 s per PWM step (must dwell at suspect operating point)         | 10–20 s (steady-state averaging)                                          |
| **False-positive sources** | Dust on blades, blocked intake, flexed grille, cable touching fan | Coupled-fan beating, inflow distortion, RPM jitter                  | Trapped air bubbles in healthy AIO, pump motor electrical noise           |
| **Required calibration**   | Healthy reference at same RPM/PWM (per-fan baseline learned in first calibration) | Healthy reference at same RPM/PWM, plus blade count                  | Healthy reference at pump's nominal PWM, plus impeller blade count        |
| **Discriminator**          | Envelope-spectrum peak at BPFO/BPFI tracking f_rot          | Sideband prominence tracking f_rot (3:1 sideband-to-floor)          | 1–3 kHz band rise + impeller-BPF tone presence                            |
| **What the mic cannot catch** | Sub-mm cracks (need AE > 100 kHz); incipient bearing wear before macroscopic damage | Flutter outside the calibration sweep; flutter at <100 Hz BPF (mic noise floor) | Incipient cavitation (needs AE 0.5–1 MHz); fluid-borne pressure pulsations |

---

## 3. The literature: what is solid, what is borrowed, what is wishful

### 3.1 Bearing diagnostics — the most-developed of the three

The condition-monitoring literature on rolling-element bearings is decades-deep and converges on a small set of techniques that all require *vibration* (or AE) input but whose extracted features carry over to airborne acoustics if the SNR is sufficient.

**ISO 13373** is the relevant standard family. ISO 13373-1:2002 covers general procedures for vibration condition monitoring; ISO 13373-2:2016 covers signal processing, analysis, and presentation; ISO 13373-3 covers diagnostic guidelines; ISO 13373-9 (2017) specifically covers diagnostic techniques. The standard recognises two basic approaches: **time-domain analysis** (RMS, crest factor, kurtosis, peak, peak-to-peak) and **frequency-domain analysis** (spectrum, envelope spectrum, order-tracking). Bearing faults are explicitly listed as one of the dynamic phenomena the standard addresses, with the canonical observation that "faults in rolling element bearings are often detected from repeated high-frequency transient responses to ball or race defects".

The four characteristic frequencies (BPFO, BPFI, BSF, FTF) are the load-bearing concept. For a rolling-element bearing with N rolling elements, ball diameter Bd, pitch diameter Pd, contact angle α, and shaft frequency f_rot = RPM/60, the formulas (from any standard bearing-vibration reference) are:

```
BPFO = (N/2) · (1 − (Bd/Pd) · cos α) · f_rot
BPFI = (N/2) · (1 + (Bd/Pd) · cos α) · f_rot
BSF  = (Pd / (2·Bd)) · (1 − (Bd/Pd)² · cos² α) · f_rot
FTF  = (1/2)         · (1 − (Bd/Pd) · cos α) · f_rot
```

For a small instrument bearing typical of a 120 mm chassis fan (N=8, Bd=2.5 mm, Pd=10 mm, α=0°, RPM=1500 → f_rot=25 Hz), the characteristic frequencies are:

```
BPFO ≈ 4 · 0.75 · 25 = 75 Hz
BPFI ≈ 4 · 1.25 · 25 = 125 Hz
BSF  ≈ 2 · 0.9375 · 25 ≈ 47 Hz
FTF  ≈ 0.5 · 0.75 · 25 ≈ 9.4 Hz
```

These all sit comfortably in the 1–500 Hz band that an 8 kHz-Nyquist mic captures. The catch: they are *modulation* frequencies on a high-frequency carrier (the bearing's structural resonance, typically 2–10 kHz). Envelope analysis is the technique that recovers the modulation rate from a broadband signal: bandpass the raw signal around the resonance, full-wave rectify (or take Hilbert envelope), then FFT the envelope to find peaks at BPFO/BPFI/BSF.

The MathWorks and Brüel & Kjær (B&K) treatments of this — the canonical industry references — note that envelope analysis is "the optimal method for detecting incipient bearing faults" because it separates the *carrier* (where the impulses excite a resonance) from the *modulation* (the impulse rate, which is the diagnostic). Spectral kurtosis and the kurtogram (Antoni 2006, "The spectral kurtosis: a useful tool for characterising non-stationary signals") automate the choice of bandpass: the optimal demodulation band is the one where the signal is most impulsive (highest kurtosis).

For ventd, the practical question is whether the resonance band fits in the 16 kHz Nyquist (yes — small fan bearings resonate around 2–6 kHz), and whether the airborne-mic SNR is sufficient to recover the envelope sidebands (marginal — see §5 on SNR budget).

**Crest factor** (peak / RMS) is the simplest of the impulsiveness indicators. A healthy bearing produces near-Gaussian noise with crest factor 3–5; a damaged bearing emits sharp impulses that lift the peak much faster than the RMS, producing crest factors 8–15. **Kurtosis** (4th central moment / σ⁴) is the related dimensionless indicator: ≈ 3 for Gaussian noise, increasing with impulsiveness. Both are easy to compute and have decades of acceptance in the field. The "Pachaud-Salvetat" 1997 work explicitly compares crest factor and kurtosis for impulsive-defect identification.

**Sleeve bearings** — common on consumer cooling fans, particularly older Cooler Master / Arctic / generic case fans — produce a *different* failure mode. Instead of impulsive defects, sleeve bearings fail by *lubricant evaporation* leading to dry-shaft contact. The audible signature is a "whining" or "whirring" tone that increases with RPM, with a raised low-frequency rumble. Crest factor stays near healthy because there are no impulses; the discriminator is the broadband low-frequency rise plus 1×–3× shaft-frequency tonal content. The Comair-Rotron and GamersNexus references both note that sleeve bearings start dead-silent and "abruptly die" — meaning the failure is sudden in user-perceptible terms, but the acoustic precursor (gradual whine) is typically days to weeks old by the time the user hears it.

### 3.2 Blade flutter and rotating stall

The aeroacoustics literature (Wikibooks "Engineering Acoustics/Noise from cooling fans"; the AIVC "Aerodynamic Noise of Fans" reference; ASME GT2010 "Detection of Stall Regions in a Low-Speed Axial Fan: Part I — Azimuthal Acoustic Measurements" and "Part II — Stall Warning by Visualisation of Sound Signals") all converge on the same finding: rotating stall in axial fans produces a stall cell that propagates around the rotor at 30–50 % of the running frequency *in the opposite direction* to rotation, generating unsteady blade forces and consequently unsteady noise.

The detectable acoustic signature is a *modulation* of the BPF tone. If the BPF carrier is at f_BPF and the stall cell rotates at f_stall (with f_stall ≈ 0.3–0.5 × f_rot), the result is a tone at f_BPF with sidebands at f_BPF ± f_stall and possibly f_BPF ± 2·f_stall. For consumer fans:

| Fan          | RPM   | f_rot [Hz] | B (blades) | BPF [Hz] | f_stall [Hz] | Sidebands [Hz]    |
|--------------|-------|------------|------------|----------|---------------|--------------------|
| 120 mm axial | 800   | 13.3       | 7          | 93       | 4–6.7         | 86–89, 97–100      |
| 120 mm axial | 1500  | 25         | 7          | 175      | 7.5–12.5      | 162–168, 182–188   |
| 140 mm axial | 1200  | 20         | 9          | 180      | 6–10          | 170–174, 186–190   |
| 92 mm server | 4500  | 75         | 5          | 375      | 22.5–37.5     | 337–352, 398–413   |

The sidebands sit comfortably in the audible band for a 16 kHz mic. The challenge is that the sidebands are typically only 6–15 dB above the *local* spectral floor and may be only 10–20 dB above broadband noise. A *coupled-fan beating* signal (two fans at slightly different RPMs, R18 §3) produces the same sideband structure, so the detector cannot distinguish flutter from beating without consulting the per-channel RPM telemetry: if a sideband's frequency offset from BPF tracks a known coupled fan's BPF difference, it is beating, not flutter.

The Reynolds-number and inflow-distortion literature (Carolus group at Siegen; the "Acta Acustica" 2022 work on slitted leading-edge blades) cautions that *inflow turbulence* can produce broadband elevations near BPF that mimic flutter sidebands. The discriminator is *coherence with f_rot*: a real flutter signal moves its sideband frequency in proportion to RPM; an inflow-distortion artefact stays at a fixed frequency tied to the chassis geometry.

**Practical conclusion:** flutter is detectable in principle but is the most false-positive-prone of the three signatures. A conservative detector requires:

1. Sideband peak ≥ 6 dB above local floor.
2. Sideband frequency offset from BPF in the range 0.25 × f_rot to 0.55 × f_rot (the rotating-stall band).
3. The same offset persists when the fan RPM is changed (the offset must scale with f_rot).
4. No coupled-fan beating explanation: no other fan in the same R17 coupling group has a BPF within ±20 % of the observed sideband.

Conditions (3) and (4) are why this signature works only during *active calibration*, not during passive monitoring: the calibration sweep changes the RPM and lets the detector verify that the sideband tracks.

### 3.3 Pump cavitation

The pump-cavitation literature (Dong 2019 "Detection of Inception Cavitation in Centrifugal Pump by Fluid-Borne Noise Diagnostic"; Sheikh et al. "Evaluation the low cost of vibration and acoustics techniques based on novel cavitation detecting in axial pumps"; the MDPI Processes 2023 review "A Review of Pump Cavitation Fault Detection Methods Based on Different Signals") converges on three findings:

1. **The discriminating frequency band is 1–3 kHz** for centrifugal pumps. The cited bands are "1000–2000 Hz rarely influenced by operating conditions under non-cavitation" and "2000–3000 Hz sensitive to cavitation but not influenced by flow conditions". This is *because* cavitation produces small (sub-mm) bubble collapses whose individual durations are 10–100 µs, with peak energy at 10–100 kHz, but whose envelope statistics produce broadband audible-range elevation centred in the low-kHz band as the bubbles' collapse-shock acoustics propagate through the coolant and casing.

2. **AE (acoustic emission) detection at 0.5–1 MHz catches incipient cavitation** before the acoustic signature reaches the audible band. This is consistent with the bearing literature: AE finds early defects, audible noise finds advanced defects. ventd has no AE sensor and so cannot catch incipient cavitation.

3. **Pump and impeller blade-pass frequencies appear as discrete tones** even in a healthy pump: Dong reports peaks at shaft frequency (24 Hz in their test pump), blade frequency (144 Hz = 6 vanes × 24 Hz), and harmonics. For consumer AIO pumps at 2400–4800 RPM with 2–4 impeller vanes, this puts the pump shaft tone at 40–80 Hz and the impeller BPF at 80–320 Hz. A *cavitating* pump shows broadband lift in the 1–3 kHz band on top of these discrete tones.

The user-side gotcha for consumer AIO pumps is that **trapped air bubbles** ("gurgling") in a *non-failed* AIO produce similar acoustics: broadband lift plus low-frequency pulsing. The Corsair, darkFlash, Linus Tech Tips and EveZone references all distinguish these as user-side problems: bubbles dissipate within a few minutes of running the pump at full speed and after physical re-orientation; cavitation persists indefinitely. Within a single calibration session the detector cannot tell them apart, so `ventd calibrate --acoustic` should report "cavitation or trapped air suspected — re-orient PC, run pump at 100 % for 1 hour, and re-run calibration" rather than declaring one or the other.

### 3.4 Reference datasets and open-source projects

The community has produced several reusable resources:

- **CWRU Bearing Dataset** (Case Western Reserve University). The canonical bearing-fault benchmark: vibration data from a 2 hp induction motor with EDM-machined faults of varying sizes (7, 14, 21, 28, 40 mil) on inner race, outer race, and ball, at four motor loads, 1720–1797 RPM. Used in hundreds of papers as the standard reference. Available on Kaggle and in many GitHub mirrors. Sample rate 12 kHz or 48 kHz depending on subset.

- **NASA / IMS Bearing Dataset** (NASA Prognostics Center of Excellence + University of Cincinnati). Run-to-failure data: four bearings on a loaded shaft (6000 lbs) at 2000 RPM, vibration sampled at 20 kHz, 1-second snapshots every 10 minutes until failure. Three datasets covering inner-race, outer-race and rolling-element failures. The canonical dataset for *prognostics* (predicting remaining useful life) rather than just classification.

- **MIMII Dataset** (Hitachi, Ltd. — Purohit et al. 2019). The most directly relevant dataset to ventd's use case: *audio* (not vibration) recordings of industrial machines including **fans, pumps, valves, slide rails**, sampled at **16 kHz mono** by an 8-channel mic array (each channel usable independently). 5 000–10 000 seconds of normal sound and ~1 000 seconds of anomalous sound per machine model, across seven product models per machine type. Anomalies include contamination, leakage, rotating unbalance, rail damage. This dataset is what we should validate any ventd anomaly detector against, since the recording conditions match our deployment (single ~16 kHz mic, factory floor noise floor analogous to a homelab room) more closely than any vibration dataset.

- **MIMII DUE / MIMII DG** (same group, 2021 / 2022). Domain-shift extensions of MIMII: same machines, recorded under different operational/environmental conditions, used for the DCASE Challenge Task 2 on unsupervised anomalous-sound detection.

- **DCASE Challenge Task 2 (2020–2025)**: an annual benchmark on "Unsupervised Anomalous Sound Detection for Machine Condition Monitoring". Builds on MIMII and ToyADMOS. The 2023+ editions added "first-shot" generalisation requirements (deploy on a new machine type without machine-specific tuning) and domain-generalisation requirements. The DCASE baseline implementations are available on GitHub and use the MIMII `mimii_baseline` repo as a starting point. The relevance for ventd: these systems achieve ~80–90 % AUC on machine anomalous-sound detection from a single-channel 16 kHz mic, which is *roughly* what we should expect to achieve. They are not 99% — anyone promising us high accuracy from a USB mic in a homelab is overselling.

- **PyOD** (yzhao062/pyod) and **scikit-multiflow** (now succeeded by River). PyOD is the standard Python anomaly-detection library — 60+ detectors, batch-oriented but with some streaming support. scikit-multiflow's IForestASD (Isolation Forest for streaming data) is the canonical streaming anomaly detector. Neither is acoustic-specific; both apply downstream of feature extraction (e.g. log-mel spectrograms or hand-engineered statistics).

- **Hussain-Aziz/Machine-Sound-Anomaly-Detector**: a small GitHub project that uses convolutional autoencoders + novelty detection on the latent space to detect anomalies in 10-second clips from MIMII-style data. A reasonable reference for "what does a small project of this kind look like in practice".

- **AudioMoth** (OpenAcousticDevices) and various USB-mic projects (mico, OpenRefMic, INMP441-based USB mics). Hardware-side; useful if we ever want to recommend a specific mic for the feature.

### 3.5 What we are *not* using

- **ASHRAE Handbook Chapter 49 "Noise and Vibration Control"** — covers HVAC duct attenuation, plenum design, and room acoustics. Useful if we ever need to *predict* SPL at the user position from a known fan emission, but not directly relevant to anomaly detection.

- **AMCA Standard 300** ("Reverberant Room Methods of Sound Testing of Fans"). A *measurement* standard for characterising fan emissions in a lab. Not applicable to in-situ detection.

- **ECMA-74 / ECMA-418-2** — IT-equipment noise emission standards. Useful for tonality scoring in R18; not directly relevant here.

---

## 4. Detection algorithms

### 4.1 Bearing wear: broadband + envelope spectrum

A two-stage detector. Stage 1 is cheap, runs on every block, and triggers on broadband lift. Stage 2 runs only when stage 1 trips, is expensive, and confirms by finding envelope-spectrum peaks at BPFO/BPFI tracking f_rot.

#### 4.1.1 Stage 1 — broadband and impulsiveness gates

Block size 8192 samples (0.51 s at 16 kHz). Hop 4096 samples (50 % overlap).

```
1. Take a healthy reference snapshot during the first calibration of the channel.
   The reference is per (channel, RPM-bucket) where RPM-bucket = round(RPM/100)·100.
   Stored under KV namespace: acoustic/ref/<channel>/<rpm_bucket>.cbor
   Reference fields: rms_db (calibrated relative to ambient floor, not absolute SPL),
   band_energy[8] (octave bands centred 31, 63, 125, 250, 500, 1k, 2k, 4k Hz),
   crest_factor, kurtosis.

2. At runtime, compute current block's rms_db, band_energy[8], crest_factor, kurtosis.

3. Trigger stage 2 if any of:
   - band_energy[5] (1 kHz) or band_energy[6] (2 kHz) > reference + 6 dB
   - crest_factor > max(5, reference.crest_factor + 2)
   - kurtosis    > max(4, reference.kurtosis + 1.5)
```

#### 4.1.2 Stage 2 — envelope-spectrum peak detection

Block size 16384 samples (1.02 s at 16 kHz). Sliding window with 75% overlap; need ~15 s of audio for a stable envelope spectrum.

```
1. Estimate optimal demodulation band via simplified kurtogram:
   - Try four candidate bands: [500, 1500], [1000, 2000], [1500, 3000], [2000, 4000] Hz.
   - For each: bandpass filter, compute kurtosis of the envelope (Hilbert), pick the band
     with the highest kurtosis. (This is a fast 4-way kurtogram, not the full 1/3-octave
     kurtogram; cheaper, sufficient for the chassis-fan SNR regime.)

2. Bandpass the raw signal at the chosen band.

3. Compute the analytic-signal envelope via |hilbert(x)|.

4. Compute the FFT of the envelope; the result is the envelope spectrum.

5. Compute predicted BPFO and BPFI from per-channel f_rot = RPM / 60 and assumed
   bearing geometry (default: N=8 balls, Bd/Pd ≈ 0.25, α=0°, giving
   BPFO ≈ 3 · f_rot, BPFI ≈ 5 · f_rot).

6. Search the envelope spectrum for peaks within ±5 % of the predicted BPFO and BPFI
   plus their first three harmonics.

7. Compute peak prominence (peak height − local-floor median in a 10 Hz neighbourhood).

8. Confirm bearing-fault if any one of:
   - BPFO peak prominence ≥ 6 dB AND tracks f_rot when RPM changes (verified by
     re-running at two PWM steps differing by ≥ 200 RPM).
   - Same for BPFI.
   - Same for at least two of the first-three harmonics of either BPFO or BPFI.
```

#### 4.1.3 Why the bearing-geometry assumption is OK

We do not know the bearing parameters of any consumer fan. The key fact, from the BPFO/BPFI formulas, is that the ratios BPFO/f_rot and BPFI/f_rot depend only on N, Bd/Pd, and α — not on RPM. For *any* small instrument bearing with N=7–9 and Bd/Pd ≈ 0.2–0.3 and α=0°, BPFO/f_rot lands in [2.5, 3.6] and BPFI/f_rot lands in [4.4, 5.5]. So we search the envelope spectrum in *bands* (BPFO band: 2.5–3.6 × f_rot; BPFI band: 4.4–5.5 × f_rot) rather than at exact predicted frequencies. This widens the search but is robust to the unknown geometry.

#### 4.1.4 Worked example

A 120 mm fan at 1500 RPM (f_rot = 25 Hz):

- BPFO band: 62.5 − 90 Hz
- BPFI band: 110 − 137.5 Hz
- BPFO 2nd harmonic: 125 − 180 Hz (collides with BPFI fundamental — accept the collision; either trips the detector)
- BPFI 2nd harmonic: 220 − 275 Hz

A 9 mm bearing peak at 73 Hz that tracks to 122 Hz when RPM ramps to 2500 RPM (f_rot = 41.67 Hz, BPFO band 104 − 150 Hz) is a clear bearing-fault signature: 73 / 25 = 2.92, 122 / 41.67 = 2.93 — both ratios match BPFO geometry of N=8, Bd/Pd=0.25, α=0°.

### 4.2 Blade flutter: BPF sideband detector

Block size 16384 samples (1.02 s at 16 kHz). Need ≥ 5 s of audio at the suspect operating point.

```
1. From per-channel RPM and assumed blade count B (R18 §2 defaults), compute f_rot
   and BPF = B · f_rot.

2. Compute the spectrum (Welch, Hann window, 50 % overlap) over the dwell period.

3. Locate the BPF tonal peak: find the maximum within ±10 Hz of the predicted BPF.

4. Search for sidebands in the rotating-stall band:
   - Sideband-search range: BPF ± [0.25 · f_rot, 0.55 · f_rot].
   - For each candidate sideband, compute prominence (peak − local-floor median).

5. Trigger flutter if:
   - sideband prominence ≥ 6 dB
   - sustained for ≥ 2 s of overlapping blocks
   - the sideband offset (in Hz) tracks f_rot when RPM is changed by ≥ 200 RPM
   - no R17-coupled fan has a BPF within ±20 % of the observed sideband
     (rules out coupled-fan beating)
```

The four conditions are AND-gated. False-positive rate without all four is high; with all four, the only remaining false-positive sources are inflow distortion that happens to resonate at the same frequency offset across two RPMs (rare but possible) and grille-mounted resonance that scales with airflow (also rare).

### 4.3 Pump cavitation: 1–3 kHz band lift + impeller-BPF tone

Block size 8192 samples. Need ≥ 10 s of audio at the pump's nominal operating PWM.

```
1. Compute Welch PSD over the dwell period.

2. Compute energy in the 1000–3000 Hz band (the cavitation-sensitive band per §3.3).

3. Compute energy in the 4000–8000 Hz band (the reference band — relatively unchanged
   between healthy and cavitating pumps in the literature).

4. Compute the cavitation index: CI = E_1k_3k / E_4k_8k.

5. Locate the impeller-BPF tone: f_imp_BPF = Vanes · (pump_RPM / 60), where Vanes
   defaults to 3 for AIO pumps when not in catalog.

6. Detect the impeller-BPF tone prominence (peak vs local floor in a 20 Hz band).

7. Trigger cavitation suspect if any of:
   - CI exceeds healthy-reference CI by ≥ 8 dB
   - 1–3 kHz band energy exceeds healthy reference by ≥ 8 dB
   - impeller-BPF tone disappears or changes amplitude by ≥ 6 dB compared to the
     healthy reference (cavitation modulates impeller loading and breaks the tonal
     periodicity).

8. Report ambiguity: cavitation OR trapped air OR both. Do not commit to one diagnosis.
```

### 4.4 The simplest 90%-coverage detector

If we ship one detector for v0.7.0, ship the **broadband-rise + crest-factor + impulsiveness** detector. This is a single block of ~30 lines of Python-equivalent logic that catches the most prevalent failure (bearing wear is by far the dominant fan-failure class in homelab fleets) and the clearer-cut subset of cavitation, and rejects most false positives by requiring agreement across three statistics.

```python
def detect_acoustic_anomaly(audio_block, ref):
    """
    Simplest 90%-coverage acoustic anomaly detector for ventd calibrate --acoustic.

    Parameters:
        audio_block: numpy array of shape (N,) at 16 kHz, mono, normalised to [-1, 1].
                     N >= 8192 (>= 0.5 s of audio).
        ref:         per-(channel, rpm_bucket) reference: dict with keys
                     'rms_db', 'band_energy_db' (length 8 — octave bands 31..4k Hz),
                     'crest_factor', 'kurtosis'.

    Returns:
        AcousticVerdict: enum of {OK, ANOMALY_BEARING_LIKELY, ANOMALY_FLUTTER_LIKELY,
                                  ANOMALY_PUMP_LIKELY, INSUFFICIENT_SNR}.
        details: dict with the four computed statistics and which ones tripped.
    """
    N = len(audio_block)
    rms = sqrt(mean(audio_block ** 2))
    if rms < 1e-4:           # mic is dead or fan is silent
        return INSUFFICIENT_SNR, {}

    rms_db   = 20 * log10(rms)
    peak     = max(abs(audio_block))
    crest    = peak / rms
    kurt     = scipy.stats.kurtosis(audio_block, fisher=False)  # ~3 for Gaussian

    # Octave-band energies: 31, 63, 125, 250, 500, 1k, 2k, 4k Hz
    bands_hz   = [(22, 44), (44, 88), (88, 177), (177, 354),
                  (354, 707), (707, 1414), (1414, 2828), (2828, 5657)]
    psd, freqs = welch(audio_block, fs=16000, nperseg=4096)
    band_db    = [10 * log10(sum(psd[(freqs>=lo) & (freqs<hi)]) + 1e-12)
                  for (lo, hi) in bands_hz]

    # Three independent gates
    broadband_rise   = max(band_db[5] - ref['band_energy_db'][5],   # 1 kHz
                           band_db[6] - ref['band_energy_db'][6])   # 2 kHz
    crest_excess     = crest - ref['crest_factor']
    kurt_excess      = kurt  - ref['kurtosis']

    tripped = []
    if broadband_rise >= 6:  tripped.append('broadband_1k_2k')
    if crest_excess   >= 2:  tripped.append('crest_factor')
    if kurt_excess    >= 1.5: tripped.append('kurtosis')

    # Need ≥2 of 3 gates to claim anomaly (single-gate trips are dust, blocked
    # intake, casing flex, etc.).
    if len(tripped) < 2:
        return OK, {'tripped': tripped}

    # Coarse classification by which gates tripped:
    if 'crest_factor' in tripped and 'kurtosis' in tripped:
        return ANOMALY_BEARING_LIKELY, {'tripped': tripped, ...}
    if broadband_rise >= 8 and band_db[5] - band_db[7] > ref_diff_1k_4k + 4:
        # 1k-band rises faster than 4k-band → cavitation pattern
        return ANOMALY_PUMP_LIKELY, {'tripped': tripped, ...}
    return ANOMALY_BEARING_LIKELY, {'tripped': tripped, ...}  # default fallback
```

This detector does **not** detect blade flutter. Flutter requires the BPF-sideband logic in §4.2, which is more involved and has higher false-positive risk. Ship the simple detector first; add the flutter detector in a follow-up once the simple detector has run on a fleet and we have empirical false-positive rates.

### 4.5 Implementation pseudocode for the full pipeline

```text
ventd calibrate --acoustic flow:

1. Pre-flight (before any PWM write):
   1a. Verify mic device is present and accessible (via ALSA / PulseAudio / PipeWire).
   1b. Sample 5 s of "ambient" audio with all controllable channels at min responsive PWM.
   1c. Compute ambient RMS in dB FS. If ambient_rms_db > -25 dB FS, refuse acoustic
       verification (ambient is too loud; we cannot resolve fan emissions). Fall back
       to tach-only calibration.
   1d. Verify mic frequency response is sensible by playing a brief 1 kHz pulse from
       speakers if available — OR — by checking that the ambient spectrum has a
       reasonable shape (no flat-line, no dominant 50/60 Hz hum from a broken USB cable).

2. For each channel, for each PWM step in the calibration sweep:
   2a. Write the PWM value via polarity.WritePWM (RULE-POLARITY-05).
   2b. Wait for thermal/RPM settle (the existing calibration logic owns this).
   2c. Capture 15 s of audio (single channel, 16 kHz mono).
   2d. Run §4.4 simple detector: per-block, with hop 0.5 s, accumulate verdicts.
   2e. If ≥ 4 of 30 blocks (≈ 13 %) report ANOMALY_*, stage 2:
       - Run §4.1 envelope-spectrum analysis (bearing) over the full 15 s.
       - Run §4.3 cavitation detector (only if channel.is_pump or RPM > 3000).
       - Run §4.2 flutter detector (requires a second PWM step at different RPM —
         dispatch this only if the calibration sweep has already done a second step
         on the same channel; otherwise schedule for the next step).
   2f. Persist verdict per (channel, PWM step) under KV namespace
       acoustic/verdict/<channel>/<pwm>.

3. Post-sweep aggregation:
   3a. For each channel, summarise verdicts across all PWM steps.
   3b. Distinguish:
       - Anomaly at ALL PWM steps → high-confidence per-channel fault.
       - Anomaly at SOME PWM steps → operating-point-specific (consistent with flutter).
       - Anomaly at NO PWM steps → channel is acoustically OK.
       - INSUFFICIENT_SNR at most steps → mic position is too far from this channel,
         or this channel is too quiet relative to its neighbours.
   3c. Surface findings in `ventd doctor` output (one line per channel).
   3d. Update the per-(channel, RPM) reference in KV — only if all blocks reported OK,
       so a confirmed-faulty fan does not corrupt its own future reference.

4. Exit: write a single summary line to journald and the diag bundle:
   "acoustic verification: 5 channels probed, 4 OK, 1 ANOMALY_BEARING_LIKELY (chassis_fan_3 at 1500 RPM); 0 INSUFFICIENT_SNR".
```

---

## 5. SNR budget — does this work at all on a USB mic?

A back-of-the-envelope SNR analysis to bound expectations.

### 5.1 Reference levels

A typical USB measurement-grade mic (e.g. an INMP441 or a Behringer ECM8000) has:

- Sensitivity: ~−26 dBV/Pa = ~50 mV/Pa
- Self-noise: ~28–30 dB(A) SPL equivalent
- Dynamic range: ~90 dB SNR at 94 dB SPL reference (1 Pa)
- ADC: 16 or 24 bit at 16 kHz mono → ENOB typically 14 bits, noise floor ~ -85 dB FS

A *consumer* USB mic (built-in webcam, headset mic, Blue Yeti at default gain) has worse numbers:

- Self-noise: ~35–45 dB(A)
- SNR: ~60–70 dB
- Frequency response: ±5 dB across 100 Hz–6 kHz, often with a presence boost at 2–4 kHz (designed for speech intelligibility, not flat measurement)
- Aggressive automatic gain control (AGC) on by default, which destroys absolute level information

For ventd, we should assume the consumer-mic case. The detector must work on uncalibrated, AGC-modified, frequency-response-unknown audio.

### 5.2 Fan emission levels

A typical 120 mm chassis fan at 1 m emits 18–25 dB(A) at 800 RPM and 28–35 dB(A) at 1500 RPM. At the chassis surface (~10 cm) the SPL is ~20 dB higher (1/r² + reflection). So a chassis-mounted mic 10 cm from a 1500 RPM fan sees ~50 dB(A) SPL.

Ambient room SPL in a typical homelab is 30–45 dB(A) at night, 40–55 dB(A) during the day. The fan signal is therefore roughly 5–20 dB above ambient at the chassis-mounted mic.

A *bearing-faulty* fan emits 6–12 dB(A) more in the 1–4 kHz band than a healthy fan of the same model. At the mic position, this is a ~6–12 dB band-energy rise on top of the existing ~5–20 dB SNR. The detector needs to resolve a 6 dB rise against a noise floor that includes ambient + AGC artefacts + frequency-response drift. Empirically, the MIMII baseline papers show ~80–90 % AUC on this kind of task; we should expect ~75–85 % AUC for ventd's use case (slightly worse because consumer mic vs measurement mic).

### 5.3 Conditions under which the detector will fail

- Mic positioned > 50 cm from the chassis (SPL drop with distance brings fan emission close to ambient).
- Air-conditioning, server-rack noise, or another loud appliance in the same room (ambient > 55 dB(A)).
- AGC enabled and actively riding levels (drowns 6 dB band rises in AGC compensation).
- Multiple fans within the same R17 coupling group (the detector cannot attribute the anomaly to a specific channel without spatial separation, which a single mic cannot provide).

The detector must self-check for these conditions during the pre-flight (§4.5 step 1) and refuse rather than report misleading verdicts.

---

## 6. Caveats — what this won't catch and what calibration the user must do

### 6.1 What the detector cannot catch

1. **Incipient bearing wear** below the audible-band threshold. AE sensors at >100 kHz catch sub-mm cracks; we cannot. Bearings will have audible signs only after macroscopic wear has begun.
2. **Incipient cavitation** at the bubble-inception threshold. AE sensors at 0.5–1 MHz catch this; we cannot.
3. **Blade-pitch defects** that do not produce flutter (e.g. one blade chipped) at operating points outside the calibration sweep. The detector only verifies the operating points the sweep visits.
4. **Failure modes that produce subsonic or ultrasonic signatures only**. Sub-100 Hz signatures fall into the room-mode and HVAC-thump band where the mic floor is high; supersonic (>8 kHz) signatures are above the 16 kHz Nyquist.
5. **PSU coil whine, capacitor electrolyte rumble, hard-drive head clicks, and other non-fan acoustic events**. The detector attributes any anomaly to the channel currently being probed; if a non-fan sound source generates an anomaly during a probe step, the channel will be falsely flagged. Mitigation: the post-sweep aggregation requires anomaly-at-all-PWM-steps for a high-confidence per-channel fault, and operating-point-specific anomalies are flagged as "investigate" rather than "faulty".
6. **Failure modes whose acoustic signature is below the consumer-mic's frequency response**. Most consumer USB mics roll off below 100 Hz; subsonic shaft-frequency content of laptop blower fans (which run at 6000+ RPM, putting shaft frequency at 100 Hz) is barely captured.
7. **Coupled-fan attribution**. A single mic cannot tell which of two fans in the same coupling group is faulty. The detector must report "anomaly somewhere in coupling group X" rather than per-channel when the coupling group has > 1 fan.

### 6.2 What the user must do before --acoustic produces useful output

1. **Position the mic close to the chassis** (≤ 15 cm). The setup wizard must have a clear instruction with a picture: "Place the mic on top of, beside, or inside the chassis. Do not place it on a different desk or in a different room." This is non-negotiable; the SNR budget collapses otherwise.

2. **Disable mic AGC/auto-level** in the OS. AGC will obliterate the band-rise signature. The ventd pre-flight should detect AGC by playing a short test signal at two amplitudes and checking that the measured signal scales linearly. If it doesn't, refuse acoustic verification with a clear message.

3. **Run the silence-floor capture in a quiet room** if possible. This sets the ambient reference. If the user's room is loud (server rack at 60 dB(A)), the detector should refuse rather than report unreliable verdicts.

4. **Run a "healthy reference" calibration when the system is known-good**, before any fan has a chance to develop a fault. The per-(channel, RPM) reference is stored in KV and used to detect deviations. Without this baseline, the detector has nothing to compare to and falls back to a generic "all consumer fans look like this" reference, which is much weaker.

5. **Re-run the reference calibration after any hardware change** that affects acoustics: new fans, new chassis, repositioned mic, new PC location. The hwmon_fingerprint mismatch logic from RULE-CPL-PERSIST-01 should extend to cover an "acoustic_fingerprint" that tracks the mic device and the chassis (probably DMI product_name + mic USB VID:PID).

6. **Accept that the feature is a "second opinion"** to the existing tach-based calibration, not a replacement. The tach is the load-bearing safety primitive; the acoustic verifier produces *advisory* output. R28's existing failure-mode classification (PolarityPhantom, BIOSOverride, etc.) takes precedence; --acoustic adds a "BearingWearSuspected" advisory class that the wizard surfaces alongside, not in place of, the tach-based outcome.

### 6.3 What the spec must specify clearly

When this becomes a real spec slice, it must say:

1. The acoustic detector is **advisory only**. It never refuses control, never aborts calibration, never blocks a fan curve from being applied. Its output is a `Severity=Warning`-class hwdiag entry per affected channel.
2. The acoustic detector **opt-in**. The flag is `--acoustic` (off by default); when run without the flag, calibration proceeds tach-only. Adding the flag without a mic present produces a clear "no mic detected" message and proceeds tach-only.
3. The reference store is **per-host**, not fleet-shared. Acoustic baselines vary too much across chassis, mic models, and room geometry for a fleet baseline to be useful.
4. The detector **does not** call out vendor or model-specific failure codes. "Bearing wear suspected" is the maximum specificity. The user is told to investigate; ventd does not declare which bearing or which failure mode.
5. The detector **must not** record raw audio to disk in the diag bundle. Raw audio carries privacy implications (background voices, room conversations) that R28's redactor was not designed to handle. Only the *summary statistics* (band energies, crest factor, kurtosis, sideband prominence) are persisted, and only after the redactor's existing rules confirm they cannot reverse-identify a host.

---

## 7. Open questions / followups for v0.7+ planning

1. **Mic enumeration and selection** when multiple are present. PipeWire and PulseAudio both expose multiple devices; the user may have a webcam mic, a headset mic, and a deliberately-installed measurement mic. The setup wizard needs to let the user pick one. Defaulting to the highest-SNR mic by spec sheet is unreliable because the spec sheet is not always available.

2. **Chassis-mounted vs desk-mounted positioning**. The SNR budget improves dramatically with chassis-internal mic placement, but most users will not do that. Investigate whether a low-cost adhesive-mount mic (ECM8000-style) sold as a ventd accessory is worth the support burden.

3. **Fleet-aggregated reference vs per-host reference**. Per-host is the right default, but for the homelab use case where a user has ten identical Beelink mini-PCs, a fleet-shared reference would let new units bootstrap without a healthy-baseline calibration. R20 (fleet federation) is the natural place to plug this in.

4. **Streaming vs batch detection**. The pseudocode above is batch (run during calibration only). A future version could run the detector continuously during normal operation, alerting on developing faults *between* calibrations. This requires more careful CPU budgeting (the FFTs in §4.1.2 are not free) and should not be attempted before v0.8.

5. **Hardware accelerator integration**. On systems with a Intel GNA, NPU, or DSP block, the FFT and statistics computation could run off-CPU. Out of scope for v0.7; relevant for v0.9+.

6. **Coupling-group attribution with two or more mics**. With two mics positioned at known distances from the chassis, time-difference-of-arrival (TDOA) localisation can attribute an anomaly to a specific channel in a coupling group. This is well-studied in array-signal-processing literature but is a v0.8+ feature at the earliest.

7. **Validation against MIMII**. Before shipping, run the §4.4 detector against MIMII fan and pump subsets and report AUC. If AUC < 0.7, the detector is not worth shipping; if 0.7–0.85, ship as advisory; if > 0.85, consider promoting to "high-confidence" status. The MIMII paper gives baseline AUCs around 0.80–0.85 for fans and 0.75–0.85 for pumps, so the target is to land in that range.

8. **Acoustic fingerprint as a calibration validator**. R22 covers "signature persistence across upgrades"; an acoustic fingerprint of the chassis-fan ensemble could detect a kernel update that silently broke fan control (PWM writes accepted but fans not actually responding) by noticing that the acoustic spectrum no longer changes when PWM changes. Speculative; document as a future possibility.

---

## 8. Citations and primary sources

The list below covers the standards, papers, datasets, and open-source projects that ground the technical claims in this document.

### Standards

1. ISO 13373-1:2002. *Condition monitoring and diagnostics of machines — Vibration condition monitoring — Part 1: General procedures.* https://www.iso.org/standard/21831.html
2. ISO 13373-2:2016. *Condition monitoring and diagnostics of machines — Vibration condition monitoring — Part 2: Processing, analysis and presentation of vibration data.* https://www.iso.org/standard/68128.html
3. ISO 13373-3 / ISO 13373-9:2017. *Vibration condition monitoring — Diagnostic guidelines / Diagnostic techniques.*
4. AMCA Standard 300 — Reverberant Room Methods of Sound Testing of Fans (2008/2024 revisions). https://www.amca.org/publish/publications-and-standards/amca-standards/amca-standard-300-24-reverberant-room-methods-of-sound-testing-of-fans.html
5. ASHRAE Handbook, Chapter 49 "Noise and Vibration Control". https://handbook.ashrae.org/Handbooks/A23/IP/A23_Ch49/a23_ch49_ip.aspx
6. IEC 61672-1:2013 (A-weighting). Cited in R18; relevant here for ambient-floor weighting.

### Bearing-fault diagnostics

7. Antoni, J. (2006). *The spectral kurtosis: a useful tool for characterising non-stationary signals.* Mechanical Systems and Signal Processing, 20(2), 282–307. (Foundational for the kurtogram approach used in §4.1.2.)
8. Randall, R. B., & Antoni, J. (2011). *Rolling element bearing diagnostics — A tutorial.* Mechanical Systems and Signal Processing, 25(2), 485–520. (Cited in R18 §5; canonical tutorial.)
9. Heng, R. B. W., & Nor, M. J. M. (1998). *Statistical analysis of sound and vibration signals for monitoring rolling element bearing condition.* Applied Acoustics, 53(1–3), 211–226.
10. Brüel & Kjær Application Note BR-1763. *Detecting Faulty Rolling-Element Bearings.* https://www.bkvibro.com/fileadmin/mediapool/Internet/Application_Notes/detecting_faulty_rolling_element_bearings.pdf
11. ACOEM. *Understanding Bearing Fault Frequencies (BPFO, BPFI, BSF, and FTF).* https://acoem.us/blog/vibration-analysis/bearing-fault-frequencies/
12. IoT Bearings. *Understanding Bearing Defect Frequencies: BPFO, BPFI, BSF, and FTF Explained.* https://iotbearings.com/bearing-defect-frequencies-bpfo-bpfi-bsf-ftf-explained/
13. Pachaud, C., Salvetat, R., & Fray, C. (1997). *Crest factor and kurtosis contributions to identify defects inducing periodical impulsive forces.* Mechanical Systems and Signal Processing, 11(6), 903–916.
14. Wang, D. (2018). *Optimal sub-band analysis based on the envelope power spectrum for effective fault detection in bearing under variable, low speeds.* Sensors, 18(4), 1100. https://pmc.ncbi.nlm.nih.gov/articles/PMC5981466/
15. Reliability Connect. *Bearing Problems — Fault Frequency & AI Methods.* https://www.reliabilityconnect.com/bearing-problems-fault-frequency-and-artificial-intelligence-based-methods/

### Fan and turbomachinery aeroacoustics

16. Wikibooks. *Engineering Acoustics / Noise from cooling fans.* https://en.wikibooks.org/wiki/Engineering_Acoustics/Noise_from_cooling_fans
17. Wikibooks. *Engineering Acoustics / Noise from turbine blades.* https://en.wikibooks.org/wiki/Engineering_Acoustics/Noise_from_turbine_blades
18. AIVC. *Aerodynamical noise of fans.* https://www.aivc.org/sites/default/files/members_area/medias/pdf/CR/CR01_Aerodynamical_noise_of_fans.pdf
19. Tannoura, N. et al. (2022). *Aerodynamic and aeroacoustic properties of axial fan blades with slitted leading edges.* Acta Acustica. https://acta-acustica.edpsciences.org/articles/aacus/full_html/2022/01/aacus220013/aacus220013.html
20. ASME GT2010. *Detection of Stall Regions in a Low-Speed Axial Fan: Part I — Azimuthal Acoustic Measurements.* https://www.researchgate.net/publication/267501995
21. ASME GT2010. *Detection of Stall Regions in a Low-Speed Axial Fan: Part II — Stall Warning by Visualisation of Sound Signals.* https://asmedigitalcollection.asme.org/GT/proceedings/GT2010/43987/181/347460
22. Carolus, T., et al. (Siegen IFTSM). *Unsteadiness of blade-passing frequency.* https://www.mb.uni-siegen.de/iftsm/forschung/veroeffentlichungen_pdf/136_2014.pdf
23. Sound and Vibration. *Understanding Fan Sound — December 2014.* http://www.sandv.com/downloads/1412eich.pdf

### Pump cavitation

24. Dong, L., Liu, J., Liu, H., Dai, C., & Gradov, D. V. (2019). *Detection of inception cavitation in centrifugal pump by fluid-borne noise diagnostic.* Shock and Vibration, 2019, 9641478. https://onlinelibrary.wiley.com/doi/10.1155/2019/9641478
25. Sheikh, T. (2025). *Evaluation the low cost of vibration and acoustics techniques based on novel cavitation detecting in axial pumps by varying load conditions and statistical method.* Scientific Reports. https://www.nature.com/articles/s41598-025-01731-7
26. Mousmoulis, G., et al. (2019). *The application of acoustic emission for detecting incipient cavitation and the best efficiency point of a 60 kW centrifugal pump.* Mechanical Systems and Signal Processing.
27. MDPI Processes. (2023). *A review of pump cavitation fault detection methods based on different signals.* Processes, 11(7), 2007. https://www.mdpi.com/2227-9717/11/7/2007
28. Cernetic, J. (2009). *Use of noise and vibration signal for detection and monitoring of cavitation in kinetic pumps.*
29. Corsair. *AIO: How to fix rattling or bubbling sounds in AIO cooler.* https://help.corsair.com/hc/en-us/articles/360045227371

### Datasets

30. CWRU Bearing Dataset. Case Western Reserve University Bearing Data Center. https://www.kaggle.com/datasets/brjapon/cwru-bearing-datasets
31. NASA / IMS Bearing Dataset. NASA Prognostics Center of Excellence Data Repository. https://data.nasa.gov/dataset/ims-bearings and https://www.nasa.gov/intelligent-systems-division/discovery-and-systems-health/pcoe/pcoe-data-set-repository/
32. Purohit, H., Tanabe, R., Ichige, K., Endo, T., Nikaido, Y., Suefusa, K., & Kawaguchi, Y. (2019). *MIMII Dataset: Sound Dataset for Malfunctioning Industrial Machine Investigation and Inspection.* arXiv:1909.09347. https://arxiv.org/abs/1909.09347 and https://zenodo.org/records/3384388
33. Dohi, K., Imoto, K., Harada, N., Niizumi, D., et al. (2022). *MIMII DG: Sound Dataset for Malfunctioning Industrial Machine Investigation for Domain Generalization Task.* https://zenodo.org/records/6529888
34. Tanabe, R., Purohit, H., et al. (2021). *MIMII DUE: Sound Dataset for Malfunctioning Industrial Machine Investigation and Inspection with Domain Shifts.* IEEE.
35. DCASE Challenge. (2020–2025). *Task 2: Unsupervised Anomalous Sound Detection for Machine Condition Monitoring.* https://dcase.community/challenge2025/task-first-shot-unsupervised-anomalous-sound-detection-for-machine-condition-monitoring and predecessor years.
36. Dohi, K., et al. (2024). *Description and Discussion on DCASE 2024 Challenge Task 2.* arXiv:2406.07250. https://arxiv.org/abs/2406.07250

### Open-source software

37. PyOD. yzhao062/pyod. https://github.com/yzhao062/pyod
38. scikit-multiflow. https://scikit-multiflow.github.io/ (now succeeded by River.)
39. MIMII Baseline. MIMII-hitachi/mimii_baseline. https://github.com/MIMII-hitachi/mimii_baseline
40. python-sounddevice. spatialaudio/python-sounddevice. https://github.com/spatialaudio/python-sounddevice
41. SciPy spectrogram and Welch PSD reference. https://docs.scipy.org/doc/scipy/reference/generated/scipy.signal.spectrogram.html
42. AudioMoth USB Microphone (OpenAcousticDevices). https://github.com/OpenAcousticDevices/AudioMoth-USB-Microphone
43. Hussain-Aziz/Machine-Sound-Anomaly-Detector. https://github.com/Hussain-Aziz/Machine-Sound-Anomaly-Detector

### Consumer-fan and AIO context

44. Comair-Rotron. *Cooling fan noise: sleeve bearing vs ball bearing.* https://www.comairrotron.com/m/article/cooling-fan-noise-sleeve-bearing-vs-ball-bearing.html
45. GamersNexus. *The basics of case fan bearings.* https://gamersnexus.net/guides/779-computer-case-fan-bearing-differences
46. Gerhard, A. (2006). *Experimental study of the noise emission of personal computer cooling fans.* Applied Acoustics. https://www.sciencedirect.com/science/article/abs/pii/S0003682X06000065
47. EveZone. *AIO Pump Noise Solved: Is It Air Bubbles or Failure?* https://evezone.evetech.co.za/build-lab/aio-pump-noise-bubbles-vs-failure-guide/

### Microphone and SPL background

48. Wikipedia. *A-weighting.* https://en.wikipedia.org/wiki/A-weighting
49. TDK InvenSense Application Note AN-1112. *Microphone Specifications Explained.* https://invensense.tdk.com/wp-content/uploads/2015/02/AN-1112-v1.1.pdf
50. Analog Devices. *Understanding Microphone Sensitivity.* https://www.analog.com/en/resources/analog-dialogue/articles/understanding-microphone-sensitivity.html

---

## 9. Summary for the spec author

If you are writing the actual `--acoustic` spec slice, the load-bearing decisions in this research are:

1. **Ship the §4.4 simplest detector first.** Broadband-rise + crest-factor + kurtosis with a 2-of-3 gating rule and a per-(channel, RPM) reference. Catches the dominant fault class (bearing wear) with acceptable false-positive rate. Don't ship the §4.1 envelope spectrum or the §4.2 flutter detector until the simple detector has fleet data.

2. **Make the feature explicitly opt-in and explicitly advisory.** No control-loop coupling. No refusal of calibration. No fan-curve modification based on acoustic verdicts.

3. **Validate against MIMII before shipping.** If the simple detector cannot achieve > 0.7 AUC on MIMII fan and pump subsets, the detector is not ready and the feature should slip a release.

4. **Refuse-to-run gates are critical.** Ambient SPL too high, mic AGC enabled, mic > 50 cm from chassis — all must produce `INSUFFICIENT_SNR` rather than silent false-positive verdicts.

5. **The reference store is per-host, indexed by an acoustic_fingerprint** (DMI product_name + mic USB VID:PID + hwmon_fingerprint). Re-warm on any change.

6. **No raw audio to disk.** Only summary statistics. The redactor's existing rules (R28-master + spec-pr2c-04..10) extend to cover summary-stat persistence.

7. **The spec must include a "what this won't catch" section** verbatim from §6.1, so the user understands they are getting a stall-verification feature, not a fault-classification feature.

The work is bounded. The mic has no calibration. The detector has no ground truth in the field. The output is one of three verdicts per channel per PWM step, aggregated to one of four post-sweep summaries per channel. That is the honest envelope.
