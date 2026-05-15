# RULE-SETUP-REPROBE-01: Setup Manager re-runs the daemon-level probe after a successful driver install or kernel-module load.

After `hwmonpkg.InstallDriver` returns nil OR `Manager.LoadModule` modprobes
a module successfully, the Manager MUST invoke its registered ReProber
callback. The callback re-runs `probe.New(...).Probe(ctx)` and persists
the result via `probe.PersistOutcome`, so `wizard.initial_outcome` reflects
the post-install kernel state instead of the stale pre-install value.

Without this re-probe, a fresh install whose driver populates pwm channels
mid-wizard leaves the persisted outcome at `monitor_only` until the next
daemon restart (#766). The wizard's local Phase 4 sysfs walk
(`discoverHwmonControls`) is unaffected by this rule — the rule covers
KV-state synchronisation, not the wizard's internal flow.

A nil ReProber is a no-op (the Manager is not always wired to state in
tests). A failed modprobe MUST NOT trigger the ReProber: there's nothing
new to probe in the kernel and re-running a transient probe would risk
overwriting a valid earlier persistence with a worse one. Errors returned
by the ReProber are logged at WARN but do not propagate as a LoadModule /
InstallDriver failure — the underlying modprobe / build succeeded.

Bound: internal/setup/reprobe_test.go:TestReProber_FiresAfterLoadModule
Bound: internal/setup/reprobe_test.go:TestReProber_NotFiredOnFailedLoadModule
Bound: internal/setup/reprobe_test.go:TestReProber_NilIsNoOp
Bound: internal/setup/reprobe_test.go:TestReProber_ErrorLoggedDoesNotBlockSuccess

## RULE-SETUP-REPROBE-02: Setup Manager re-runs the daemon-level probe at the end of the finalize phase whenever calibration produced a non-empty doneFans set.

After `Manager.run` writes `m.result = cfg` (the wizard's apply-ready
configuration containing one or more controllable channels), the Manager
MUST invoke its registered ReProber callback. This persists a fresh
`wizard.initial_outcome` to KV that reflects the post-calibration kernel
state, regardless of whether the wizard ran the `installing_driver` phase.

`afterDriverInstall` (RULE-SETUP-REPROBE-01) only fires from the install
path. Hosts whose fan-controller driver is already loaded at first boot
skip `installing_driver` entirely — the wizard runs hardware-discovery,
polarity probe, calibration, and finalize without ever calling
`afterDriverInstall`. Without this rule the persisted KV outcome stays at
the daemon-startup probe's stale `monitor_only` value forever; every
smart-mode subsystem (coupling / marginal / blended / aggregator /
layer_a / opportunistic / signature) remains permanently inert despite
controllers actively driving PWM (issue #1108).

A nil ReProber is a no-op (test scaffolding only). Errors returned by the
ReProber are logged at WARN but do not block the wizard goroutine — the
generated config and the apply path are unaffected; only smart-mode
activation is delayed until the next reprobe trigger.

The trigger condition is "finalize wrote a non-empty config". The wizard's
existing `if len(doneFans) == 0 { return }` short-circuit at the top of
the apply / build-config block means `afterFinalize` is reached only when
calibration proved controllable channels exist, so a fresh probe will
classify as `OutcomeControl` per RULE-PROBE-04 (≥1 thermal source + ≥1
controllable channel + not virt + not container).

Bound: internal/setup/reprobe_test.go:TestReProber_FiresAfterFinalize
Bound: internal/setup/reprobe_test.go:TestReProber_FinalizeNilIsNoOp
Bound: internal/setup/reprobe_test.go:TestReProber_FinalizeErrorLoggedDoesNotPanic
