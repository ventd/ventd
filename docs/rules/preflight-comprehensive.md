# Comprehensive preflight rules — v0.5.9 PR-D

These invariants govern v0.5.9's predict-not-react preflight chain
in `internal/hwmon/ootpreflight.go` (extended) and the live probe
implementations in `internal/hwmon/preflight_probes.go`. The legacy
`PreflightOOT` checked five conditions; the install path had ~25
implicit preconditions, so mid-install failures surfaced as cryptic
errors. This pack pins twelve new probe contracts plus an ordering
invariant that keeps `KERNEL_TOO_NEW` deterministic.

The patch spec lives in `/root/.claude/plans/here-is-a-draft-curious-sutton.md`.
Each rule binds 1:1 to a subtest in
`internal/hwmon/ootpreflight_test.go`. `tools/rulelint` blocks the
merge if a rule lacks its bound test.

## RULE-PREFLIGHT-OK_all_present: Probes happy-path returns ReasonOK.

`PreflightOOT` returns `ReasonOK` when every probe in `Probes`
reports a healthy state. The `baseProbes()` helper in the test
file is the canonical fixture for "everything OK"; subtests for
the failure paths invert one probe at a time off this baseline.

Bound: internal/hwmon/ootpreflight_test.go:RULE-PREFLIGHT-OK_all_present

## RULE-PREFLIGHT-CONTAINER_refused: Container detection refuses install.

`IsContainerised() == true` causes `PreflightOOT` to return
`ReasonContainerised` regardless of any other state. Calibration
cannot run safely from inside a container — hwmon writes don't
reach real hardware and the calibration sweep produces garbage.

Bound: internal/hwmon/ootpreflight_test.go:RULE-PREFLIGHT-CONTAINER_refused

## RULE-PREFLIGHT-SUDO_required: No-root-no-sudo refuses install.

`HaveRootOrPasswordlessSudo() == false` causes `PreflightOOT` to
return `ReasonNoSudoNoRoot`. Driver install needs to write to
`/lib/modules` and run `modprobe`; without root or passwordless
sudo, every later step would fail with permission errors anyway —
better to refuse up-front with an actionable message.

Bound: internal/hwmon/ootpreflight_test.go:RULE-PREFLIGHT-SUDO_required

## RULE-PREFLIGHT-CONCURRENT_wizard: Live wizard lock refuses install.

`AnotherWizardRunning() == true` causes `PreflightOOT` to return
`ReasonAnotherWizardRunning`. A second wizard racing the first
leads to corrupted DKMS state, half-written modules-load.d
entries, and intermixed log streams.

Bound: internal/hwmon/ootpreflight_test.go:RULE-PREFLIGHT-CONCURRENT_wizard

## RULE-PREFLIGHT-INTREE_conflict: Loaded conflicting in-tree driver refuses install.

`InTreeDriverConflict(target)` returning a non-empty conflict name
+ `true` causes `PreflightOOT` to return
`ReasonInTreeDriverConflict`. The remediation detail string MUST
name the conflicting module so the operator (or auto-fix endpoint)
knows which driver to `modprobe -r` and blacklist before retry.

Bound: internal/hwmon/ootpreflight_test.go:RULE-PREFLIGHT-INTREE_conflict

## RULE-PREFLIGHT-LIBMODULES_readonly: Read-only /lib/modules refuses install.

`LibModulesWritable(release) == false` causes `PreflightOOT` to
return `ReasonLibModulesReadOnly`. Immutable-rootfs distros
(Silverblue, NixOS, Ubuntu Core) hit this — the install pipeline
literally cannot create `/lib/modules/<release>/extra/` and the
remediation is a docs-only redirect to the distro's
system-modification procedure.

Bound: internal/hwmon/ootpreflight_test.go:RULE-PREFLIGHT-LIBMODULES_readonly

## RULE-PREFLIGHT-DISKFULL_lib_modules: Insufficient free space on /lib/modules refuses install.

`DiskFreeBytes("/lib/modules")` returning a value below
`MinFreeBytes` (256 MiB) causes `PreflightOOT` to return
`ReasonDiskFull`. The detail string MUST name the offending path
so the operator knows which filesystem to free.

Bound: internal/hwmon/ootpreflight_test.go:RULE-PREFLIGHT-DISKFULL_lib_modules

## RULE-PREFLIGHT-DISKFULL_skips_missing_path: Missing diskCheckPaths entries do not refuse.

