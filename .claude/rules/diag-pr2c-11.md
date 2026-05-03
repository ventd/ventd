# RULE-DIAG-PR2C-11: Raw audio temp files (`/tmp/ventd-acoustic-*`, `*.wav`, `*.raw`) are on the architectural denylist and never enter a bundle.

The v0.5.12 PR-D mic-calibration CLI (`ventd calibrate --acoustic`)
captures up to 60 s of mic audio via ffmpeg → 16-bit PCM mono 48 kHz
WAV → in-memory parse for RMS dBFS + A-weighted dBFS + K_cal offset.
The CLI deletes the temp `.wav` immediately after parsing — raw audio
is never persisted by design.

**But** a crashed ffmpeg subprocess, a panic in the parser, or an
operator who stages a manual reference-tone capture by hand could
leave a stale audio file under `/tmp`. The architectural denylist
catches any such file before it could enter a diagnostic bundle:

- `/tmp/ventd-acoustic-` — the canonical CLI temp prefix.
- `/tmp/ventd-mic-` — reserved for the operator-staged reference-tone
  capture path used by the wizard's PhaseGate (PR-D follow-up).
- `.wav` — generic suffix; catches operator-renamed captures anywhere.
- `.raw` — generic suffix; catches alternative encodings if a future
  collector emits raw PCM dumps.

Like all denylist entries (RULE-DIAG-PR2C-06), the enforcement is
architectural — the bundle generator simply never reads files matching
these patterns. No redactor primitive is asked to scrub them because
they are never collected.

The R30 calibration *result* (K_cal offset, dBA-vs-PWM curve, mic
identity hash) lives in the calibration JSON record under
`ChannelCalibration.{KCalOffset,DBAPerPWM,DBAPWMCurve,KCalMicID}` and
DOES enter the bundle — those fields are operator-meaningful triage
data with no embedded raw audio.

Bound: internal/diag/bundle_audio_denylist_test.go:TestRuleDiagPR2C_11_AudioTempFilesNeverCaptured
