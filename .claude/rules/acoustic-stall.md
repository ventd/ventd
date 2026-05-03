# Acoustic stall detector — R31 (advisory only)

These invariants govern `internal/acoustic/stall/`, the implementation of
R31's 2-of-3 fan-stall detector. The detector is consumed during the
post-calibration soak phase only; it surfaces a flag on the polarity
classifier's `ChannelResult.AcousticStallSuspected` and never refuses
fan writes or alters control behaviour.

The three signals (R31 §3):

- **Broadband rise**: window RMS dB rises by ≥ 6 dB over the channel's
  healthy reference at the same RPM bucket.
- **Crest factor excess**: `peak / RMS` rises by ≥ 2 over healthy.
  Bursty stall transients push crest above the Gaussian floor.
- **Kurtosis excess**: 4th-moment excess kurtosis rises by ≥ 1.5 over
  healthy. Heavy-tailed amplitude distributions correlate with stall.

Trigger when at least 2 of the 3 fire within a single window.

The patch spec is `docs/research/r-bundle/R31-fan-stall-acoustic.md`.
v0.5.12 simplification: the broadband measurement uses full-spectrum RMS
rather than R31's specified 1–2 kHz band-passed RMS. The 2-of-3 gate
logic is structurally identical; the band-pass refinement (a 4th-order
Butterworth IIR similar to the A-weighting cascade) lands as a separate
PR after MIMII validation. Documented in RULE-STALL-01 below.

## RULE-STALL-01: DefaultConfig pins R31's canonical thresholds (6 dB / 2.0 / 1.5 / 2-of-3) at fs=48 kHz, window=3 s.

`stall.DefaultConfig()` returns:

```go
Config{
    SampleRate:        48000,
    WindowSeconds:     3.0,
    BroadbandRiseDB:   6.0,
    CrestFactorExcess: 2.0,
    KurtosisExcess:    1.5,
    GateThreshold:     2,
}
```

These values come from R31 §3 directly. The wiring layer can override
individual fields (e.g. tuning sensitivity per HIL telemetry), but the
defaults are the contract for "no operator config" deployments.

The simplification from R31's "1–2 kHz band-passed RMS" to v0.5.12's
"full-spectrum RMS" is documented in the package doc and tracked for
follow-up. The 2-of-3 gate's structural correctness is unaffected.

Bound: internal/acoustic/stall/detector_test.go:TestRULE_STALL_01_DefaultThresholdsLocked

## RULE-STALL-02: Evaluate fires the 2-of-3 gate when at least 2 of {broadband-rise, crest-excess, kurtosis-excess} cross their thresholds.

The exhaustive truth table (8 combinations of 3 binary criteria) is
exercised in the bound subtest. None or 1-of-3 fires never trip the
gate; 2-of-3 and 3-of-3 always do. Refusal-style firing is asymmetric
in a way the test pins: "broadband only", "crest only", and "kurtosis
only" each return `StallSuspected=false` even when the single firing
criterion exceeds its threshold by an arbitrary margin.

Bound: internal/acoustic/stall/detector_test.go:TestRULE_STALL_02_TwoOfThreeGateFires

## RULE-STALL-03: Healthy Gaussian-noise fixture does NOT trip the gate.

A 3-second window of pure Gaussian noise at the same amplitude as the
reference passes through `Extract` + `Evaluate` without firing any
criterion. This is the canonical false-positive guard — the detector
must not flag healthy fans as stalled, since the wiring layer surfaces
`AcousticStallSuspected=true` to the operator and a noisy signal
would erode trust in the doctor card.

The fixture uses different RNG seeds for healthy vs. current to ensure
the test is exercising statistical similarity rather than identical
sample sequences.

Bound: internal/acoustic/stall/detector_test.go:TestRULE_STALL_03_HealthyFixturePassesWithoutAlert

## RULE-STALL-04: Advisory-only contract — Result has no fan-write / abort / disable methods, no exported symbol alters control behaviour.

Static-API contract test. The `Result` struct's allowed fields are
exactly `{StallSuspected, BroadbandRise, CrestExcess, KurtosisExcess,
FiredBroadband, FiredCrest, FiredKurtosis}` — all informational. Adding
a method or a non-allowlisted field fails the test.

The package's `polarity.ChannelResult.AcousticStallSuspected bool` flag
is the operator-visible surface; `WritePWM` (RULE-POLARITY-05) does NOT
read this flag. A fan flagged as stall-suspected can still receive PWM
writes — the wiring deliberately keeps stall detection out of the
control loop in v0.5.12.

This binding doubles as a regression guard: a future PR that adds an
"AbortFan" / "DisableChannel" / similar method to `Result` to "act on
the detection" will fail the test, forcing an explicit decision +
rule-text amendment rather than silently widening the contract.

Bound: internal/acoustic/stall/detector_test.go:TestRULE_STALL_04_AdvisoryOnlyContract

## RULE-STALL-05: Synthetic burst-transient fixture trips at least 2-of-3 against a healthy Gaussian reference.

The canonical "stalled-fan synthetic" — Gaussian noise floor + periodic
0.8-amplitude single-sample bursts every 50 ms — exercises the
positive-detection path. The bursts spike crest factor (peak ≫ RMS),
spike kurtosis (heavy tail in the amplitude histogram), and raise
broadband RMS by a measurable amount. Any one criterion alone might
be marginal; the 2-of-3 gate hits because at least two cross threshold.

This binding pins the feature extractor's correctness on a known input
shape. A regression in `Extract`'s moment computation (e.g. variance
formula bug, peak detection bug) breaks this test before it can mask
a real-world stall.

The MIMII dataset validation (AUC ≥ 0.7 acceptance criterion) is
deferred to a separate PR — the synthetic fixture validates structural
correctness; MIMII validates real-world generalisation.

Bound: internal/acoustic/stall/detector_test.go:TestRULE_STALL_05_BurstTransientsTripGate
