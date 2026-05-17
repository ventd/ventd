# Wizard PhaseGate machinery rules — v0.5.9 PR-D

These invariants govern the explicit pre/body/post/cleanup hooks
that replace the legacy implicit phase transitions in
`internal/setup/setup.go::Manager.run`. Each phase now has a
declarative `PhaseGate` (in `internal/setup/gates.go`) so:

- Failures classified as a refused-entry surface as a recovery
  card BEFORE the phase body runs (rather than as a mid-flight
  crash inside Body).
- A non-trivial postcondition check catches the case where Body
  returned nil but the resulting state isn't usable for the next
  phase.
- OnFailCleanup runs on every failure path — Pre refusal, Body
  error, Post refusal, ctx cancel, panic recovered — so each
  phase can declare what state it owns and undo it for retry.

The wizard run lock (`internal/setup/lock.go`) is the
phase-zero coordination primitive: it prevents two concurrent
wizard runs from racing each other's modprobe / DKMS state.

Each rule binds 1:1 to a subtest in
`internal/setup/gates_test.go` or `internal/setup/lock_test.go`.

## RULE-WIZARD-GATE-01: Pre refusal skips Body and runs OnFailCleanup.

When `PhaseGate.Pre` returns a non-`ClassUnknown` `FailureClass`,
`RunGate` MUST NOT invoke `Body` and MUST invoke `OnFailCleanup`.
The returned `*GateError` carries the class + detail so the
wizard's calibration error banner renders the matching
remediation card. This is the load-bearing change that turns the
install path from reactive to predictive.

Bound: internal/setup/gates_test.go:TestRULE_WIZARD_GATE_PreRefusalSkipsBody

## RULE-WIZARD-GATE-02: Body error triggers cleanup; sub-gate class is preserved.

When `Body` returns a non-nil error, `RunGate` MUST invoke
`OnFailCleanup` before returning. If the error is already a
`*GateError` (a sub-gate's failure surfacing through this one),
the parent's `*GateError` MUST preserve the inner class — the
wizard banner needs the most-specific class to render the
right card, not a generic `ClassUnknown` widening.

Bound: internal/setup/gates_test.go:TestRULE_WIZARD_GATE_BodyErrorTriggersCleanup

## RULE-WIZARD-GATE-03: Post refusal triggers cleanup with the surfaced class.

When `Body` succeeds (returns nil) but `Post` returns a non-
`ClassUnknown` `FailureClass`, `RunGate` MUST invoke
`OnFailCleanup` and return a `*GateError` carrying the Post
class + detail. This catches "module loaded but pwm sysfs
absent" / "calibration succeeded but no fans were actually
written" / etc. — postconditions that can't be expressed as
Body errors because Body's narrow contract is "did the side
effect happen", not "is the resulting state correct".

Bound: internal/setup/gates_test.go:TestRULE_WIZARD_GATE_PostRefusalTriggersCleanup

## RULE-WIZARD-GATE-04: Body panic is recovered and triggers cleanup.

When `Body` panics, `RunGate`'s deferred recover MUST invoke
`OnFailCleanup` and return a `*GateError` (with `ClassUnknown`,
since a panic is by definition unclassifiable). The wizard's
own deferred recover handles logging the panic + stack — the
gate's job is only to ensure cleanup runs and the panic doesn't
escape the goroutine.

Bound: internal/setup/gates_test.go:TestRULE_WIZARD_GATE_PanicRecoversAndCleansUp

## RULE-WIZARD-GATE-05: Happy path runs Body and skips OnFailCleanup.

When all of `Pre`, `Body`, and `Post` succeed, `RunGate` MUST
return nil and MUST NOT invoke `OnFailCleanup`. Cleanup is
explicitly the failure-path contract — running it on success
would needlessly tear down the state the next phase depends on.

Bound: internal/setup/gates_test.go:TestRULE_WIZARD_GATE_HappyPathRunsBody

## RULE-WIZARD-GATE-06: Nil Body returns a GateError without invoking cleanup.

A `PhaseGate` with `Body == nil` is a programming error —
there's no runtime state to clean up because nothing ran. RunGate
returns a `*GateError` with `ClassUnknown` immediately and
MUST NOT invoke `OnFailCleanup`. The wizard surface treats this
as a generic "Send diagnostic bundle" path.

Bound: internal/setup/gates_test.go:TestRULE_WIZARD_GATE_NilBodyRefused

## RULE-WIZARD-GATE-LOCK-01: AcquireWizardLock writes the current PID and release removes it.

`AcquireWizardLock()` MUST write `os.Getpid()` to
`WizardLockPath()` and return a release function that removes
the file on call. The lock path resolves via the precedence
$VENTD_WIZARD_LOCK_DIR > /run (root) > $XDG_RUNTIME_DIR > /tmp.
The internal/hwmon `wizardLockPath` helper that the preflight
probe reads MUST agree on the same precedence so the
AnotherWizardRunning probe sees what AcquireWizardLock wrote.

Bound: internal/setup/lock_test.go:TestRULE_WIZARD_GATE_LockAcquireRelease

## RULE-WIZARD-GATE-LOCK-02: Stale PID in the lock file is reused.

When the lock file exists but contains a PID that
`isWizardAlive(pid)` reports as no longer running,
`AcquireWizardLock` MUST overwrite the file with the current
PID instead of refusing. A stuck wizard process that died
without releasing the lock would otherwise permanently block
subsequent wizard runs until manual cleanup.

Bound: internal/setup/lock_test.go:TestRULE_WIZARD_GATE_LockStalePidIsReused

## RULE-WIZARD-GATE-LOCK-03: Live non-self PID in the lock file refuses with ErrWizardAlreadyRunning.

When the lock file contains a live, non-self PID,
`AcquireWizardLock` MUST return `*ErrWizardAlreadyRunning` with
the holder PID populated. The wizard surface uses the PID to
render an actionable "Take over PID N" button rather than a
generic "wizard busy" error. The "non-self" exception lets the
gate handle re-entry of the same process gracefully.

Bound: internal/setup/lock_test.go:TestRULE_WIZARD_GATE_LockLivePidRefuses
