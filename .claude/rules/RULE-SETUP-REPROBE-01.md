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
