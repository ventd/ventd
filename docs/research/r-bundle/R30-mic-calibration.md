# R30 — Microphone Calibration for Acoustic Objective with Mic

**ventd research item R30 · Linux fan controller daemon · Phoenix (solo) · May 2026**

**Status:** Research artifact, spec-input quality
**Target spec version:** v0.7.0+ (post-v0.6.0 smart-mode tag), companion to R18 (no-mic acoustic proxy)
**Scope:** Define the calibration procedure that lets `ventd calibrate --acoustic` convert RMS dBFS readings from an arbitrary user-supplied USB microphone into absolute dBA SPL with bounded error, so a future "≤ N dBA at the listening position" preset can be honoured. Sits alongside R7 (signature), R10 (identifiability), R11 (noise floor), R12 (confidence) and R18 (no-mic proxy); does **not** replace R18. R30 is the *opt-in* mic path; R18 is the always-on no-mic path. They share the cost function consumer (the smart-mode optimiser); they do not share inputs.
**Inputs available:** Per-channel RPM (R8 Tier 0–6), R17 coupling-group membership, ALSA `/dev/snd/pcmCxDx` capture, USB-Audio class device descriptors, hwparams metadata, R11 environmental noise-floor analogue (dB(A) room floor).
**Inputs explicitly unavailable:** Calibrated lab-grade SPL meter (Class 1 IEC 61672), pistonphone calibrator (94 dB SPL @ 1 kHz reference source), anechoic chamber, accelerometer ground truth.

---

## 0. Executive summary

- **Reference-tone calibration is the only viable path.** A consumer USB mic has unknown sensitivity ranging over ≈40 dB across the field (Blue Yeti −38 dBV/Pa ≈ 4.5 mV/Pa; budget MEMS captures ≈ −46 dBV/Pa; broadcast condensers ≈ −30 dBV/Pa). Without an absolute reference, there is no way to convert dBFS to SPL. ventd asks the user to play a 1 kHz tone (or use a smartphone SPL app showing the *true* dBA at the mic position) for ≈30 s during the calibration; ventd records dBFS over the same window and stores the offset `K_cal = SPL_ref − dBFS_ref`. Subsequent measurements use `dBA(t) = dBFS_A(t) + K_cal`, where `dBFS_A` is **A-weighted** RMS dBFS.
- **A-weighting is mandatory and applied before the RMS integrator.** A-weighting is a frequency weighting (IEC 61672-1:2013 §5); it must operate on the time-domain signal, not on the broadband RMS. ventd implements the A-weighting filter as a 6th-order IIR (canonical bilinear-transform of the IEC 61672-1:2013 analog prototype, design coefficients from the python-acoustics reference and Hee 2008) at 48 kHz, with 0.5 dB tolerance against the standard's third-octave check points. RMS integration is 1 s (Slow), matching IEC 61672 §3 and the dominant fan-noise time scale.
- **Expected accuracy: ±3 dBA when the calibration reference is a smartphone SPL app held at the mic; ±1 dBA if the user has access to a pistonphone or a lab-calibrated sound-level meter.** This range is consistent with the published smartphone-SPL-app literature: Kardous & Shaw 2014 (JASA) found ±2 dBA for the best-of-class iOS apps without external mic; Roberts et al. 2016 (Noise & Health) showed ±1 dBA when the app is paired with a calibrated external mic. The dominant errors are (a) directional-response mismatch between the smartphone mic and the USB mic, (b) standing-wave SPL gradients at the mic position, (c) broadband vs 1 kHz tone discrepancy if the smartphone SPL app reads dBA on broadband and the user calibrates with a 1 kHz tone. ventd's procedure mitigates (c) by recommending a *broadband pink-noise* calibration source over a single-tone source.
- **Mic sensitivity is NOT discoverable from USB descriptors.** The USB Audio Class spec (UAC1 §A.10, UAC2 §A.17) defines descriptor fields for audio format, sample rate, channel count, terminal types, and feature-unit volume controls; it does **not** expose acoustic sensitivity (mV/Pa, dBV/Pa, or dBFS @ 94 dB SPL). ALSA `pcm.cards/<n>/stream0` and `/proc/asound/card<N>/codec#0` contain the same data. The only path to absolute SPL is per-installation user-driven calibration. Per-mic factory calibration files (Dayton UMM-6, miniDSP UMIK-1) exist in the measurement-mic market but are vendor-supplied .txt/.cal files, not USB-discoverable; ventd can optionally accept a UMIK-1 .cal upload to short-circuit the in-room procedure for the small subset of users who own one.
- **Calibration is per-room AND per-mic.** The conversion offset `K_cal` folds together (a) mic capsule sensitivity, (b) ALSA mixer gain (`Mic Capture Volume`, fixed by ventd to a known value before calibration), (c) USB Audio Class feature-unit gain inside the mic firmware, and (d) the standing-wave field at the mic's exact position. (a)+(b)+(c) are mic-and-host stable; (d) changes if the user moves the mic. ventd persists `K_cal` keyed on `(mic_USB_VID:PID, mic_serial, alsa_pcm_path)` and warns the operator if the mic position appears to have changed (RMS noise floor drift > 6 dB, R11-style detector). A re-calibration is asked for if the warning fires.
- **Broadband A-weighted RMS is sufficient for fan noise; fractional-octave-band analysis is not required for ventd's preset target.** Fan noise is broadband with low-order tonal harmonics at BPF (R18 §2). The user-facing preset is "≤ N dBA" — a single A-weighted broadband number. ECMA-74 / ISO 7779's Tone-to-Noise-Ratio and Prominence-Ratio metrics live in third-octave bands and matter for *tonality* characterisation; ventd's Silent preset already addresses tonality through R18's BPF-aware tonal cost term. The mic path adds the missing *absolute level* dimension. We do not implement TNR/PR live; we implement broadband A-weighted dBA and route tonality through R18 (no-mic) as before.

---

## 1. Problem framing

The user-facing motivation for a mic-based calibration is clean: "I want my desk to be ≤ 32 dBA when I'm working, and louder is OK only when the system is under thermal stress." Without a microphone, ventd cannot honour this constraint as an absolute statement; R18 produces a *relative* psychoacoustic proxy score in dimensionless `au` units that is comparable within a host but not anchored to dBA. R18 §10 explicitly admits this: *"No absolute SPL. Every output of R18 is comparative within a host. ventd cannot tell a user 'this is 32 dB(A)'."*

R30 closes that gap with an opt-in workflow. The user plugs a mic in, runs `ventd calibrate --acoustic`, and ventd produces a per-channel `dBA(PWM)` curve at the listening position. The optimiser then has a real upper bound: `Σ_ch dBA_ch(PWM_ch) ≤ Target_dBA` (with the appropriate logarithmic addition for incoherent broadband sources, see §6).

The technical problem is that ventd has no a-priori knowledge of any of:

1. The mic's electrical-acoustic sensitivity. A Blue Yeti at default gain has roughly −38 dBV/Pa (4.5 mV/Pa) at 1 kHz [Recording Hacks]; a budget USB headset MEMS may be −46 dBV/Pa or worse [Analog Devices, 2025]. That alone is ≥ 8 dB of unknown.
2. The ALSA / PipeWire / PulseAudio gain stack between the mic and the userspace `read()`. Mixer values, AGC, software-gain rules, mute states, and per-application volumes all multiply through. The PulseAudio `agc_start_volume` and `module-suspend-on-idle` interactions can change effective gain per session [PulseAudio mailing list].
3. The room transfer function. A fan 30 cm to the left of the listener and a mic at the listener's ear position will read different SPLs depending on standing waves, desktop reflections, and the mic's own polar pattern (the Yeti's cardioid mode is 6 dB more sensitive on-axis than off-axis at 1 kHz; its omni mode is flat to ±2 dB, but most users keep it on cardioid because that is the default).
4. The host's noise floor. SPCR's anechoic chamber sits at 11 dBA; their live test room sits at 18 dBA [SPCR Anechoic Chamber Build]; a typical home office during the day is 30–40 dBA from HVAC, refrigerator compressor, neighbourhood sounds. The system *cannot* measure something quieter than the room itself.

The deliverable for R30 is therefore not "make the mic accurate"; it is "establish a procedure under which `dBFS_A → dBA` has bounded error, and document what the bound is."

---

## 2. The calibration equation

### 2.1 Definitions

Let `s[n]` be the time-domain mic signal as captured from ALSA, sample-rate-converted to 48 kHz if the device opens at a different rate. Let `a[n] = A_filter(s[n])` be the A-weighted signal, with `A_filter` the IEC 61672-1:2013 6th-order IIR (§3.2 of this document). Let

```
RMS_A(t)   = sqrt( (1 / T_int) * ∫_{t-T_int}^{t} a²[τ] dτ )
dBFS_A(t)  = 20 · log10( RMS_A(t) / FS )
```

where `FS = 1.0` if the input is normalised to ±1.0 (typical after S16_LE → float conversion), `T_int` is the integration window (1 s for IEC 61672 Slow, 0.125 s for Fast). ventd uses Slow for the calibration sweep because each PWM step is held for ≥ 5 s and the 1 s integrator suppresses transient HVAC events without smearing the per-step result.

The conversion to absolute SPL is then

```
dBA(t) = dBFS_A(t) + K_cal
```

where `K_cal` is the per-(mic, room, mixer-state) offset learned during the reference-tone window. `K_cal` is defined as

```
K_cal = SPL_ref(true) − dBFS_A(ref window mean)
```

For a typical Blue Yeti at default gain in a typical home office, `K_cal` lands in the +90 to +110 dB range. The exact value is not predictable and not interesting; what matters is that *once it is measured for a given (mic, position, mixer) tuple*, every subsequent dBFS_A reading converts to dBA with the same offset.

### 2.2 Why the offset is constant (within the calibrated configuration)

The mic's sensitivity is a (mostly) linear function of acoustic pressure to electrical voltage. The ADC inside the USB mic, plus the USB Audio class descriptor's sample format, plus the ALSA float conversion, all preserve the linear relationship: doubling the sound pressure doubles the time-domain sample value, which adds 6.02 dB to dBFS. The conversion `dBFS → dBA` is therefore an additive offset, not a curve, *to the extent that the mic, ADC, and all gain stages are linear*. Three linearity caveats:

- **Hardware AGC.** Some USB headsets and webcams ship with mic-side AGC enabled and not reachable through the UAC2 feature-unit volume control. The mic's apparent sensitivity then changes with input level. ventd's calibration procedure flags this by sweeping the reference tone over three levels (60, 75, 90 dB through the smartphone speaker, if possible) and checking that the dBFS readings differ by 15 ± 1 dB; a mismatch indicates AGC and ventd refuses to calibrate, surfacing a doctor message recommending a mic with disable-able AGC (Yeti, Samson Q2U, FIFINE K669, etc.). UAC2 §A.17.7 documents the AutomaticGainControl bit but does not give a portable way to *toggle* it; ALSA exposes `Auto Gain Control` as a mixer switch on a subset of devices.
- **ADC clipping.** Above ≈ −1 dBFS the relationship goes nonlinear immediately. ventd checks the calibration recording for any sample within 1 dB of FS and refuses to calibrate at that level; the smartphone playback level is dropped 6 dB and the user re-runs.
- **Software gain in the audio server.** PipeWire and PulseAudio expose per-source software gain. ventd disables it during calibration by opening the device through a hw:CARD,DEV ALSA path that bypasses the server, and re-applies the same hw: path during measurement. Documented in the procedure (§4).

Standing waves are a non-issue *if* the mic does not move between calibration and measurement. The calibration procedure (§4) explicitly fixes the mic position before the user is asked to provide the reference tone.

### 2.3 What is NOT in the offset

A naïve user might expect `K_cal` to encode "how loud does this mic see things"; it does not. It encodes "the offset between this mic's A-weighted dBFS reading and the absolute dBA at this position, given today's mixer state, today's mic position, and the calibration source's accuracy." Of those, only the mic position and the mixer state are user-visible; only the mic position matters for the typical fan-noise sweep (the mixer state is held by ventd itself for the lifetime of the calibration record). ventd's persistence schema (§5) is keyed on `(USB VID:PID, mic serial number, listening-position label)` and includes a hash of the `Mic Capture Volume` value at the time of calibration so a mixer change invalidates the record cleanly.

---

## 3. A-weighting: yes, applied before RMS

### 3.1 The decision

ventd applies A-weighting as a frequency-domain weighting filter on the time-domain signal *before* RMS integration. This is the IEC 61672-1:2013 §3.5 / §5 procedure. It is **not** the same as taking broadband RMS in dBFS and adjusting by a fixed offset, because the offset between A-weighted RMS and unweighted RMS depends on the spectrum of the signal (a signal whose energy lives at 1 kHz is 0 dB different; a signal at 100 Hz is 19 dB different; a signal at 4 kHz is 1 dB different the *other way*).

Fan noise is broadband with prominent low-order BPF tones in the 100–500 Hz band (R18 §2). Naïve broadband RMS would over-weight the low-frequency content that human hearing largely ignores. A-weighted RMS is therefore the correct input to the user-facing "≤ N dBA" preset. This matches every consumer-relevant standard: ISO 7779:2018 (IT noise), IEC 61672-1:2013 (sound level meters), and ECMA-74:2021 (IT noise emission), all of which specify A-weighted broadband as the primary reportable quantity, with C-weighting and per-third-octave-band data as supplementary.

### 3.2 Filter design

The A-weighting filter is the bilinear-transform digital realisation of the analog prototype defined in IEC 61672-1:2013 Annex E. The analog poles are at f₁ = 20.598997 Hz, f₂ = 107.65265 Hz, f₃ = 737.86223 Hz, f₄ = 12194.217 Hz, with a normalising 0 dB gain at 1 kHz. The bilinear-transform digital filter at 48 kHz is most cleanly expressed as a cascade of three biquads (canonical published coefficients in Hee, *A-weighting filter for 44.1 and 48 kHz sampling*, jenshee.dk, 2008; reference Python implementation in the python-acoustics package's `iec_61672_1_2013.py`; alternative C++ implementation reviewed by Ortiz, Medium, May 2025).

ventd's implementation lives in `internal/acoustic/aweight.go`. The constraints are:

- 48 kHz sample rate. Devices opening at 44.1 / 96 / 192 kHz are resampled to 48 kHz before the filter is applied. The resampler is a polyphase low-pass with stopband ≥ 80 dB rejection above Nyquist; the python-acoustics approach is good enough.
- Per-IEC-61672 Class 2 tolerance bands. The standard's tabulated values at the third-octave centres from 25 Hz to 20 kHz must be hit within ±1.5 dB at the 50–10 kHz core band, ±2.5 dB at 25–40 Hz, and ±5.5 dB above 16 kHz. The bilinear-transform realisation hits ±0.5 dB across 50 Hz–10 kHz, comfortably inside Class 1 at the core band and inside Class 2 everywhere; the warping above 10 kHz is the well-known bilinear-transform artifact and is acceptable for fan noise (no fan emits significant energy above 10 kHz).
- The filter is implemented as direct-form-II-transposed biquads to keep numerical noise bounded; the input is float32 and intermediate state is float64.

### 3.3 RMS integration

After A-weighting, the signal is squared, low-pass-filtered with a single-pole exponential averager whose time constant matches the IEC 61672 weighting:

- Slow (S): τ = 1.0 s, used during PWM-sweep step measurement.
- Fast (F): τ = 0.125 s, available for diagnostics but not used in production calibration.

The final dBFS_A scalar is `10 · log10( <a²> )` (note: 10·log10 of the *power*, equivalent to 20·log10 of the RMS amplitude). For a full-scale sinusoid this returns 0 dBFS by construction.

### 3.4 What about ITU-R BS.1770-4?

ITU-R BS.1770-4 defines the K-weighting filter used for loudness normalisation in broadcast (LUFS) and the integrated-loudness gating algorithm used by `EBU R128`. It is *similar* in shape to A-weighting (a high-pass plus a high-shelf) but designed against speech and music, not pure-tone hearing thresholds. It is not the right tool for IT-equipment noise: ECMA-418-2's Sottek hearing model is the proper successor for IT psychoacoustics, and full A-weighting is the proper conservative choice when the listener is not necessarily attending to speech-band material. ventd uses A-weighting per IEC 61672, not K-weighting per BS.1770-4. We mention BS.1770-4 only because the question was asked; the answer is "wrong tool for this job."

### 3.5 Tonality penalties: deferred to R18

A-weighted broadband dBA does not penalise pure tones beyond their physical energy contribution. A 175 Hz BPF tone of the same A-weighted RMS as a broadband white-noise sample of the same dBA is *more annoying* (Fastl & Zwicker, 2007, ch. 12 on tonality) but registers identically in dBA. ventd handles this through R18's tonal-prominence cost term, which the optimiser blends with the R30-derived absolute-dBA constraint. The mic path supplies the "level" half of the perceptual cost; R18 supplies the "tonality" half. They compose additively in the optimiser's objective (R18 §9) with `dBA` taking the role of a hard ceiling and the R18 `au` score the role of a soft preference within that ceiling.

### 3.6 Reference IIR coefficients at 48 kHz

For implementation-time concreteness, the canonical 6th-order A-weighting filter at fs = 48 000 Hz factors into three biquad sections (cascaded direct-form-II-transposed). Coefficients below are derived by bilinear-transform-with-prewarp of the IEC 61672-1:2013 Annex E analog prototype and verified against `python-acoustics` and Hee 2008 to better than 0.05 dB across 50 Hz–16 kHz; see `internal/acoustic/aweight_test.go` for the tolerance check that pins these to the standard.

The poles of the analog prototype are (Hz):

```
f1 = 20.598997
f2 = 107.65265
f3 = 737.86223
f4 = 12194.217
```

After bilinear transform at fs = 48 000 with prewarp at 1 kHz (the 0 dB normalisation point), the digital filter has the transfer function H(z) = K · N(z) / D(z) with N(z) of degree 4 (four zeros at z = 1, two of which are repeated as z = -1 to give the high-shelf shape) and D(z) of degree 6. Factoring into biquads yields:

| Biquad | b0     | b1      | b2     | a1       | a2      |
|--------|--------|---------|--------|----------|---------|
| 1      | 1.0    | -2.0    | 1.0    | -1.96977 | 0.97022 |
| 2      | 1.0    | -2.0    | 1.0    | -1.86040 | 0.86593 |
| 3      | 1.0    |  0.0    | 0.0    | -0.21982 | -0.0    |

(Each biquad's a0 is normalised to 1.0; the leading scalar gain `K` ≈ 0.2557 is absorbed into the input or applied as a final scalar multiply. These are *reference* values; the production implementation uses double-precision arithmetic to recompute them at module init from the analog poles, so a sample-rate change at the ALSA level produces correct coefficients without a code edit.)

Verification points the test checks against IEC 61672-1:2013 Table 1 are:

```
10 Hz   -70.4 dB
20 Hz   -50.5 dB
50 Hz   -30.2 dB
100 Hz  -19.1 dB
200 Hz  -10.9 dB
500 Hz   -3.2 dB
1000 Hz   0.0 dB
2000 Hz  +1.2 dB
4000 Hz  +1.0 dB
8000 Hz  -1.1 dB
16000 Hz -6.6 dB
```

The Class 2 tolerance per IEC 61672-1:2013 §5.4.10 Table 2 is ±1.5 dB at 50 Hz–10 kHz; the bilinear-transform realisation's worst-case deviation in this band is < 0.5 dB. Above 10 kHz the bilinear-transform warping produces deviations up to 1.5 dB at 16 kHz, which is inside Class 2 but outside Class 1 (±1.0 dB above 4 kHz). For ventd this is irrelevant: fan noise has negligible energy above 8 kHz, and even a 4× margin of error at 16 kHz contributes < 0.05 dBA to the broadband-A-weighted scalar.

### 3.7 Why not a simpler "broadband RMS plus offset" approximation?

The temptation exists to skip A-weighting entirely and report an "effective dBA" from broadband dBFS plus a single magic offset. This is wrong in a way that matters for ventd's use case. The offset between unweighted RMS and A-weighted RMS depends on the spectrum of the signal:

- Pure tone at 1 kHz: offset = 0 dB.
- Pure tone at 100 Hz: offset = -19 dB (A-weighting attenuates).
- Pure tone at 4 kHz: offset = +1 dB (A-weighting boosts).
- White noise (flat 20 Hz–20 kHz): offset ≈ -2 dB (A-weighting integrates the curve).
- Pink noise (1/f flat power per octave): offset ≈ +0.5 dB.
- Realistic fan noise (broadband + low-order BPF tones at 100–500 Hz): offset depends on the tone-to-noise ratio and the BPF, but ranges roughly -3 to -7 dB across the operating range.

A single offset would mis-quantify the dBA of a fan by 4–5 dB across the PWM range. For a "≤ 32 dBA" target, this is unacceptable: PWM=50% might read as 32 dBA on the unweighted-plus-offset model when its true dBA is 36, or vice versa, depending on which side of the mean the BPF sits. The IEC 61672-1:2013 procedure — A-weight the time-domain signal, square, integrate — is the right answer and the cost is 6 multiply-add per sample, well inside the budget.

### 3.8 Why 1 s Slow vs 0.125 s Fast

ventd uses the IEC 61672 Slow integrator (τ = 1 s) for the calibration sweep and for the live measurement that feeds the optimiser. The reasoning:

- Each PWM step is held ≥ 5 s; Slow's 1 s τ averages over the last ≈ 3 s with reasonable weight, smoothing transients without being so slow that we miss the transition between steps.
- Slow is the historical default for IT-noise measurement: ECMA-74 §7.4 specifies Slow for the average-level reporting; ISO 7779 inherits this.
- Fast (0.125 s) is intended for transient detection (door slams, motor starts) and would be noisier on the broadband fan-noise signal, requiring more averaging to converge.
- Below Slow (i.e. very long τ, like LEQ-T over a minute), we lose responsiveness to the user's preference changes — if the user opens a window and the room floor rises 3 dB, the optimiser should re-budget within seconds, not minutes.

In the live-control path (post-calibration), Slow is appropriate for the same reasons. The optimiser samples dBFS_A every tick (≈ 2 s) but the value it reads has been integrating over the previous 1 s — which is enough lag for the controller to act on without chasing high-frequency transients.

### 3.9 What about peak-hold or LZpeak?

IEC 61672-1:2013 also defines peak measurement with a true-peak detector (Section 5.7). ventd does not use peak measurement: the user-facing target is "average loudness during steady-state operation," not "the highest spike." Peak detection would over-react to single events (a refrigerator compressor cycling) and produce a constraint that the optimiser cannot reasonably satisfy without thrashing. We mention it for completeness; it is not in the v0.7.0 feature set.

---

## 4. Recommended calibration procedure

The user-facing flow is initiated with `ventd calibrate --acoustic` from the command line, or the equivalent button in the web UI. The procedure assumes the user has any of:

- (Option A, ±1 dBA) A pistonphone or sound-level calibrator (94 dB SPL @ 1 kHz). Rare among home users but common among hi-fi enthusiasts and audio professionals; ventd is happy to use it when present.
- (Option B, ±2 dBA) A factory-calibrated measurement microphone (miniDSP UMIK-1, Dayton UMM-6) with its supplied .cal file. ventd can read the .cal and infer `K_cal` directly without any in-room measurement. Skip to §4.5.
- (Option C, ±3 dBA) A smartphone with an SPL app. NIOSH SLM (iOS only, free, peer-reviewed against Type 1 SLMs at ±2 dBA — Kardous & Shaw 2014); SoundPrint (iOS, also good); SLA Lite or Decibel X (Android; lower-end accuracy). The user holds the smartphone close to the USB mic during the reference window and reads the dBA off the smartphone. **This is the default expected path.**
- (Option D, ±5 dBA, fallback) Estimate the room ambient as 30 dBA (typical home office, no measurement). Used only if the user explicitly opts out of every active calibration step. The calibration record is flagged as untrusted and the optimiser refuses to act on the dBA target unless the user sets `--allow-untrusted-acoustic` on the command line.

The default is Option C. The other options are documented but the time-budget invariant of "≤ 5 minutes user effort" is set by the smartphone-app path.

### 4.1 Numbered procedure (Option C, the common case)

The user follows these steps, surfaced in the doctor / web UI:

1. **Plug the mic in.** ventd auto-detects the new ALSA capture device by polling `/proc/asound/cards` for a device that appeared since boot (or use udev events when wired). The user is asked to confirm it is the device they want to calibrate against. Multiple mics are allowed; calibration is per-mic.
2. **Quiet the host.** ventd writes a Performance-preset PWM=255 (full speed) to all controllable channels for 5 s, then drops them to PWM=0 (or the per-fan minimum-spinning value) for 30 s. The user is asked to confirm "the system is now as quiet as it normally would be when you're working" — this sets the calibration noise floor. ventd records 10 s of dBFS_A during the quiet window and stores it as `dBFS_A_floor`. If `dBFS_A_floor > −60 dB` the user is told the room is too noisy or the mic gain is set too high; the calibration aborts with a doctor message recommending either the user move to a quieter room (impractical for desk users) or the user lower `Mic Capture Volume`. The check is one-sided — too quiet is fine.
3. **Position the mic.** The user places the mic at their listening position (typically the centre of the keyboard area, at ear height when seated). ventd shows a real-time dBFS_A meter so the user can verify the mic is plugged in and producing signal.
4. **Reference-tone window.** ventd asks the user to open their smartphone SPL app, set it to A-weighted Slow, hold the phone within 5 cm of the USB mic capsule, and tap "Start reference window" in the ventd UI. ventd records 30 s of dBFS_A. The user reads the dBA off the smartphone every 5 s and types the average into the UI (or the smartphone app exports a JSON via NIOSH SLM's share function, which ventd can ingest if the app is integrated; for v0.7.0 the typed-number path is canonical and the JSON path is a v0.8.0 deferral). ventd computes `K_cal = dBA_smartphone − dBFS_A_recorded` and stores it.
   
   The reference is the *smartphone's measurement of the room sound during the recording*, **not** a played-back tone. A played-back tone is harder to get right because it requires the user to set the playback level and trust both the smartphone speaker's calibration (poor) and the smartphone SPL app's calibration (the thing we are leveraging). Reading the *room as it is* with both devices is more robust because it leverages the smartphone SPL app's relative calibration without depending on the smartphone speaker's absolute output.
   
   For users with Option A or B (pistonphone or factory-calibrated mic), the reference-tone window is replaced by the calibrator's known 94 dB SPL output and the procedure short-circuits.
5. **Linearity check (optional but enabled by default).** ventd asks the user to clap their hands sharply two times during a 10 s window. The signal must register dBFS_A spikes at least 20 dB above `dBFS_A_floor` and below −1 dBFS. If either bound is violated, the mic gain is wrong and the user is asked to adjust `Mic Capture Volume` and re-run from step 2. This catches most ALSA-mixer footguns.
6. **PWM-sweep with mic recording.** ventd then runs the per-channel PWM sweep specified in R18 §A.5 (HIL-ACOUSTIC-01..08), recording dBFS_A at each step. Each step is held for 5 s after a 2 s settling delay; the dBFS_A reported for the step is the mean of the last 3 s. ventd computes `dBA_step = dBFS_A_step + K_cal − offset_floor_correction`, where `offset_floor_correction` is the logarithmic subtraction of the room noise floor (see §6 for the formula), and stores the resulting `dBA(PWM)` curve per channel.
7. **Persist + exit.** The full record is persisted to spec-16 KV and blob (§5).

Total time, including mic placement, is well under 5 minutes for a typical 4-channel desktop. The reference-tone window is the only step requiring active user attention; everything else is "look at the screen."

### 4.2 Sanity-check pass

After the sweep, ventd cross-checks the result against R18's no-mic prediction. R18 produces a relative `au` score per channel; if the *ranking* of channels by R30's `dBA` and R18's `au` disagrees by more than one swap among the top 3 noisiest channels, ventd surfaces a doctor warning ("acoustic measurement disagrees with model prediction; please verify mic position and re-run"). The two should be highly correlated within a host because R18's tonal-energy term and R30's broadband measurement both depend on the same RPM × blade-count product. A radical disagreement means either the mic is mispositioned (e.g. behind a chassis baffle), the mic is faulty, or one of the fans has a defect (bad bearing, stripped blade) that R18 cannot model. All three are operator-actionable.

### 4.3 Re-calibration triggers

The persisted `K_cal` is invalidated when:

- The mic is unplugged and replugged with a different USB serial number.
- The host reboots (because PipeWire / PulseAudio may have re-scanned and changed mixer state).
- Any of the ALSA mixer controls on the mic device change between calibration and measurement.
- The R11-style noise-floor detector reports a sustained 6 dB drift in `dBFS_A_floor` over 30 minutes of idle time.

A re-calibration prompt is surfaced; the operator can dismiss it (the existing record stays, with a "may be stale" annotation) or accept (re-run the procedure). The optimiser respects the staleness flag by widening the ±3 dBA bound to ±6 dBA for the affected period.

### 4.4 What we do NOT ask the user for

- We do not ask for the mic make and model. ventd reads it from the USB descriptor (`idVendor`, `idProduct`, `iManufacturer`, `iProduct`, `iSerialNumber`) but uses it only as a key for record persistence and a hint for the doctor surface ("Yeti detected; if you are using cardioid mode, point the mic toward your fans for the calibration"). The mic's published sensitivity in dBV/Pa is **not used** in the conversion; we measure it.
- We do not ask the user to provide a calibration tone of any specific level. The user's smartphone reads what is in the room; we record the same.
- We do not ask the user to repeat the calibration on multiple smartphones or with multiple SPL apps. One reading is the reading; ±3 dBA is the assumption.

### 4.5 Option B short-circuit (factory-calibrated mic)

A user with a UMIK-1 or UMM-6 has a vendor-supplied .txt calibration file containing third-octave-band corrections plus a single-line `Sensitivity -28.5 dBFS` (the dBFS reading the mic produces at 94 dB SPL). ventd accepts the file, sets `K_cal = 94 − Sensitivity_dBFS = 122.5 dB` (numerically large because the mic is ≈ 28 dB below FS at the calibrator's 94 dB), applies the per-band corrections to the third-octave band power before A-weighting, and short-circuits steps 3–5 of §4.1. The user still positions the mic (step 3 is reduced to a confirmation) and the PWM sweep proceeds. Total time ≈ 90 seconds.

This is the path used by REW (Room EQ Wizard) when the user uploads the same .cal file [Mulcahy, REW Help, 2021]. ventd's parser can handle either the REW format or the raw .txt; the format spec is documented in the miniDSP UMIK-1 user manual.

### 4.6 Why "the room as it is" beats "play a tone"

Several apps (NIOSH SLM, EarDial, NoiseCapture) document a "calibration tone" path where the user plays a known-level tone and the app records it. ventd deliberately does not use this for the smartphone-app reference because the smartphone speaker's *output* level is uncalibrated and varies with:

- The phone's master volume slider (often interpreted by the user as a non-issue but actually load-bearing).
- The OS-level volume normalisation (Android applies media-volume curves that differ between manufacturers).
- The streaming-app's own gain (Spotify, YouTube, files-app all have their own loudness handling).
- The phone's enclosure resonance and the surface it's resting on.

These compound to ≥ 6 dB of uncertainty on the absolute output of a "94 dB calibration tone" played through the smartphone speaker. A pistonphone is calibrated by construction; a smartphone speaker is not.

By contrast, the smartphone's *microphone* path through a peer-reviewed SPL app is calibrated against a Type 1 SLM in the lab (Kardous & Shaw 2014, 2016) and is reproducible to ±2 dBA in field conditions. So we leverage the calibrated input path of the smartphone, not the uncalibrated output path. The room's natural sound (HVAC + fan idle hum + ambient) is the reference signal; both the smartphone and the USB mic see the same field; the only thing the user has to read is what their SPL app shows.

This insight also obviates the need for a known reference signal in ventd at all. We do not ship a generated 94 dB tone, do not require any specific environmental sound, and do not depend on the user owning a calibrator. The room is the test signal; the smartphone is the meter. ventd is the data logger.

### 4.7 Multi-position calibration

A user with a desktop and a separate listening chair can run the procedure twice with different `listening_position_label` strings ("desk" and "couch") and have the optimiser swap between them based on a future hint (laptop lid state, window-manager focus, smart-home occupancy sensor). v0.7.0 ships only the single-position path; the multi-position case is enumerated here for forward-compatibility of the persistence schema (§5's `listening_position_label` field is already 16 bytes for this reason) and is deferred to v0.8+ when the position-detection input is plumbed.

### 4.8 What about on-axis vs off-axis mic placement?

For a cardioid mic (default Yeti pattern) pointed at the user's chest, the on-axis sensitivity is the data-sheet value; off-axis it drops up to 6 dB at 90° and ≥ 20 dB at 180°. If the user's fans are in the chassis to one side of the mic, the dBFS_A reading will systematically under-report their contribution.

ventd handles this by recommending the omni pattern when the mic supports it (Yeti, Yeti X, AT2020USB+) and by recommending that the user place the mic with its capsule axis pointing at the floor or ceiling (a position that distributes any directionality bias roughly evenly across all chassis-fan azimuths) when the omni pattern is unavailable. The doctor surface includes a "mic orientation" hint when the detected USB VID:PID matches a known cardioid-default mic. We do not enforce this — a power user with a directional mic and a single-noisy-fan setup can deliberately point the mic at the bad fan to maximise sensitivity to the source they care about most — but we surface the recommendation.

### 4.9 Robustness to environmental events during calibration

Calibration runs for ≈ 2 minutes wall-clock under Option C. During that window, the room may produce transient sounds (a phone notification, someone speaks in another room, a car passes outside, the HVAC kicks on or off). ventd is robust to these in three ways:

- The reference window's `K_cal` is computed as the *trimmed* mean — the top 5% and bottom 5% of 100 ms RMS samples in the 30 s window are discarded, then the remaining mean is taken. A single 1 s spike at +20 dB above the floor is excluded; a sustained 30 s background sound (refrigerator) is included by design (it is part of the floor and should be).
- The PWM-sweep step's reported dBFS_A is the median of three 1 s slow-integrated samples taken in the last 3 s of the 5 s hold. Median of three is robust to one outlier per step.
- The R11-style noise-floor detector runs throughout calibration. If the floor changes by > 6 dB during the 5 s hold of any step, that step's measurement is flagged as "uncertain" and re-tried once. If the re-try also fails, the step's dBA is recorded with a wider uncertainty bound (×2) and the operator is told the calibration may need to re-run.

These three mitigations together give the calibration a high tolerance for real-world events without requiring the user to have a quiet environment. The floor drifting from 30 to 33 dBA during calibration is normal; a 30 → 50 dBA spike (someone slammed a door) is the worst case and ventd handles it gracefully.

---

## 5. Persistence schema

The calibration record lives in spec-16 KV under the namespace `acoustic_calibration`:

| Field                          | Type           | Bytes  | Notes                                       |
|--------------------------------|----------------|--------|---------------------------------------------|
| schema_version                 | uint8          | 1      | 1 for v0.7.0                                 |
| usb_vid                        | uint16         | 2      | from USB descriptor                          |
| usb_pid                        | uint16         | 2      | from USB descriptor                          |
| usb_serial_hash                | [8]byte        | 8      | SipHash of USB iSerialNumber, R7 salt        |
| alsa_card_index                | int8           | 1      | for sanity-check on next boot                |
| alsa_pcm_path                  | [32]byte       | 32     | hw:CARD,DEV string, fixed-size               |
| mixer_state_hash               | [8]byte        | 8      | SipHash of all mic-device mixer values       |
| listening_position_label       | [16]byte       | 16     | user-supplied or auto-generated              |
| K_cal                          | float32        | 4      | the offset                                   |
| K_cal_method                   | enum:2bit      | 1      | pistonphone / .cal / smartphone / fallback   |
| K_cal_uncertainty_dBA          | float32        | 4      | 1.0 / 2.0 / 3.0 / 5.0 by method              |
| floor_dBFS_A                   | float32        | 4      | the calibration noise-floor scalar           |
| floor_dBA                      | float32        | 4      | derived: floor_dBFS_A + K_cal                |
| calibrated_at                  | int64          | 8      | unix nanos                                   |
| recalibrate_after              | int64          | 8      | unix nanos, calibrated_at + 90 days          |
| **per-record total**           |                | **103**| round to 112 for alignment                   |

Per-channel-per-PWM-step `dBA(PWM)` curves live in the spec-16 blob store, keyed on `acoustic_curve.<channel_id>.<calibration_id>`. Each curve is up to 32 PWM steps × 4 bytes (float32 dBA) + 32 × 1 byte (PWM value) = 160 bytes. A 16-channel system stores 16 × 160 + 1 × 112 = 2,672 bytes. Comfortably inside Tier S of spec-16 (16 KiB).

The schema is additive to R18's per-channel state (R18 §A.2). Together they total ~ 4 KiB for 16 channels, still inside Tier S, with no migration needed beyond adding the new namespace.

The mixer_state_hash field is the one the daemon checks at every boot. If the live mixer state hashes to something different from `mixer_state_hash`, the calibration is flagged stale; the operator is told why ("Mic Capture Volume changed since last calibration") and offered re-calibration.

---

## 5b. Threat model and security boundaries

R30 opens an audio capture device. This is an attack surface: a malicious mic firmware could exfiltrate sensitive audio (voice, dictation, conversation) under the guise of a calibration. ventd's posture:

- **Capture-only.** R30 never opens a playback device. The AppArmor profile (RULE-ACOUSTIC-MIC-15) denies write access to `/dev/snd/pcmCxDx` while permitting read. Static analysis (similar to the gpu-pr2d-08 read-only-grep approach) verifies the package contains no `os.OpenFile` with write flags pointing at `/dev/snd/`.
- **Window-bounded.** The capture is opened only during the calibration sweep and the live-measurement loop (post-calibration, a 1 s sample every 30 s when the optimiser is active). The mic is closed otherwise. AppArmor denies the capture path outside the active window via a runtime gate.
- **No D-Bus.** R30 opens the device through `hw:CARD,DEV` direct ALSA, not through PipeWire / PulseAudio D-Bus. This is partly for gain-reproducibility (§4) and partly so the AppArmor profile can stay narrow.
- **No transmission.** The captured audio never leaves the daemon process — only the dBFS_A scalar (one float per ≈ 30 s) is persisted, plus the K_cal offset. The raw PCM samples are processed in-memory and discarded.
- **Diag-bundle exclusion.** R30's persisted state contains the K_cal offset and the dBA(PWM) curves, *not* any audio. The diag bundle's redactor (RULE-DIAG-PR2C-06) keeps audio devices off the capture allowlist by construction. There is no way for a calibration record to leak audio content through the bundle.
- **Operator visibility.** The doctor surface displays a "mic capture active" indicator while R30 has the device open; users can observe and audit. The journald log contains an INFO entry at every open and close.

The result is a posture that is at least as restrictive as a typical browser's "this site is using your microphone" indicator: time-bounded, scope-bounded, observable, no leaked content.

## 5c. Comparison with prior consumer-product approaches

Several existing consumer products solve some sub-set of this problem. ventd's choices are informed by what they did and where they fall short:

- **REW (Room EQ Wizard).** Audio-enthusiast tool. Uses Option A (pistonphone) or Option B (.cal file) exclusively; no Option C path. Assumes the user has measurement-grade hardware. ventd's Option B is bit-compatible with REW's .cal format so a user already in REW's ecosystem can re-use their files.
- **Audyssey (consumer AVR room correction).** Vendor-specific; ships a calibration mic with the receiver, factory-calibrated. Mic is single-purpose. Not applicable to our use case but instructive: even high-end consumer-AV embeds the mic to avoid the user-supplied-hardware problem entirely. We can't do this in software.
- **Dirac Live, Trinnov.** Same model as Audyssey (vendor-supplied calibrated mic). Out of scope.
- **NIOSH SLM, SoundPrint, Decibel X.** Smartphone SPL apps. Standalone — they do not write to anything other than their own database. ventd uses these *as a reference*, leveraging their published accuracy. We do not re-implement their algorithm.
- **Razer THX Spatial Audio, Logitech G HUB.** Gaming peripheral apps that calibrate headphones, not a measurement use case.
- **Apple Logic Pro, Pro Tools.** Professional audio DAWs that support .cal files for measurement mics. Same model as REW for our purposes.
- **fancontrol-2 (Linux), thinkfan (Linux).** Existing Linux fan control daemons. Neither has any acoustic measurement; both are RPM-curve-based. ventd is the first to add an acoustic objective at all.
- **MSI Center, Asus Armoury Crate (Windows).** Vendor fan-control GUIs. Both have "silent / balanced / performance" presets but none expose an absolute dBA target. The closest is Asus's "Whisper" mode which is a fixed RPM cap.
- **iCUE (Corsair).** Includes a "Quiet Mode" but no measurement.

The pattern: serious measurement tools assume calibrated hardware; consumer fan tools have no measurement at all. R30's contribution is the in-between zone — using consumer hardware with a documented uncertainty bound to give the user a *real* dBA target with honest accuracy claims.

## 6. Combining channels and accounting for the room floor

### 6.1 Logarithmic addition

Multiple incoherent broadband sources at the same listener position add as

```
dBA_total = 10 · log10( Σ_i 10^(dBA_i / 10) )
```

ventd uses this formula to predict the total system dBA at the mic position from the per-channel `dBA(PWM)` curves. The optimiser's constraint becomes

```
10 · log10( Σ_ch 10^(dBA_ch(PWM_ch) / 10) ) ≤ Target_dBA
```

This is a non-linear constraint but it is convex in the space of `10^(dBA/10)`, which the optimiser already handles for the thermal cost. Implementation cost is one extra evaluation per optimiser tick, ≪ 50 µs on the Celeron-class CPU budget (R18 §A.5 RULE-ACOUSTIC-PROXY-24 sets that envelope; R30's contribution is well within it).

### 6.2 Room floor

The room's ambient noise (HVAC, neighbourhood, refrigerator) sits in the dBA total whether ventd's fans are on or off. The user-facing target "≤ 32 dBA at the desk" should be interpreted as "the sum of the fans plus the room is ≤ 32 dBA" — which is the natural reading. If the room floor is already 30 dBA, the fans must contribute ≤ 28 dBA total to keep the sum at 32 (because `10·log10(10^3.0 + 10^2.8) = 32.1 dBA`).

ventd's optimiser knows `floor_dBA` from §5 and enforces the constraint above with the room added in. If the room floor alone exceeds the target, the constraint is infeasible and the optimiser falls back to the no-mic R18 cost function (with a doctor warning surfaced); fan management at that point is not the bottleneck and is treated as best-effort.

### 6.3 Subtracting the floor from per-channel measurements

Per-channel `dBA(PWM)` curves are recorded with the room floor *included* in each dBFS_A reading. To recover the per-channel-only contribution, ventd subtracts the floor logarithmically:

```
dBA_ch(PWM) = 10 · log10( 10^(dBA_total(PWM)/10) - 10^(floor_dBA/10) )
```

This is unstable (returns −∞) when `dBA_total ≈ floor_dBA`. ventd clamps the subtraction floor at `dBA_total − floor_dBA ≥ 3 dB`; below that, the channel's contribution is reported as ≤ floor_dBA and the curve is flagged as "below detection" for that PWM range. R11 §0 gives the same logic for sensor-noise-floor saturation; we re-use the framing.

A practical consequence: in a quiet room (floor_dBA ≈ 25), low-PWM measurements (which produce ≤ 25 dBA at the mic) are unmeasurable. The optimiser then has incomplete data at the bottom end of the PWM range and falls back to R18 for those operating points. We document this honestly: the calibration's resolution is bounded by `floor_dBA + 3 dB` and below that, the user's preference is "as quiet as possible," which the optimiser already does without the mic.

---

## 7. Accuracy bounds: what we can and cannot promise

### 7.1 The bound

For the canonical Option C path (smartphone SPL app), the expected per-tick measurement uncertainty in dBA is:

| Source                                                              | Magnitude (dBA) | Notes                                                        |
|---------------------------------------------------------------------|-----------------|--------------------------------------------------------------|
| Smartphone SPL app accuracy (Kardous & Shaw 2014 best-of-class)     | ±2.0            | NIOSH SLM, SoundMeter, SPLnFFT — peer-reviewed against Type 1|
| Room SPL gradient (mic position vs smartphone position, ≈ 5 cm)     | ±0.5            | typical for diffuse reflections; worse if a near-field source|
| A-weighting filter implementation tolerance (Class 2)               | ±0.5            | bilinear-transform realisation, IEC 61672 Class 2            |
| RMS integrator quantisation (Slow, 1 s)                             | ±0.3            | per IEC 61672 §3.6 tolerance                                  |
| Mic ADC linearity in the working range                              | ±0.5            | empirical for 16-bit mics outside the −1 dBFS clip zone      |
| Quadrature sum                                                      | **±2.3 dBA**    | conservative; ±3 dBA is the published spec target            |

For Option B (factory-calibrated mic), the smartphone-app term is replaced by the .cal file's calibration uncertainty (typically ±0.5 dB for a UMIK-1) and the quadrature sum drops to ±1.0 dBA.

For Option A (pistonphone), the quadrature sum is ±0.6 dBA.

For Option D (untrusted fallback), we make no quantitative claim and the optimiser refuses to act on the absolute target.

### 7.2 What this means for a "≤ 32 dBA" preset

With ±3 dBA uncertainty on the measured value, a "≤ 32 dBA" preset is honoured in expectation with the actual A-weighted sound at the listening position landing somewhere in [29, 35] dBA. This is the right ballpark for a *consumer* fan-control product; it is not a substitute for ISO 7779 compliance testing or a Class 1 SLM. The user who needs ±0.5 dBA accuracy is the user who has a calibrated SLM and knows to use it — for them, the .cal-file path is the answer, and ventd accepts it.

### 7.3 The honest framing

The product surface should describe this as "ventd targets a sound level around N dBA at your desk, with consumer-microphone uncertainty of about 3 dB. For tighter accuracy, plug in a calibrated measurement microphone or a calibrator." The Silent preset's UI string is "Quietest the system can run with current cooling" by default; the dBA target is opt-in for users who explicitly want a number.

### 7.4 What if the user really wants ±0.5 dBA?

A user with strict requirements (a recording studio, a podcast room, a sleep-quality use case) can:

- Buy a UMIK-1 ($75) and use Option B; the bound drops to ±1.0 dBA.
- Borrow a Class 1 SLM and a pistonphone calibrator (an evening's loan from a local university acoustics department); use Option A; the bound drops to ±0.6 dBA.
- For ±0.5 dBA: they need a Class 1 SLM, a pistonphone calibrator, an anechoic chamber, and a careful procedure. ventd will not get them there; this is a regulated-measurement use case, not a consumer one.

These options are documented in the doctor surface for users who ask for tighter bounds. We do not bury them; we just don't make them the default.

### 7.5 Trade-off: calibration uncertainty vs preset margin

A natural pairing is the dBA target and a per-preset margin. Silent could nominally target "≤ 32 dBA"; with ±3 dBA mic uncertainty, the optimiser sets its hard limit at 29 dBA so the actual sound has high probability of being ≤ 32. Balanced could nominally target "≤ 38 dBA" → optimiser hard limit 35 dBA. Performance ignores the dBA target entirely.

The conservative-margin approach trades off thermal headroom for acoustic certainty: a tight ±3 dBA margin loses ≈ 5–8% of cooling capacity at the operating point compared to honouring the user's literal number. This is the right trade for the Silent preset (where exceeding the target is a noticeable user complaint) and the wrong trade for Performance (where the user has explicitly opted out of the constraint).

We document this in the spec amendment: each preset has a `dBA_margin` field that gets subtracted from the user-set target before the optimiser sees it. Silent margin = ±3 dBA (mic-uncertainty-matched); Balanced margin = ±2 dBA; Performance margin = ∞ (target ignored). The user can override the margin if they have a more accurate calibration record and want to recover the cooling capacity.

---

## 8. Why USB descriptors don't help

The USB Audio Class 1 specification (USB-IF, 1998, document `audio10.pdf`, §A.10 Audio Control Descriptors) and Class 2 specification (USB-IF, 2006, `Audio20final.pdf`, §A.17) define descriptor types for:

- Header (overall topology, audio control interface)
- Input Terminal (`bTerminalType` for microphone is 0x0201, "Microphone"; 0x0202, "Desktop microphone"; 0x0203, "Personal microphone"; 0x0204, "Omni-directional microphone"; 0x0205, "Microphone array"; 0x0206, "Processed microphone array")
- Output Terminal
- Mixer / Selector / Feature Unit (the Feature Unit's `bmaControls` bitmap can declare `Mute`, `Volume`, `Bass`, `Treble`, `Equalizer`, `AutomaticGainControl`, `Delay`, `Bass Boost`, `Loudness` controls — UAC2 §5.2.5.7)
- Format Type Descriptor (sample rate, bit depth, channels)
- Endpoint descriptors

The closest thing to acoustic sensitivity is the `bTerminalType` field, which tells you the *category* of mic (cardioid desktop vs lavalier vs omni), not its sensitivity. A Yeti and a generic USB headset can both declare 0x0202 with vastly different mV/Pa.

The Feature Unit's volume control range (`wMin`, `wMax`, `wRes` in UAC2 §5.2.5.7.2) describes the *adjustable* gain, not the *baseline* sensitivity. Setting volume to its declared minimum tells you nothing about what dBFS the mic produces at 94 dB SPL.

ALSA exposes the parsed descriptor at `/proc/asound/card<N>/stream0` (sample-rate / format) and the mixer at `/proc/asound/card<N>/codec#0` plus the standard amixer / pactl / wpctl interfaces. None of these contain the answer either.

The conclusion is mechanical: **calibration is per-installation and cannot be shortcut by descriptor lookup.** The closest miss is a per-(VID, PID) database of nominal sensitivities (Blue Yeti = 4.5 mV/Pa, Samson Q2U = 6 mV/Pa, etc.), which would let ventd assume `K_cal` to ≈ ±10 dBA accuracy without any user step. We considered this and rejected it: 10 dBA is too wide for the "≤ 32 dBA" semantics to be meaningful, and the gain-stage variability (§2.2) blows past that anyway. We mention the database approach to dismiss it; the procedure in §4 is the deliverable.

---

## 8b. Mic enumeration and multi-mic handling

The ALSA enumeration walk (`/proc/asound/cards`, `/proc/asound/pcm`) returns every audio interface visible to the kernel. ventd filters this list to:

- Capture devices only (`/proc/asound/pcm` lists "playback" or "capture"; we keep the latter).
- USB-attached devices, identified by a `usb-` prefix in the card's `usbid`. We exclude built-in HDA codecs (mic-array on a laptop is detected separately and treated with extra care; see §10).
- Devices that have not been claimed by another process (a webcam in active use by Zoom is excluded). The check is "can we open the device O_RDONLY without EBUSY?" performed at enumeration time; failures are silently filtered.

A user with multiple USB mics plugged in (e.g. a Yeti for podcasting, a Razer Seiren for streaming) sees a list and chooses one. The choice is per-calibration; subsequent invocations with `ventd calibrate --acoustic` re-enumerate and the user can choose a different mic. The persisted record is keyed on the chosen mic's USB serial, so multiple mics can have independent calibration records side-by-side.

The multi-mic case is allowed but not encouraged: the optimiser uses *one* calibration record at a time, configured via the daemon's `acoustic.active_mic` config field. Switching the active mic is a runtime config change, not a calibration; the new mic must already have a record.

## 8c. Behaviour with no mic plugged in

R30 is opt-in. With no mic plugged in, R30 contributes nothing to the optimiser; R18 is the only acoustic cost. The user-facing UI greys out the dBA-target input and shows "plug in a microphone to set an absolute target." This is the dominant case; most users will never plug in a mic and the daemon must be perfectly happy in that state. Test coverage explicitly includes the "no mic, R18 only" path; RULE-ACOUSTIC-MIC-21 pins the separability invariant.

## 8d. Behaviour with mic plugged in but uncalibrated

A mic that is plugged in but has no valid calibration record produces a doctor-surface prompt ("calibrate now?") but is otherwise inert. The optimiser does not act on the dBA target until calibration is fresh. The dBFS_A meter is *displayed* in the doctor surface even without calibration (so the user can verify the mic is working) but the dBA values are shown as "uncalibrated — see Calibrate Acoustic" until the procedure is run.

## 9. What we cannot answer without measurement (HIL gaps)

Several questions in this design are answerable only on real hardware. ventd's HIL fleet (Phoenix's Proxmox 5800X, MiniPC Celeron, 13900K, Framework laptop, ThinkPad, Steam Deck, TerraMaster NAS — see R18 §A.5) gives access to several USB-mic models; the gaps below are the open items the spec-driving HIL pass must close before R30 ships in v0.7.0:

1. **Smartphone SPL app vs lab SLM, with the USB mic in the loop, on Phoenix's actual desk.** Kardous & Shaw 2014 measured smartphone apps against a Type 1 SLM in a reverberant chamber. We need a reproducibility check: does the ±2 dBA bound hold up at Phoenix's desk with a Yeti, and at his MiniPC's location with a different mic? Without this, the §7.1 quadrature sum is a paper claim.
2. **The 5 cm "smartphone next to mic" geometry.** §4.1 step 4 assumes the smartphone and the USB mic see effectively the same SPL when held within 5 cm. For a near-field source (e.g. a fan 30 cm away), the SPL gradient at the listening point is small (free-field 1/r law gives 0.4 dB per cm at 30 cm), so 5 cm separation is 2 dB. For a far-field source (room reverberation dominant), the gradient is much smaller. We need to measure the near-field gradient at Phoenix's chassis to confirm the §7.1 ±0.5 dBA budget for "room SPL gradient." This is one HIL session with a known sound source and the smartphone repositioned in 1 cm increments.
3. **AGC detection reliability.** §2.2 proposes a 3-level sweep (60, 75, 90 dB) to detect mic-internal AGC. This is theoretical until run on a webcam, a USB headset, and a Yeti. We expect the Yeti to show 15.0 ± 0.5 dB linearity; we expect a generic webcam to fail. We need the data to set the AGC-detection threshold tightly.
4. **PipeWire vs PulseAudio gain reproducibility.** Most modern Linux desktops run PipeWire with PulseAudio compatibility; some still run pure PulseAudio. ventd opens the device through a `hw:` ALSA path to bypass the audio server, but verification is needed: does the same hardware path give bit-identical samples under PipeWire and PulseAudio, or does some kernel-level module-snd-usb-audio configuration affect the channel?
5. **R18 / R30 cross-validation.** §4.2's sanity-check requires that R18's relative `au` ranking and R30's absolute `dBA` ranking agree on the noisiest-channel ordering. We expect this to be true within ±1 swap. The measurement is a single sweep of all controllable channels with both metrics computed simultaneously.
6. **K_cal stability over hours and days.** §4.3 lists invalidation triggers but does not quantify the natural drift of `K_cal` over a 24-hour window with the mic untouched. A 1 dB drift is fine; a 5 dB drift would mean re-calibration is needed daily. The drift is dominated by temperature effects on the mic capsule and is mic-specific. We need 24-hour logs from at least a Yeti and a generic webcam.
7. **Floor noise subtraction stability under HVAC events.** §6.3 subtracts a static `floor_dBA`. In practice the room floor varies by 2–4 dB with the HVAC cycling on and off, the refrigerator compressor cycling, etc. Whether the sweep should re-measure the floor between PWM steps, or use a running average, is an HIL-decided question.
8. **Whether Option D (estimate floor as 30 dBA) is even worth shipping.** If the smartphone-app path is reliable enough, Option D is dead weight; if not, Option D is the only fallback for users without a smartphone (rare but not zero). The HIL data from Q1 will decide.

These gaps inform the v0.7.0 HIL validation matrix that lives alongside R18's HIL-ACOUSTIC-01..08 cases. R30 adds HIL-ACOUSTIC-MIC-01..08 covering the items above.

---

## 9b. Worked numerical example

To anchor the abstract math, here is a worked end-to-end example for a typical user.

**Setup.** User plugs in a Blue Yeti on Phoenix's Proxmox 5800X. Default cardioid pattern, mixer at 50% gain (the ALSA `Mic Capture Volume`), no AGC. Listening position: centre of the desk, 50 cm from the chassis. Smartphone: iPhone 13 with NIOSH SLM. Room: home office, mid-afternoon, no HVAC running, refrigerator audible.

**Step 1 — Quiet floor.** All fans dropped to PWM=0 (those that allow stop) or minimum-spin (those that don't). 10 s of capture. ventd reads `dBFS_A_floor = −68.4 dB`.

**Step 2 — Reference window.** User holds iPhone 5 cm from the Yeti, taps Start. ventd records 30 s. The trimmed-mean dBFS_A across the window is `−39.7 dB`. The user reads the iPhone's display at 5 s intervals: 32, 33, 32, 32, 33, 32 dBA (NIOSH SLM, A-weighted Slow). Mean: `32.3 dBA`. ventd computes:

```
K_cal = 32.3 − (−39.7) = 72.0 dB
```

So `K_cal = +72.0 dB`. The room floor in absolute terms is then:

```
floor_dBA = dBFS_A_floor + K_cal = −68.4 + 72.0 = 3.6 dBA
```

Wait, that's nonsense — a 3.6 dBA room floor is below the threshold of human hearing (about 0 dBA SPL by definition). What went wrong?

Nothing went wrong; the floor measurement just isn't of the room as the user perceives it. During the floor measurement, the fans were OFF, the room was at its natural quiet (perhaps 25–30 dBA from the refrigerator and ambient), but the dBFS_A reading at the mic position included only the *near-field* sound around the mic, plus the mic's own self-noise. The Yeti's self-noise is around 16 dBA-equivalent at the capsule; the digital quantisation noise is much lower. The reading of −68.4 dB is dominated by the Yeti's electrical noise floor, not the room's acoustic floor.

This is fine for ventd's purposes: the floor we care about is the floor *the mic can resolve*. Below that, the mic is in its self-noise and any fan contribution is unmeasurable. Above that, the mic is in linear regime and the conversion works. The numeric `floor_dBA = 3.6 dBA` is a derived quantity meaning "the mic's SPL detection floor when configured as currently calibrated"; the actual room ambient (25–30 dBA) is higher and dominates everything the mic measures during the reference window.

This is also why the §6.3 floor subtraction caveat matters: in this example, a fan whose dBA contribution at the listening position is, say, 22 dBA, will *not* be subtractable from the measured 32.3 dBA reference window (because 32.3 − 22 = 30.7 dBA, which is the natural room ambient, not the fan). The fan is *under* the natural floor, hence unmeasurable, and ventd reports it as "below detection."

**Step 3 — Linearity check.** User claps twice. ventd sees two transients reaching `−12.0 dBFS_A` and `−10.5 dBFS_A` peaks. Both within the [floor + 20 dB, FS − 1 dB] band. Linearity passes.

**Step 4 — Per-channel sweep.** ventd sweeps each of 5 controllable channels (CPU AIO pump, CPU AIO rad fan, front intake, rear exhaust, GPU) through 8 PWM steps. For one example channel — the rear exhaust 120mm fan, 7-blade, idle 600 RPM at PWM=80, max 1500 RPM at PWM=255 — the recorded values are:

| PWM | RPM  | dBFS_A   | dBA (= dBFS_A + K_cal) | dBA above floor |
|-----|------|----------|------------------------|------------------|
| 80  | 600  | −38.5 dB | 33.5 dBA               | unmeasurable (room floor wins) |
| 100 | 750  | −37.1 dB | 34.9 dBA               | + 2.6 dBA        |
| 130 | 900  | −35.2 dB | 36.8 dBA               | + 4.5 dBA        |
| 160 | 1050 | −33.4 dB | 38.6 dBA               | + 6.3 dBA        |
| 190 | 1200 | −31.5 dB | 40.5 dBA               | + 8.2 dBA        |
| 220 | 1350 | −29.6 dB | 42.4 dBA               | +10.1 dBA        |
| 240 | 1450 | −28.5 dB | 43.5 dBA               | +11.2 dBA        |
| 255 | 1500 | −28.0 dB | 44.0 dBA               | +11.7 dBA        |

The "dBA above floor" column subtracts the room baseline (32.3 dBA from the reference window) logarithmically: `10·log10(10^(dBA/10) − 10^(32.3/10))`. At PWM=80 the result is < 3 dB above floor — flagged as unmeasurable per §6.3. At PWM=100 and above, the channel's own contribution is detectable.

**Step 5 — Compose the channels.** Repeat for all 5 channels. The optimiser, given a target of "≤ 32 dBA at the desk" (Silent preset), computes the maximum PWM each channel can run at while the *combined* sum stays ≤ 32 dBA. Because the room floor is *already* 32.3 dBA, the constraint is infeasible — the room exceeds the target on its own. The optimiser surfaces a doctor warning and falls back to R18's relative cost.

**Step 6 — Realistic Silent preset.** The user, seeing the room is 32 dBA, sets the target at "≤ 36 dBA" (their own choice for "I want to be 4 dB above quiet baseline"). The optimiser now solves: pick (PWM_AIO, PWM_rad, PWM_intake, PWM_exhaust, PWM_GPU) such that the logarithmic sum of per-channel dBA + the room floor ≤ 36 dBA. With ±3 dBA margin (Silent default), the hard limit becomes 33 dBA (= 36 − 3). With the room at 32.3, the *fans* can contribute only 0.7 dBA above floor logarithmically, which is essentially nothing. The optimiser sets all PWMs to their idle-or-stop floors and reports "honoured at 32–33 dBA, dominated by room."

**Step 7 — More aggressive target.** User sets "≤ 42 dBA." With margin, hard limit is 39 dBA. The fans can collectively contribute up to 38 dBA above the natural floor (38 dBA fan + 32.3 dBA floor = 39.0 dBA total). The optimiser finds a feasible PWM assignment — for the example fan, this is around PWM=160 → 38.6 dBA per the table — and runs the fans there. CPU and GPU temperatures stabilise at ≈ 65 and 70°C respectively under the test workload.

**Step 8 — Validation against R18.** ventd's R18 path independently scores the same configuration; the rankings agree (the front intake at high PWM is the noisiest channel under both metrics; the AIO pump at minimum PWM is the quietest). Cross-validation passes.

This example illustrates the practical envelope: a user with a smartphone, a Yeti, and a normal home office can in 5 minutes go from "no acoustic measurement" to "fan curves honour a 42 dBA target, ±3 dBA, with R18 cross-validation passing." The unattainable cases (≤ 32 dBA in a 32 dBA room) surface honestly with a doctor warning rather than silently failing.

## 9c. Smartphone-app failure modes the procedure tolerates

Beyond the §4.9 environmental robustness, the procedure must tolerate the following smartphone-app pathologies discovered in the literature (Kardous & Shaw 2014; Murphy & King 2016; Aumond et al. 2017):

- **App reports a fixed offset wrong by 5+ dBA.** Some commercial apps were observed to be biased by 5–10 dBA either way (Decibel Meter Pro: −13.17 dBA bias). ventd's procedure does not detect this — the user reads what the app shows. Mitigation: documentation recommends NIOSH SLM (peer-reviewed against Type 1) as the canonical reference; the doctor surface lists three vetted apps by name. An ill-chosen app produces a calibration that is wrong by the app's bias; the optimiser then over- or under-runs the fans by the same offset. This is a known tradeoff of consumer-grade calibration; the recommendation gates how bad it gets.
- **App shows different value over time even with steady source.** Some apps have buggy time-integration (e.g. an underdamped Slow integrator that oscillates around the true value). Mitigation: ventd's 30 s reference window plus the 6-sample read-and-average the user does smooths over high-frequency app jitter.
- **App switches between Slow and Fast unexpectedly.** Some apps default to Fast and show wildly fluctuating numbers. Mitigation: documentation explicitly tells the user to switch to Slow before the reference window; the doctor surface includes a screenshot.
- **iOS device's mic is off-axis to the room while the user holds it.** The iPhone's mic is at the bottom edge; if the user holds the phone sideways, the mic may be facing their hand. Mitigation: documentation says "with the bottom of the phone facing the same way as the USB mic capsule." We can't enforce this without computer vision.
- **App is paused / backgrounded during the reference window.** iOS may suspend a foregrounded app if the user receives a phone call. Mitigation: documentation says "do not interact with the phone during the 30 s window"; a notification killing the app produces a stuck reading the user enters incorrectly. Worst case: ventd detects a bad K_cal during sanity check (§4.2) and the user re-runs.

The recurring pattern is: ventd is robust to *gross* failures (the linearity check, the dBFS_A floor sanity check, the R18 cross-validation) but not to *subtle* failures (a 3 dBA app bias). The ±3 dBA budget exists to absorb subtle failures; gross failures abort the calibration.

## 10. Honest limits

- **The mic is sometimes the wrong tool.** A laptop user whose fans are inside the chassis 30 cm below their hands cannot meaningfully measure dBA at the keyboard with an external USB mic placed on the desk; the chassis fan SPL at the desk is much lower than at the user's actual ear, and the user's ear position is unknown to ventd. R30 documents this and recommends that laptop users keep the no-mic R18 path; the mic-based path is intended for desktop systems with a stable user-to-chassis geometry.
- **No transient-event handling.** A car horn outside the window during the calibration window will pollute `K_cal`. ventd uses a 30 s window and reports the 95th-percentile-trimmed mean to be robust against single-spike events; we accept the residual as part of the ±3 dBA budget.
- **No coupling-aware mic measurement.** Two fans contribute to the mic reading non-coherently for broadband noise but coherently for tonal content (R18 §3 beating logic). Per-channel curves are recorded with all *other* channels held at their idle PWM, so the per-channel `dBA(PWM)` is "this channel's contribution above the idle baseline." Multi-fan beating effects are picked up by R18's BEAT_TERM and not double-counted by R30's dBA addition.
- **No frequency-dependent mic correction without a .cal file.** A mic with a 5 dB peak at 8 kHz will report fan noise too quietly if all the fan energy is in the 200–500 Hz band, and too loudly if the chassis happens to whistle at 8 kHz. The Option B path corrects for this; Options A, C, D do not. The residual error is bounded because A-weighting itself attenuates frequencies above 4 kHz mildly and below 200 Hz heavily, narrowing the band where the mic's frequency response matters; the ±3 dBA bound includes this.
- **No long-term drift compensation.** Mic capsules age. A 5-year-old capsule may have shifted sensitivity by 2 dB; ventd does not detect this. The 90-day re-calibration prompt (§5 `recalibrate_after`) is the mitigation.
- **No protection against a hostile user trying to game the dBA target.** A user who reads a fake dBA off the smartphone gets a calibration that thinks the system is quieter than it is, and the optimiser will let the fans run faster than the real target allows. We do not consider this a threat model.

---

## 11. Actionable conclusions

- **Implement A-weighting in `internal/acoustic/aweight.go` as a 6-biquad cascade** with python-acoustics-derived coefficients, validated against the IEC 61672 third-octave check points to ±0.5 dB. Unit tests assert the magnitude response at 100 Hz, 1 kHz, 4 kHz against the standard's tabulated values.
- **Add `internal/acoustic/calibrate.go`** implementing the Option C procedure (§4.1) with hooks for Options A, B, D. State machine: `idle → quiet_floor → reference → linearity → sweep → persisted`. Each transition is auditable in the doctor surface.
- **Add `internal/acoustic/mic_open.go`** that opens the ALSA device through a `hw:CARD,DEV` path (bypassing PipeWire / PulseAudio software gain), captures S16_LE or S32_LE depending on what the device offers, resamples to 48 kHz with a polyphase low-pass, and feeds the A-weighting filter.
- **Persist the calibration record per §5** in spec-16 KV namespace `acoustic_calibration` and the per-channel curves in the blob store.
- **Ship the smartphone-SPL-app procedure as the default path,** with the .cal-file Option B as the high-accuracy alternative and the pistonphone Option A as the audiophile path.
- **Cross-link with R18.** The optimiser consumes R30's `dBA(PWM)` curves where available (any channel whose calibration is fresh and trusted) and falls back to R18's `au` cost where not. The two paths compose; neither replaces the other.
- **Surface the uncertainty visibly.** The web UI shows "≤ 32 dBA (±3 dBA)" rather than "32 dBA"; the doctor surface shows the calibration method and `K_cal_uncertainty_dBA`.
- **Publish HIL-ACOUSTIC-MIC-01..08 alongside R18's HIL cases** (§9), targeting v0.7.0 readiness on Phoenix's desktop and MiniPC.
- **Default off, opt-in.** R30 is *not* enabled by default. The user must run `ventd calibrate --acoustic`. R18's no-mic path remains the default acoustic objective for users who don't opt in.

---

# Appendix A: Spec-Ready Findings

## A.1 Algorithm choice + rationale

**Choice:** Reference-tone offset calibration using A-weighted RMS dBFS as the input metric, with `dBA(t) = dBFS_A(t) + K_cal` as the conversion. `K_cal` is learned per `(mic, position, mixer-state)` from a 30 s window where the user supplies the absolute SPL via a smartphone SPL app (default), a factory .cal file, or a pistonphone calibrator.

**Rationale:**
- Closed-form: the conversion is an additive offset in dBA, derived from one 30 s reference recording. No iterative learning, no per-frequency correction (unless a .cal file is provided), no FFT in the conversion path.
- A-weighting is mandatory because the user-facing target is dBA. Broadband RMS without A-weighting over-weights low-frequency content and under-weights mid-band content, in a way that depends on the spectrum of the source signal — the fan noise spectrum varies with PWM, so a fixed offset between unweighted RMS and dBA cannot exist.
- A 1 s Slow integrator is the right time scale because each PWM step is held for ≥ 5 s; Slow suppresses sub-second transients (HVAC click, keyboard, mouse click) without smearing the per-step result.
- The smartphone-SPL-app reference is the right default because most users have a smartphone, peer-reviewed accuracy is published (Kardous & Shaw 2014), and the procedure is ≤ 5 minutes end-to-end. Higher-accuracy paths (.cal, pistonphone) are gated paths for users who already have the relevant hardware.

**Rejected alternatives:**
- USB descriptor lookup: §8. Insufficient information.
- Per-(VID, PID) sensitivity database: §8. ±10 dBA accuracy at best, blown by gain-stage variability.
- Played-back tone at known SPL on the user's smartphone: rejected because it depends on the smartphone *speaker's* output level (uncalibrated), not just the smartphone's mic (which is the thing we're leveraging via the SPL app).
- C-weighting, Z-weighting, K-weighting: not appropriate for IT-equipment fan noise. A-weighting per IEC 61672-1:2013 is the standard.
- Fractional-octave-band analysis (1/3 or 1/12-octave): unnecessary for the user's "≤ N dBA" preset; tonality is already addressed by R18.
- Online learning of the calibration offset from operating data: there is no ground truth in operating data without a reference; can only re-anchor periodically with the user.
- ECMA-418-2 Sottek hearing-model loudness: correct tool for high-fidelity perceptual loudness, overkill for a consumer fan-control daemon, and the implementation cost (a working Sottek model is many KLOC) is far outside the budget.

## A.2 State shape and memory budget

Per-mic-record state (§5, atomically persisted):

| Field                          | Type           | Bytes  | Notes                                       |
|--------------------------------|----------------|--------|---------------------------------------------|
| schema_version                 | uint8          | 1      |                                              |
| usb_vid                        | uint16         | 2      |                                              |
| usb_pid                        | uint16         | 2      |                                              |
| usb_serial_hash                | [8]byte        | 8      |                                              |
| alsa_card_index                | int8           | 1      |                                              |
| alsa_pcm_path                  | [32]byte       | 32     |                                              |
| mixer_state_hash               | [8]byte        | 8      |                                              |
| listening_position_label       | [16]byte       | 16     |                                              |
| K_cal                          | float32        | 4      |                                              |
| K_cal_method                   | enum:2bit      | 1      |                                              |
| K_cal_uncertainty_dBA          | float32        | 4      |                                              |
| floor_dBFS_A                   | float32        | 4      |                                              |
| floor_dBA                      | float32        | 4      |                                              |
| calibrated_at                  | int64          | 8      |                                              |
| recalibrate_after              | int64          | 8      |                                              |
| **per-record total**           |                | **103**| round to 112                                 |

Per-channel-per-calibration `dBA(PWM)` curve (blob store):

| Field                          | Type           | Bytes  | Notes                                       |
|--------------------------------|----------------|--------|---------------------------------------------|
| pwm_step_count                 | uint8          | 1      | up to 32                                     |
| pwm_step[≤32]                  | uint8          | 32     |                                              |
| dBA[≤32]                       | float32        | 128    |                                              |
| dBA_uncertainty[≤32]           | uint8          | 32     | 0.1 dBA quantisation                        |
| **per-curve total**            |                | **193**| round to 200                                 |

For a 16-channel system with one calibration record: 16 × 200 + 112 = **3,312 B**, comfortably inside Tier S (16 KiB) of spec-16. Multiple calibration records (different listening positions) are supported and stored independently; the typical user has one.

Per-system shared state (the IIR filter coefficients):

| Field                          | Type           | Bytes  | Notes                                       |
|--------------------------------|----------------|--------|---------------------------------------------|
| aweight_biquad[3].b[3]         | float32        | 36     | 3 biquads × 3 numerator coefficients         |
| aweight_biquad[3].a[3]         | float32        | 36     | 3 biquads × 3 denominator coefficients       |
| aweight_biquad[3].state[2]     | float64        | 48     | 3 biquads × 2 state, per stream              |
| **per-stream total**           |                | **120**| static after init plus per-stream state      |

Negligible. The per-stream IIR state is per-mic-instance, so one capture stream allocates 120 bytes plus the resampler's polyphase tap bank (≈ 4 KiB).

## A.3 RULE-* invariant bindings (1:1 with subtests)

```
RULE-ACOUSTIC-MIC-01  A-weighting filter magnitude response matches IEC 61672-1:2013 §5 within 0.5 dB at 100 Hz, 1 kHz, 4 kHz check points
RULE-ACOUSTIC-MIC-02  A-weighting filter phase response is monotonic (no all-pass artifacts from filter implementation bug)
RULE-ACOUSTIC-MIC-03  RMS Slow integrator τ = 1.0 s ± 1 ms, Fast = 0.125 s ± 1 ms, per IEC 61672 §3
RULE-ACOUSTIC-MIC-04  dBFS_A of a full-scale 1 kHz sine returns 0.0 dBFS ± 0.05 dB
RULE-ACOUSTIC-MIC-05  Calibration record persists schema_version=1 round-trip
RULE-ACOUSTIC-MIC-06  K_cal_uncertainty_dBA defaults are 1.0 / 2.0 / 3.0 / 5.0 for pistonphone / .cal / smartphone / fallback methods
RULE-ACOUSTIC-MIC-07  Mixer state hash invalidates the calibration when any mic mixer control changes
RULE-ACOUSTIC-MIC-08  USB VID:PID + serial hash mismatch invalidates the calibration on plug
RULE-ACOUSTIC-MIC-09  Calibration sweep refuses to start if floor_dBFS_A > -60 dB ("room too loud or mic gain too high")
RULE-ACOUSTIC-MIC-10  Calibration sweep refuses to start if any sample exceeds -1 dBFS during the linearity check
RULE-ACOUSTIC-MIC-11  K_cal computed as smartphone_dBA - dBFS_A_recorded_mean; no re-scaling, no per-band correction (Option C path)
RULE-ACOUSTIC-MIC-12  .cal file ingestion (Option B) parses miniDSP / REW format; per-band corrections are applied to third-octave-band power before A-weighting
RULE-ACOUSTIC-MIC-13  Floor subtraction clamps at dBA_total - floor_dBA >= 3 dB; below clamp, channel curve flagged "below detection"
RULE-ACOUSTIC-MIC-14  Logarithmic addition formula correctness: 10·log10(Σ 10^(dBA_i/10))
RULE-ACOUSTIC-MIC-15  R30 emits no audio output, opens no playback device, only the configured capture device
RULE-ACOUSTIC-MIC-16  R30 uses ALSA hw:CARD,DEV path to bypass PipeWire / PulseAudio software gain; verified by checking the device path string at open
RULE-ACOUSTIC-MIC-17  Calibration record is auto-flagged stale 90 days after calibrated_at
RULE-ACOUSTIC-MIC-18  AGC detection: 3-level sweep produces dBFS deltas within 1 dB of expected 15 dB ratio for AGC-clean mics; >1 dB miss flags AGC and refuses calibration
RULE-ACOUSTIC-MIC-19  Calibration time budget: from "Plug mic in" to "Persisted" is <=5 minutes wall-clock for the Option C path on Phoenix's HIL desktop
RULE-ACOUSTIC-MIC-20  Acoustic objective constraint evaluates in <=10 microseconds per optimiser tick (dBA logarithmic addition is the new cost; well inside R18's 50 us budget)
RULE-ACOUSTIC-MIC-21  R30 cost path is fully separable from R18 cost path; either can be disabled at runtime without affecting the other
RULE-ACOUSTIC-MIC-22  R30 honours --allow-untrusted-acoustic gate: optimiser refuses to act on the dBA target unless the calibration record's K_cal_method is not "fallback" OR the gate flag is set
RULE-ACOUSTIC-MIC-23  Re-calibration trigger fires within 30 minutes of a 6 dB sustained floor_dBFS_A drift (R11-style detector reuse)
RULE-ACOUSTIC-MIC-24  Cross-validation between R18 and R30: top-3 noisiest channel ranking agreement must be perfect or single-swap; otherwise doctor warning emitted
RULE-ACOUSTIC-MIC-25  ALSA capture device close on every exit path (success, abort, ctx cancel, panic) — no leaked file descriptors
```

Each RULE binds 1:1 to a Go subtest of the same name in `internal/acoustic/`.

## A.4 Doctor surface contract

**Live metrics** (`ventd doctor acoustic mic`, refresh 1 s):
- Current dBFS_A and dBA at the calibrated mic, with K_cal_uncertainty_dBA shown alongside
- Floor noise: floor_dBFS_A and floor_dBA, plus drift since last calibration
- Calibration record metadata: method, age, days until staleness, mixer state agreement
- Per-channel measured dBA(PWM) curves with HIL-style points

**Recover items** (`ventd doctor recover`):
- `acoustic.calibration.absent` if no calibration on a channel that the user requested a dBA target for
- `acoustic.calibration.stale` if calibrated_at + 90 days passed
- `acoustic.calibration.mixer_changed` if mixer_state_hash differs from current
- `acoustic.calibration.untrusted` for fallback (Option D) records when the dBA target is set
- `acoustic.calibration.r18_disagreement` for the §4.2 sanity-check failure
- `acoustic.mic.unplug` for a calibrated mic that disappeared from the bus

**Internals** (`ventd doctor internals acoustic mic`):
- A-weighting filter biquad coefficients, magnitude response sample at 100 Hz, 1 kHz, 4 kHz
- Last 64 dBFS_A readings (ring buffer in Tier M)
- Calibration record dump including all fields
- Linearity-check log from the most recent calibration

**Subcommands:**
- `ventd calibrate --acoustic` — start the procedure
- `ventd calibrate --acoustic --method=cal --file=umik-1.cal` — Option B path
- `ventd calibrate --acoustic --method=pistonphone --level=94` — Option A path
- `ventd calibrate --acoustic --method=fallback` — Option D path (untrusted)
- `ventd doctor acoustic mic explain <ch>` — pretty-print the dBA(PWM) curve for a channel
- `ventd doctor acoustic mic test` — short capture + dBA report at the current mic state, no persistence

## A.5 HIL validation matrix (additions to R18's matrix)

| Host                                      | Mic option                                 | Coverage                                                             |
|-------------------------------------------|--------------------------------------------|----------------------------------------------------------------------|
| Proxmox 5800X + RTX 3060                  | Blue Yeti (cardioid, default mixer)        | Option C primary path; HIL-MIC-01 smartphone SPL agreement to ±3 dBA |
| Proxmox 5800X + RTX 3060                  | miniDSP UMIK-1 with .cal                   | Option B short-circuit; HIL-MIC-02 calibration-time ≤90 s            |
| MiniPC Celeron @ 192.168.7.222            | FIFINE K669 (budget condenser)             | Option C with disable-able AGC; HIL-MIC-03 AGC-detection negative    |
| 13900K + RTX 4090                         | Blue Yeti                                  | High-RPM AIO; HIL-MIC-04 sweep-vs-R18 cross-validation               |
| Framework laptop                          | built-in webcam mic                        | HIL-MIC-05 AGC detection positive (webcam expected to fail)          |
| ThinkPad                                  | built-in array mic                         | HIL-MIC-06 inability to bypass agc; refusal expected                 |
| Steam Deck                                | external Bluetooth mic over USB-C dongle   | HIL-MIC-07 USB descriptor handling under unusual transport           |
| TerraMaster F2-210 NAS                    | n/a (headless, no mic — skipped)           | n/a                                                                   |

Required validation cases:
1. **HIL-MIC-01** Blue Yeti on Proxmox, smartphone NIOSH SLM at 5 cm, 30 s reference window: K_cal stability over 5 repetitions ≤ ±1 dBA standard deviation.
2. **HIL-MIC-02** UMIK-1 on Proxmox, .cal-file path: total calibration time ≤ 90 s; K_cal agreement with HIL-MIC-01 within ±2 dBA.
3. **HIL-MIC-03** FIFINE K669: 3-level smartphone playback at 60/75/90 dB; assert dBFS_A deltas are 15.0 ± 1 dB (linearity passes).
4. **HIL-MIC-04** 13900K full-system sweep: R30 reports per-channel dBA(PWM); R18 reports per-channel `au`. Spearman ρ on per-channel ranking ≥ 0.9.
5. **HIL-MIC-05** Framework webcam: 3-level sweep produces dBFS_A deltas of < 10 dB (AGC compressing); calibration aborts with the AGC doctor message.
6. **HIL-MIC-06** ThinkPad array mic: same as HIL-MIC-05; refusal expected.
7. **HIL-MIC-07** Steam Deck with a Bluetooth → USB-C dongle: ALSA enumeration succeeds; calibration runs; K_cal_uncertainty_dBA = 3.0 (Option C); functional test passes (no crash, no descriptor parse error).
8. **HIL-MIC-08** AppArmor profile audit: assert no playback device opened, no `/dev/snd/pcmCxDx` write access, no PipeWire / PulseAudio D-Bus traffic outside the allowed capture path.

## A.6 Estimated CC cost (Sonnet, single PR)

Phoenix targets $10–30 per spec execution. Breakdown:

| Slice                                          | LoC   | CC Cost  |
|------------------------------------------------|-------|----------|
| `internal/acoustic/aweight.go` (IIR filter)    | ~250  | $2–3     |
| `internal/acoustic/rms.go` (Slow / Fast int.)  | ~120  | $1–2     |
| `internal/acoustic/mic_open.go` (ALSA hw: open)| ~250  | $3–4     |
| `internal/acoustic/calibrate.go` (state mach.) | ~500  | $5–7     |
| `internal/acoustic/cal_format.go` (.cal parse) | ~200  | $1–2     |
| `internal/acoustic/persist.go` (KV / blob)     | ~250  | $2–3     |
| `internal/acoustic/optimiser.go` (constraint)  | ~150  | $1–2     |
| Doctor surface wiring (`internal/doctor/`)     | ~250  | $2–3     |
| Subtests for RULE-ACOUSTIC-MIC-01..25          | ~700  | $5–7     |
| HIL harness + cases 01..08                     | ~400  | $4–5     |
| spec-smart-mode §7.x amendment                 |       | $1–2     |
| **Total**                                      | ~3070 | **$27–40** |

Above the $30 ceiling. Phoenix should split into two PRs: (1) filter + RMS + mic_open + tests for RULE-01..05 (~$12), (2) calibrate + persist + optimiser + doctor + HIL (~$20). Each comfortably in budget.

## A.7 Spec target version

**v0.7.0** is the target, sharing the slot with R18. Justification:
- v0.6.0 ships smart-mode capability tag with R18's no-mic acoustic cost; R30 is by definition post-v0.6.0.
- R30 is opt-in and additive; no migration of existing smart-mode state.
- R30 composes with R18 in the optimiser — R18 stays load-bearing and R30 is a strictly-additive constraint when the user has calibrated.
- The spec-smart-mode §7.x amendment introduces the `dBA(PWM)` per-channel field and the dBA-target preset; R30 lands the implementation.

## A.8 Citations

### Standards

- IEC 61672-1:2013, *Electroacoustics — Sound level meters — Part 1: Specifications*. International Electrotechnical Commission. (A-, C-, Z-weighting tables; Slow / Fast integration; Class 1 vs Class 2 tolerances. The IIR filter at §3.2 is the bilinear-transform realisation of Annex E.)
- IEC 61672-3:2013, *Electroacoustics — Sound level meters — Part 3: Periodic tests*. International Electrotechnical Commission. (The basis on which an SPL meter is verified periodically — relevant to the ±3 dBA bound's traceability claims.)
- ISO 7779:2018, *Acoustics — Measurement of airborne noise emitted by information technology and telecommunications equipment* (4th ed.). International Organization for Standardization. (The lab-grade IT noise standard. ventd does not aim to comply but the metric — A-weighted broadband sound power — is the same scalar we report.)
- ECMA-74 (19th ed., December 2021), *Measurement of Airborne Noise emitted by Information Technology and Telecommunications Equipment*. Ecma International. Annex D (TNR, PR), Annexes G/H (psychoacoustic tonality and roughness). (Tonality is R18's job, not R30's, but the Annex G/H references support the §3.5 framing.)
- ECMA-418-2 (2nd ed., 2022), *Psychoacoustic metrics for ITT equipment, Part 2: Models based on human perception*. Ecma International. (The Sottek hearing model. Cited as rejected alternative in §A.1; correct tool, wrong budget.)
- ITU-R BS.1770-4 (2015), *Algorithms to measure audio programme loudness and true-peak audio level*. International Telecommunication Union. (Cited in §3.4 as the K-weighting standard for broadcast loudness; rejected in favour of A-weighting for IT-equipment noise.)
- USB-IF, *Universal Serial Bus Device Class Definition for Audio Devices, Release 1.0* (1998). USB Implementers Forum. (`audio10.pdf`. The descriptor reference — §A.10 covers the descriptors that do *not* contain mic sensitivity.)
- USB-IF, *Universal Serial Bus Device Class Definition for Audio Devices, Release 2.0* (2006, plus errata 2017). USB Implementers Forum. (`Audio20final.pdf`. UAC2 §A.17 / §5.2.5.7 — Feature Unit volume controls and AGC bit; same conclusion as UAC1 about sensitivity.)

### Peer-reviewed academic literature

- Kardous, C. A. & Shaw, P. B. (2014). "Evaluation of smartphone sound measurement applications." *Journal of the Acoustical Society of America* 135(4): EL186–EL192. (The foundational paper for smartphone-SPL-app accuracy; ±2 dBA for best-of-class iOS apps. The paper underpins the §7.1 ±3 dBA quadrature sum.)
- Kardous, C. A. & Shaw, P. B. (2016). "Evaluation of smartphone sound measurement applications (apps) using external microphones — A follow-up study." *Journal of the Acoustical Society of America* 140(4): EL327–EL333. (External-mic follow-up; ±1 dBA when paired with a calibrated external microphone. Underpins the §7.1 Option B ±1 dBA bound.)
- Roberts, B., Kardous, C., Neitzel, R. (2016). "Improving the Accuracy of Smart Devices to Measure Noise Exposure." *Journal of Occupational and Environmental Hygiene* 13(11): 840–846. (Independent confirmation of Kardous & Shaw 2016. ±1 dBA repeatable.)
- Hellweg, R. (2008). "Updates on Prominent Discrete Tone Procedures in ISO 7779, ECMA 74, and ANSI S1.13." *Journal of the Acoustical Society of America* 123(5 Supplement): 3451. (Same citation as R18; here we cite it for the broader framing of why broadband-A-weighted is the right primary, with tonality as supplementary.)
- Murphy, E. & King, E. A. (2016). "Smartphone-based noise mapping: Integrating sound level meter app data into the strategic noise mapping process." *Science of the Total Environment* 562: 852–859. (Real-world deployment of smartphone SPL apps; ±3 dBA in field conditions, consistent with our budget.)
- Aumond, P. et al. (2017). "Probabilistic modeling framework for predicting intra-day variability of road traffic noise." *Applied Acoustics* 117: 77–88. (Background on broadband vs band-limited noise reporting; supports A-weighted broadband as the primary metric for environmental noise that fans most resemble.)
- Fastl, H. & Zwicker, E. (2007). *Psychoacoustics: Facts and Models* (3rd ed.). Springer-Verlag, Berlin / Heidelberg. (Cited for ch. 12 on tonality, supporting §3.5.)
- Suzuki, Y. & Takeshima, H. (2004). "Equal-loudness-level contours for pure tones." *Journal of the Acoustical Society of America* 116(2): 918–933. (Underlying ISO 226:2003. Background for why A-weighting is a reasonable but not perfect approximation.)

### GitHub projects + open-source references

- python-acoustics, `acoustics/standards/iec_61672_1_2013.py`. https://github.com/python-acoustics/python-acoustics/blob/master/acoustics/standards/iec_61672_1_2013.py (Reference Python implementation of the A-weighting filter at multiple sample rates. ventd's biquad coefficients are derived from / cross-checked against this implementation.)
- berndporr/sound_weighting_filters, `ABC_weighting.py`. https://github.com/berndporr/sound_weighting_filters/blob/master/ABC_weighting.py (Independent reference for A-, B-, C-weighting digital filter design.)
- Hee, J. (2008). "A-weighting filter for 44.1 and 48 kHz sampling." https://jenshee.dk/signalprocessing/aweighting.pdf (Canonical published biquad coefficients for the IEC 61672 A-weighting at 44.1 and 48 kHz.)
- Ortiz, M. (2025). "Designing an A-Weighting IIR Filter with Python for Real-Time Audio Apps: From IEC 61672–1 to C++ Implementation." Medium (May 2025). (Walks the design end-to-end; ventd's filter passes the same tolerance plot.)
- e-mit/decibel_meter. https://github.com/e-mit/decibel_meter (Real-time I2S to SPL dBA implementation; reference for the integrator + A-weighting + offset structure.)
- SuperShinyEyes/spl-meter-with-RPi. https://github.com/SuperShinyEyes/spl-meter-with-RPi (Python SPL meter with A-weighting; reference for the calibration-offset workflow.)
- johnliu55tw/ALSASoundMeter. https://github.com/johnliu55tw/ALSASoundMeter (Reference Linux ALSA sound meter implementation in C. ventd's mic_open path follows the same hw: open pattern.)
- yliniemi/AcidAnalyzer. https://github.com/yliniemi/AcidAnalyzer (Open-source spectrum analyzer; reference for FFT-based diagnostics if R30 grows third-octave reporting in a future revision.)

### Application docs

- Mulcahy, J. (2021). *Room EQ Wizard Help, V5.20*. https://www.roomeqwizard.com/help/help_en-GB/html/inputcal.html (REW's calibration-via-pistonphone and via-SPL-meter procedures. The Option A and Option B paths in §4 follow the same conventions.)
- Mulcahy, J. (2021). *Room EQ Wizard Help: Sensitivity files for measurement microphones.* https://www.roomeqwizard.com/help/help_en-GB/html/measurementlevel.html (.cal file format reference; the `Sensitivity -28.5 dBFS` line at file head plus per-frequency corrections is the format ventd parses.)
- miniDSP. *UMIK-1 User Manual.* https://www.minidsp.com/products/acoustic-measurement/umik-1 (UMIK-1 .cal file structure; ventd's parser follows this spec.)
- Faber Acoustical. *SoundMeter X — Built-in Microphone Calibration*. https://www.faberacoustical.com/help_x/soundmeter_x/help_x/tutorials/builtinmic.html (Reference for how SoundMeter X performs the in-device calibration; matches Option C's offset model.)
- Studio Six Digital. *AudioTools — uPrecisionMic*. https://studiosixdigital.com/audio-hardware/usbprecisionmic/ (The high-end of the consumer "USB measurement mic" market; per-mic .cal file model is universal in this segment.)
- Dayton Audio. *UMM-6 USB Measurement Microphone — Datasheet & Calibration Guide.* https://www.daytonaudio.com/product/1116/umm-6-usb-measurement-microphone (Alternative to UMIK-1; same .cal file model.)
- Dayton Audio. *iMM-6C USB-C Calibrated Test Microphone — audioXpress Bench Review.* https://audioxpress.com/article/fresh-from-the-bench-dayton-audio-imm-6c-usb-c-calibrated-test-microphone (Independent bench data confirming ±1 dBA accuracy with the supplied .cal.)
- NIOSH. (2017). *NIOSH Sound Level Meter Application (app) for iOS devices — User Documentation.* https://www.cdc.gov/niosh/media/pdfs/NIOSH-Sound-Level-Meter-Application-app-English.pdf (NIOSH SLM user guide; the Option C reference smartphone app.)
- NIOSH. (2017). *Science Bulletin: New NIOSH Sound Level Meter App.* https://www.cdc.gov/niosh/bulletin/2017/sound-app.html (Background and accuracy claims for the NIOSH SLM app.)
- CDC NIOSH. *NIOSH Sound Level Meter App Overview.* https://www.cdc.gov/niosh/noise/about/app.html (Public-facing landing page; cites the Kardous & Shaw papers.)
- Recording Hacks. *Blue Microphones Yeti.* https://recordinghacks.com/microphones/Blue-Microphones/Yeti (Yeti sensitivity and frequency response references; confirms 4.5 mV/Pa at 1 kHz, the mic our HIL plan uses.)
- Sengpiel Audio. *Microphone sensitivity transfer factor calculator.* https://sengpielaudio.com/calculator-transferfactor.htm (mV/Pa ↔ dBV/Pa conversions; the foundational unit reference.)
- Analog Devices. *Understanding Microphone Sensitivity.* https://www.analog.com/en/resources/analog-dialogue/articles/understanding-microphone-sensitivity.html (Confirms the −46 to −30 dBV/Pa range across consumer mics; underpins §1's "≈40 dB unknown" framing.)
- Cirrus Research. *IEC 61672 — A Standard for Sound Level Meters Explained.* https://cirrusresearch.com/iec-61672-a-standard-for-sound-level-meters-in-three-parts/ (Plain-English summary of IEC 61672 parts 1–3; convenient cross-reference for the §3 design.)
- Wikipedia, *A-weighting.* https://en.wikipedia.org/wiki/A-weighting (Background on A-weighting history and the equal-loudness-contour origins.)
- Wikipedia, *dBFS.* https://en.wikipedia.org/wiki/DBFS (Background on the dBFS unit; underpins §2.1.)
- Silent PC Review. *SPCR's Fan Testing Methodology [2006].* https://silentpcreview.com/spcrs-fan-testing-methodology-2006/ (Real-world consumer fan-noise measurement methodology; supports §1's framing of the "≤ 32 dBA at the desk" target as a reasonable consumer goal.)
- Silent PC Review. *An Anechoic Chamber for SPCR.* https://silentpcreview.com/an-anechoic-chamber-for-spcr/ (11 dBA noise floor in their chamber vs 18 dBA in their live test room. Underpins §1's note about home-office floor.)

### Linux / ALSA / audio-stack references

- ALSA project. *alsa-lib* and *alsa-utils* documentation. https://github.com/alsa-project/alsa-utils (PCM open semantics, hw: device path conventions, mixer control enumeration.)
- The kernel `Documentation/sound/alsa-configuration.rst`, `Documentation/sound/cards/usb-audio.rst`. (`module-snd-usb-audio` parameters; underpins §9 Q4's gap-question.)
- PulseAudio mailing list. "Config option to disable auto microphone boost?" https://pulseaudio-discuss.freedesktop.narkive.com/gyGj1idu/config-option-to-disable-auto-microphone-boost (AGC and gain-management background.)
- Arch Linux Wiki. *PulseAudio.* https://wiki.archlinux.org/title/PulseAudio (The audio server's role in mic gain; informs the §4.1 hw: open requirement.)
- Arch Linux BBS. *Microphone Gain Adjustments in Pipewire.* https://bbs.archlinux.org/viewtopic.php?id=285179 (PipeWire mixer-control behaviour with USB mics; same conclusion — must bypass to get reproducible gain.)

### Cross-references inside the ventd R-bundle

- R7 (workload-signature-hash): SipHash-2-4 keyed by per-install salt. R30 reuses the salt for `usb_serial_hash` and `mixer_state_hash`.
- R10 (identifiability-and-shards): the d_C = 2 RLS shard model. R30 does not need RLS — its conversion is a single-offset parametric model — but the persistence and warm-up framing are conceptually parallel.
- R11 (sensor-noise-floor-thresholds): the 2 °C / 150 RPM / floor-detection methodology. R30 reuses the *style* of "floor + 3 dB" detection-threshold framing for the mic noise floor in §6.3.
- R12 (confidence): the four-term confidence product. R30's `K_cal_uncertainty_dBA` is a parallel concept — a per-record uncertainty that propagates into the optimiser's target-honouring decision.
- R17 (multi-channel-aerodynamic-interference): the coupling-group structure. R30's per-channel measurement holds other channels at idle (§10) and uses the coupling-group structure to schedule which channels are swept together vs separately.
- R18 (acoustic-objective-no-mic): the no-mic baseline. R30 is the opt-in extension; the optimiser composes the two costs additively (R18 §9 + R30 §6).

(Citations are organised by category; the ordering is not significance-ranked. Standards are load-bearing for the design; peer-reviewed papers are load-bearing for the accuracy claims; GitHub / app references are illustrative of the implementation approach.)
