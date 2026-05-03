# Acoustic proxy — R33 no-mic loudness estimator

These invariants govern `internal/acoustic/proxy/`, the implementation of
R33's no-microphone psychoacoustic loudness proxy. The package estimates
per-fan and per-host acoustic cost from PWM/RPM/blade-pass heuristics
alone — no audio device opened, no NVML/ALSA/pulse dependencies, no
audio data emitted or persisted.

The score is dimensionless (`au`) and within-host comparable. Absolute
dBA conversion requires R30's microphone calibration (separate package).

The patch spec is `docs/research/r-bundle/R33-nomic-acoustic-proxy.md`.
Each rule below is bound 1:1 to a subtest under `proxy_test.go::TestR33Lock`.

## R33-LOCK-01: Score is dimensionless ("au") and within-host comparable; absolute dBA requires R30.

The package emits a relative score, not an absolute level. Verified by
comparing two operating points and asserting that their rank-order
holds across a uniform input scaling (diameter change in the test).

Bound: internal/acoustic/proxy/proxy_test.go:TestR33Lock/R33-LOCK-01_dimensionless_within_host

## R33-LOCK-02: Per-fan score is the four-term sum S_tip + S_tone + S_motor + S_pump.

`Score(Fan)` = `Tip(...) + Tone(...) + Motor(...) + Pump(...)`. All four
terms are non-negative; the sum is the per-fan score in au.

Bound: internal/acoustic/proxy/proxy_test.go:TestR33Lock/R33-LOCK-02_four_term_sum

## R33-LOCK-03: Broadband term scales as 50·log10(RPM·D / D_ref·RPM_ref) per Madison/AMCA.

Doubling RPM at fixed diameter adds exactly 50·log10(2) ≈ 15.05 au.
The per-class `C_tip` anchor calibrates the absolute level at the
reference operating point; diameter scaling falls out of (D/120) factor.

Bound: internal/acoustic/proxy/proxy_test.go:TestR33Lock/R33-LOCK-03_broadband_50log10_scaling

## R33-LOCK-04: Tonal term sums harmonics k ∈ {1, 2, 3} of BPF = B·RPM/60 with weights {1.0, 0.5, 0.25}.

The blade-pass tonal stack uses the canonical 6-dB-per-octave decay
envelope from Carolus 2014 / Cattanei 2021. Each harmonic contributes
at its A-weighted level, modulated by the per-class TonalityPrior and
masked by the broadband floor + per-class M_thr.

Bound: internal/acoustic/proxy/proxy_test.go:TestR33Lock/R33-LOCK-04_tonal_harmonic_weights

## R33-LOCK-05: Tonal masking — at high broadband levels, harmonics fall below M_thr and S_tone collapses.

At sufficiently high RPM the broadband term raises the masking floor
above the tonal contribution at every harmonic; `S_tone` collapses
toward zero. Verified by asserting `S_tone < 5 au` at 4000 RPM for a
case fan.

Bound: internal/acoustic/proxy/proxy_test.go:TestR33Lock/R33-LOCK-05_tonal_masking_threshold

## R33-LOCK-06: Motor whine ramps down to zero with RPM; broadband-rise above class anchor masks the floor.

`S_motor = max(0, K_motor·(1 − RPM/rpmAeroDom) − 0.5·max(0, sTip − cTip))`.
The ramp-down with RPM is the load-bearing monotonicity; the broadband
mask is computed against the *rise* above the per-class anchor (not the
absolute sTip), because the absolute mask would be dominant at every
operating point and the term would never fire. The rule's text in R33
§5.2 specifies absolute sTip; the rise-based mask is the implementation
deviation, documented in `proxy.go::Motor` with rationale.

Verified by asserting:
- laptop_blower at 1000 RPM yields a positive motor floor.
- Motor is monotone non-increasing in RPM up to rpmAeroDom.
- Motor is zero above rpmAeroDom.

Bound: internal/acoustic/proxy/proxy_test.go:TestR33Lock/R33-LOCK-06_motor_decays_with_rpm

## R33-LOCK-07: Pump-vane-tone band at f = RPM × N_vanes / 60; A-weighting clamped to non-negative; default N_vanes = 6.

`Pump` only fires for `ClassAIOPump`. The vane-tone frequency
`f_vane = RPM × N_vanes / 60`. The A-weighting at `f_vane` only ADDS to
the cost when positive (perceptually-loud band, ~1-5 kHz); negative
A-weighting (sub-300 Hz) clamps to zero so a low-frequency vane tone
does not pull the total below the unconditional `kPumpBand` floor.
This deviation from R33 §6.2's literal `kPump · A_dB(f_vane)` matches
R33's stated intent ("kPumpBand prevents the optimiser from running
the pump up arbitrarily even when the vane tone alone is masked").
A vaneCount of 0 falls back to 6.

Bound: internal/acoustic/proxy/proxy_test.go:TestR33Lock/R33-LOCK-07_pump_vane_tone_band

## R33-LOCK-08: Multi-fan composition is energetic: S_host = 10·log10(Σ 10^(S_fan/10)).

Two identical fans compose to +3.01 au; four to +6.02 au. Empty fan
slice returns 0. R17 BEAT_TERM is added downstream by the controller,
not in this package.

Bound: internal/acoustic/proxy/proxy_test.go:TestR33Lock/R33-LOCK-08_compose_energetic_sum

## R33-LOCK-09: Score evaluation never blocks on I/O.

The proxy opens no audio device, holds no audio buffer, performs no
network or disk I/O during scoring. Verified runtime-side by asserting
1000 sequential `Score` calls complete in <50 ms.

Bound: internal/acoustic/proxy/proxy_test.go:TestR33Lock/R33-LOCK-09_no_mic_dependency

## R33-LOCK-11: Default blade counts per class match R33 §2.2 — 7 / 9 / 11 / 27 / 33.

Verified by inspecting the `classes` table directly: case axial = 7,
radiator = 9, GPU shroud = 11, NUC blower = 27, laptop blower = 33,
server high-RPM = 7.

Bound: internal/acoustic/proxy/proxy_test.go:TestR33Lock/R33-LOCK-11_default_blade_counts

## R33-LOCK-12: Wrong blade count produces ≤ 8% score error and preserves within-host ranking.

Five operating points scored with blade=7 vs blade=9 must share an
identical sort order. Verified by `argsort` comparison.

Bound: internal/acoustic/proxy/proxy_test.go:TestR33Lock/R33-LOCK-12_blade_count_robustness

## R33-LOCK-13: Per-tick proxy evaluation is O(N_fans) and ≤ 4 µs/fan budget on commodity CPU.

Loose bound: 100 fans × 1000 ticks of `Compose` complete in <1 second.
Tight bound (≤4 µs per `Score` call) is implicit in the no-I/O contract
and the closed-form math.

Bound: internal/acoustic/proxy/proxy_test.go:TestR33Lock/R33-LOCK-13_per_tick_budget

## R33-LOCK-14: Proxy never opens an audio device, links no audio library, and emits no audio.

Compile-time guarantee via the package's import set (stdlib + math
only). Runtime side: `Score` and `Compose` return finite numbers for
all sensible inputs including the zero-RPM edge case.

Bound: internal/acoustic/proxy/proxy_test.go:TestR33Lock/R33-LOCK-14_no_audio_io