`DiskFreeBytes(path)` returning a non-nil error (typically ENOENT
for paths that don't exist on this distro) is treated as "skip" —
NOT as a refusal. Distros without `/usr/src` (some embedded ones)
must still pass this gate.

Bound: internal/hwmon/ootpreflight_test.go:RULE-PREFLIGHT-DISKFULL_skips_missing_path

## RULE-PREFLIGHT-APTLOCK_held: Apt/dpkg lock held refuses install.

`AptLockHeld() == true` causes `PreflightOOT` to return
`ReasonAptLockHeld`. Auto-fix is "wait + retry", never "bypass" —
clobbering the dpkg lock corrupts the package DB.

Bound: internal/hwmon/ootpreflight_test.go:RULE-PREFLIGHT-APTLOCK_held

## RULE-PREFLIGHT-SB_signfile_missing: Secure Boot enforcing without sign-file refuses install.

When `SecureBootEnabled` reports enforcing AND
`HasBinary("sign-file") == false`, `PreflightOOT` returns
`ReasonSignFileMissing`. The remediation card runs the distro's
kmod-installing command via `DistroInfo.KmodInstallCommand()`.

Bound: internal/hwmon/ootpreflight_test.go:RULE-PREFLIGHT-SB_signfile_missing

## RULE-PREFLIGHT-SB_mokutil_missing: Secure Boot enforcing without mokutil refuses install.

When `SecureBootEnabled` reports enforcing AND
`HasBinary("mokutil") == false`, `PreflightOOT` returns
`ReasonMokutilMissing`. mokutil must come AFTER sign-file in the
chain because installing mokutil without kmod present produces a
non-functional partial fix.

Bound: internal/hwmon/ootpreflight_test.go:RULE-PREFLIGHT-SB_mokutil_missing

## RULE-PREFLIGHT-SB_mok_key_missing: Secure Boot enforcing without MOK key returns the legacy aggregate.

When `SecureBootEnabled` reports enforcing AND `MOKKeyAvailable`
is nil OR returns false, `PreflightOOT` returns
`ReasonSecureBootBlocks` (the legacy aggregate). The legacy
`emitPreflightDiag` dispatch in `setup.go::1818` keys on this
exact constant — moving MOK-key absence to a more specific reason
later requires also splitting that dispatch.

Bound: internal/hwmon/ootpreflight_test.go:RULE-PREFLIGHT-SB_mok_key_missing

## RULE-PREFLIGHT-GCC_missing: gcc absence refuses install with ReasonGCCMissing.

`HasBinary("gcc") == false` causes `PreflightOOT` to return
`ReasonGCCMissing`. The remediation card runs the distro's
build-tools install command via `DistroInfo.BuildToolsInstallCommand()`.

Bound: internal/hwmon/ootpreflight_test.go:RULE-PREFLIGHT-GCC_missing

## RULE-PREFLIGHT-MAKE_missing: make absence refuses install with ReasonMakeMissing.

`HasBinary("make") == false` causes `PreflightOOT` to return
`ReasonMakeMissing`. Same remediation as gcc — both are needed
for the kernel module build, both come from the same package
on most distros (build-essential / base-devel / build-base).

Bound: internal/hwmon/ootpreflight_test.go:RULE-PREFLIGHT-MAKE_missing

## RULE-PREFLIGHT-DKMS_stale: Stale DKMS state for the target module refuses install.

`StaleDKMSState(module) == true` causes `PreflightOOT` to return
`ReasonStaleDKMSState`. The auto-fix runs `dkms remove --all
<module>` via `CleanupOrphanInstall` before re-registering the
fresh build.

Bound: internal/hwmon/ootpreflight_test.go:RULE-PREFLIGHT-DKMS_stale

## RULE-PREFLIGHT-ORDER_container_beats_signfile: Chain ordering — container detection beats Secure Boot prerequisites.

The chain order matters. When BOTH `IsContainerised()` returns
true AND Secure Boot is enforcing without sign-file,
`PreflightOOT` MUST return `ReasonContainerised`, NOT
`ReasonSignFileMissing`. Containers cannot run calibration
regardless of signing state; the ordering test pins this so a
future re-shuffle of the chain that demotes container detection
fails CI rather than silently regressing.

Bound: internal/hwmon/ootpreflight_test.go:RULE-PREFLIGHT-ORDER_container_beats_signfile
