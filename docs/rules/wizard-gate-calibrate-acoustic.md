# RULE-WIZARD-GATE-CALIBRATE-ACOUSTIC-01: calibrate_acoustic PhaseGate is opt-in, non-fatal, and cleans up its own temp files.

The `calibrate_acoustic` PhaseGate (constructed via
`CalibrateAcousticGate(opts AcousticGateOptions)` in
`internal/setup/gates_acoustic.go`) wraps the optional post-thermal-calibration
mic-calibration step from R30. The gate is consumed by the wizard's eventual
`Manager.run` PhaseGate-slice refactor (#67); until that lands the gate is
exercised in isolation by `gates_acoustic_test.go` and the constructor is the
sole public surface.

The gate has three guarantees, each binding to one subtest of
`TestRULE_WIZARD_GATE_CALIBRATE_ACOUSTIC_01`:

1. **Opt-in**: when `opts.MicDevice` is empty, Body returns nil immediately
   without invoking the runner. This is the canonical "operator did not pass
   `--mic`" path; the wizard proceeds to the next phase as if this gate
   doesn't exist.

2. **Non-fatal on runner failure**: when the runner returns an error, the
   gate's OnFailCleanup runs and the gate driver returns a `*GateError` —
   but acoustic calibration is opt-in, so the wizard's recovery banner
   surfaces a generic ClassUnknown remediation card rather than refusing
   the install. The daemon falls back to R33 proxy-only loudness estimation.
   Equivalently, when Post detects the calibration JSON wasn't written (a
   silent runner failure), Post logs a warning and returns ClassUnknown so
   the wizard proceeds — this is the "soft fall-through to proxy-only"
   contract, never a refusal.

3. **OnFailCleanup sweeps `<TempDir>/ventd-acoustic-*.{wav,raw}`**: enforces
   RULE-DIAG-PR2C-11's architectural denylist for raw audio temp files,
   even when the wizard's own goroutine doesn't reach the runner's deferred
   cleanup (e.g. the operator killed the process mid-capture). The sweep is
   prefix + suffix bounded — files matching `ventd-other-*.wav` or
   `ventd-acoustic-*.txt` are NOT touched, so cleanup can't disturb other
   ventd subsystems' temp files or operator-staged log captures.

The constructor's `AcousticRunner` callback is the implementation hook. The
production wiring (in a follow-up PR after this one's gate definition lands)
will pass `cmd/ventd/calibrate_acoustic.go::runCalibrateAcoustic` as the
runner; tests pass a stub that records invocations and returns the test's
chosen outcome. A `nil` Runner with a non-empty MicDevice is a
misconfiguration — Body returns an error immediately so the wizard wiring
layer surfaces the bug rather than silently no-op'ing.

The Pre hook is intentionally absent: the soft fall-through semantics mean
there's no condition that would justify Pre refusing the gate. ffmpeg
absence, mic-not-found, ALSA permission failures — the runner handles all
of these and either returns nil (logging a warning) or returns an error that
becomes a generic `*GateError`. The wizard never gets stuck on a missing
optional dependency.

## Wizard integration (Manager.runAcousticGate)

`Manager.runAcousticGate(ctx)` is the wizard's hook into the gate.
Called from `Manager.run` after thermal calibration's `wg.Wait()` and
before the finalising phase. Reads `acousticGateOpts` (set via
`SetAcousticGateOptions`) and invokes the gate when `MicDevice` is
non-empty:

- **No-op**: empty `MicDevice` → returns immediately, no `setPhase`,
  no runner invocation. The wizard proceeds to finalise without
  surfacing a "calibrating microphone" phase to operators who never
  asked for it. Pinned by `TestManager_runAcousticGate_NoOpWhenMicEmpty`.
- **Happy path**: non-empty `MicDevice` → `setPhase("calibrate_acoustic", ...)`
  + `RunGate(...)` invocation. Pinned by
  `TestManager_runAcousticGate_RunsRunnerWhenMicSet`.
- **Non-fatal**: a runner-returned error is logged at WARN and
  discarded — the wizard continues to finalise. Pinned by
  `TestManager_runAcousticGate_NonFatalOnRunnerError`.
- **Round-trip**: `SetAcousticGateOptions` is mutex-protected and
  preserves all fields. Pinned by
  `TestManager_SetAcousticGateOptions_RoundTrip`.

Bound: internal/setup/gates_acoustic_test.go:TestRULE_WIZARD_GATE_CALIBRATE_ACOUSTIC_01
Bound: internal/setup/gates_acoustic_test.go:TestManager_runAcousticGate_NoOpWhenMicEmpty
Bound: internal/setup/gates_acoustic_test.go:TestManager_runAcousticGate_RunsRunnerWhenMicSet
Bound: internal/setup/gates_acoustic_test.go:TestManager_runAcousticGate_NonFatalOnRunnerError
Bound: internal/setup/gates_acoustic_test.go:TestManager_SetAcousticGateOptions_RoundTrip
