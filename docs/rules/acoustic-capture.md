# Acoustic capture — R30 mic-calibration primitives

These invariants govern `internal/acoustic/capture/`, the pure-Go
implementation of R30's microphone calibration primitives:
WAV-header parsing, RMS dBFS computation, and the A-weighting filter
(IEC 61672-1:2013 Class 1, 6th-order IIR via canonical bilinear
transform) used to convert dBFS → dBA when paired with R30's K_cal
reference-tone offset.

The package is consumed by the v0.5.12 PR-D `ventd calibrate --acoustic`
CLI subcommand which spawns ffmpeg for the actual ALSA capture. This
package never opens an audio device and never persists raw audio.

The patch spec is `docs/research/r-bundle/R30-mic-calibration.md`.
Each rule below is bound 1:1 to a subtest under `capture_test.go`.

## RULE-ACOUSTIC-CAPTURE-01: Parse accepts only 16-bit PCM mono 48 kHz WAVs.

Sample rate / channel count / bit depth are pinned at construction
time so the A-weighting IIR coefficients (computed for 48 kHz) stay
load-bearing. Any deviation rejects with ErrFormat — silently up-
or down-converting would invalidate the dBFS math.

Bound: internal/acoustic/capture/capture_test.go:TestParse_AcceptsValidMonoPCM
Bound: internal/acoustic/capture/capture_test.go:TestParse_RejectsWrongSampleRate
Bound: internal/acoustic/capture/capture_test.go:TestParse_RejectsStereo
Bound: internal/acoustic/capture/capture_test.go:TestParse_RejectsWrongBitDepth
Bound: internal/acoustic/capture/capture_test.go:TestParse_RejectsTruncatedHeader

## RULE-ACOUSTIC-CAPTURE-02: Parse rejects captures longer than MaxCaptureSeconds (60 s).

Calibration captures are 30 s per R30 §3.2; the 60 s cap bounds
memory usage and prevents an operator-supplied .wav from exhausting
the daemon's heap.

Bound: internal/acoustic/capture/capture_test.go:TestParse_RejectsOverlongCapture
Bound: internal/acoustic/capture/capture_test.go:TestEnsureWAVRead_BoundsMaxSize

## RULE-ACOUSTIC-CAPTURE-03: RMSdBFS matches the IEC convention — full-scale sine ≈ -3.01 dBFS, half-scale ≈ -9.03 dBFS, silence floors at -120 dBFS.

The dBFS calculation is sqrt(mean(x²)) with reference 1.0. A full-
scale sine wave (amplitude 1.0) has RMS = 1/√2 ≈ 0.707 →
20·log10(0.707) ≈ -3.01 dBFS. The -120 dBFS floor matches what
"silence" looks like in IEC measurements — the -∞ true value is
clamped to keep downstream math finite.

Bound: internal/acoustic/capture/capture_test.go:TestRMSdBFS_FullScaleSineApproxMinusThree
Bound: internal/acoustic/capture/capture_test.go:TestRMSdBFS_HalfScaleSineApproxMinusNine
Bound: internal/acoustic/capture/capture_test.go:TestRMSdBFS_SilenceClampedToFloor
Bound: internal/acoustic/capture/capture_test.go:TestRMSdBFS_EmptyInputReturnsFloor

## RULE-ACOUSTIC-CAPTURE-04: A-weighting filter at 1 kHz produces ~0 dB change; 100 Hz attenuates ~19 dB; 10 kHz attenuates ~2.5 dB.

The IEC 61672-1:2013 Class 1 A-weighting curve is normalised so 1 kHz
→ 0 dB. Lower / higher frequencies are attenuated per the standard
curve so "30 dB at 100 Hz" and "30 dB at 1 kHz" map to comparable
loudness — that's the whole point of dBA over dBFS.

Coefficients are 3-stage biquad cascaded for fs=48 kHz, computed via
canonical bilinear transform from the analogue Class-1 prototype.
Tolerances are wider than the IEC tolerance (±1 dB at 1 kHz, 14-24 dB
at 100 Hz, -3-8 dB at 10 kHz) to absorb the bilinear-transform
roll-off near Nyquist + transient settling on the test signals.

Bound: internal/acoustic/capture/capture_test.go:TestAWeight_NearOneKHzApproxZeroDBChange
Bound: internal/acoustic/capture/capture_test.go:TestAWeight_LowFrequencyHeavilyAttenuated
Bound: internal/acoustic/capture/capture_test.go:TestAWeight_HighFrequencyAttenuated
