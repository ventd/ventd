# Preflight orchestrator rules — v0.5.11

These invariants govern v0.5.11's `internal/preflight/` package: the
iterative install-time orchestrator + per-check catalogue. Each rule
below binds 1:1 to a subtest. `tools/rulelint` blocks the merge if a
rule lacks its bound test.

The patch spec is `/root/.claude/plans/tingly-twirling-duckling.md`.
The runtime entry point is the `ventd preflight` subcommand; install-
time integration is in `scripts/install.sh`.

## Orchestrator (internal/preflight/orchestrator.go)

## RULE-PREFLIGHT-ORCH-01: Triggered Blocker with no AutoFix MUST count and return error.

A docs-only blocker (nil AutoFix) cannot clear under any orchestrator
mode. `Run` MUST count it in `Report.BlockerCount` and return a non-
nil error. The operator action is out-of-band (reboot to host, edit
sudoers); the orchestrator surfaces the docs-only branch so the
install path doesn't silently succeed when it shouldn't.

Bound: internal/preflight/orchestrator_test.go:RULE-PREFLIGHT-ORCH-01_blocker_with_no_fix_returns_error

## RULE-PREFLIGHT-ORCH-02: Y answer runs AutoFix and re-runs Detect.

The fix loop's contract is "run, then verify". A buggy AutoFix that
returned nil but didn't actually clear the condition would silently
mark the channel clean without the second Detect call. Two Detect
calls per Y → fix → verify cycle is the load-bearing invariant.

Bound: internal/preflight/orchestrator_test.go:RULE-PREFLIGHT-ORCH-02_interactive_yes_runs_fix_and_redetects

## RULE-PREFLIGHT-ORCH-03: N answer leaves blocker counted; chain continues.

A No answer means "skip this fix" but does not abort the run — the
operator may decline one fix and accept the next. The blocker stays
counted so `Run` returns an error at the end; later checks still
execute and report.

Bound: internal/preflight/orchestrator_test.go:RULE-PREFLIGHT-ORCH-03_no_answer_leaves_blocker_pending

## RULE-PREFLIGHT-ORCH-04: Abort response stops remaining fix prompts.

`PromptAbort` (q / EOF) is the operator saying "I'm done; don't ask
me about anything else". The fix loop MUST exit immediately, leaving
remaining blockers unprompted. This protects the operator who
realises mid-run that a different machine-level issue needs
attention first.

Bound: internal/preflight/orchestrator_test.go:RULE-PREFLIGHT-ORCH-04_abort_stops_remaining_checks

## RULE-PREFLIGHT-ORCH-05: Successful AutoFix with RequiresReboot sets Report.NeedsReboot.

When a Check declares `RequiresReboot: true` (canonical case: MOK
enrollment via `mokutil --import`), a successful AutoFix MUST flip
`Report.NeedsReboot`. install.sh reads this and surfaces a single
reboot prompt at the end of the run rather than per-check. Multiple
RequiresReboot fixes still produce a single reboot.

Bound: internal/preflight/orchestrator_test.go:RULE-PREFLIGHT-ORCH-05_reboot_required_when_fix_marks_it

## RULE-PREFLIGHT-ORCH-06: --skip set excludes named checks from the run.

`Options.Skip[name]` is the operator override path. Even a triggered
blocker named in the skip set MUST NOT count toward BlockerCount or
trigger an AutoFix prompt. The Result is marked `NotApplicable=true`
so the JSON output is auditable.

Bound: internal/preflight/orchestrator_test.go:RULE-PREFLIGHT-ORCH-06_skip_set_excludes_named_checks

## RULE-PREFLIGHT-ORCH-07: --only set restricts the run to the named checks.

`Options.Only`, when non-empty, restricts execution to the named
set. Any check outside the set is dropped from the report entirely
(not Skipped — it never ran). Enables the "re-run a single check
after fixing it manually" workflow.

Bound: internal/preflight/orchestrator_test.go:RULE-PREFLIGHT-ORCH-07_only_set_restricts_run

## RULE-PREFLIGHT-ORCH-08: MaxFixAttempts caps per-check retries.

A `MaxFixAttempts > 1` retries a transient AutoFix failure (e.g. apt
network blip). The cap MUST be honoured — an infinite-retry loop
would hang on a permanently-failing fix. The default is 1 attempt.

Bound: internal/preflight/orchestrator_test.go:RULE-PREFLIGHT-ORCH-08_max_attempts_caps_retries

## RULE-PREFLIGHT-ORCH-09: Severity=Warning never drives the fix loop.

A triggered Warning is reported in the summary but does NOT trigger
the Y/N prompt or run AutoFix. Warnings are advisory; they don't
gate the install. Only Blockers go through the fix loop.

Bound: internal/preflight/orchestrator_test.go:RULE-PREFLIGHT-ORCH-09_warning_does_not_drive_fix_loop

## RULE-PREFLIGHT-ORCH-11: RequiresReboot fix skips post-fix re-detect.

AutoFix on a `RequiresReboot=true` check (canonical: `mokutil
--import` for MOK enrollment) QUEUES a change that only takes
effect after firmware MOK Manager confirmation at next boot. A
generic post-fix Detect would still report `triggered=true`
(`mokutil --list-enrolled` doesn't include queued imports), causing
the orchestrator to falsely treat the fix as failed and exhaust
MaxFixAttempts. The orchestrator MUST trust AutoFix's nil return
for these checks: skip the re-detect, mark StillTriggered=false,
set Report.NeedsReboot. Caught on Phoenix's HIL desktop.

Bound: internal/preflight/orchestrator_test.go:RULE-PREFLIGHT-ORCH-11_requires_reboot_skips_redetect

## RULE-PREFLIGHT-ORCH-10: Summary groups by severity (Blocker → Warning → Info).

Operators read top-down. The pre-fix summary block MUST list
Blockers first, then Warnings, then Info. A reordering that surfaces
Info above Blockers would bury the load-bearing problems.

Bound: internal/preflight/orchestrator_test.go:RULE-PREFLIGHT-ORCH-10_summary_groups_by_severity

## Per-distro dispatch (internal/preflight/checks/distro_dispatch.go)

## RULE-PREFLIGHT-DISPATCH-01: Debian/Ubuntu installs use apt-get with -y and --no-install-recommends.

Without -y the install hangs on a confirm prompt; without
--no-install-recommends apt pulls in a kernel metapackage we don't
want. `DEBIAN_FRONTEND=noninteractive` suppresses debconf prompts
mid-install (eg. for grub-pc).

Bound: internal/preflight/checks/distro_dispatch_test.go:RULE-PREFLIGHT-DISPATCH-01_debian_uses_apt

## RULE-PREFLIGHT-DISPATCH-02: Fedora dispatches to dnf -y; yum is symlinked.

Fedora/RHEL/CentOS use `dnf install -y`. yum is a compatibility
symlink to dnf on every supported version, so the same command works
across the family.

Bound: internal/preflight/checks/distro_dispatch_test.go:RULE-PREFLIGHT-DISPATCH-02_fedora_uses_dnf

## RULE-PREFLIGHT-DISPATCH-03: Arch dispatches to pacman with --needed --noconfirm.

`--needed` prevents reinstalling an already-present package;
`--noconfirm` is the non-interactive flag.

Bound: internal/preflight/checks/distro_dispatch_test.go:RULE-PREFLIGHT-DISPATCH-03_arch_uses_pacman_needed

## RULE-PREFLIGHT-DISPATCH-04: Unknown distro family returns docs-only.

When `os-release` ID + ID_LIKE don't match any of debian/fedora/
arch/suse/alpine, `installCommand` returns `ok=false`. The AutoFix
surfaces a docs-only error rather than running an empty shell
command — leaves the operator to install manually.

Bound: internal/preflight/checks/distro_dispatch_test.go:RULE-PREFLIGHT-DISPATCH-04_unknown_family_returns_docs_only

## RULE-PREFLIGHT-DISPATCH-05: Kernel-headers package name is family-specific.

Debian/Ubuntu encode the running kernel release in the package name
(`linux-headers-<release>`); Fedora uses `kernel-devel-<release>`;
Arch is rolling and uses bare `linux-headers`. A wrong name would
install headers for the wrong kernel.

Bound: internal/preflight/checks/distro_dispatch_test.go:RULE-PREFLIGHT-DISPATCH-05_kernel_headers_package_per_family

## RULE-PREFLIGHT-DISPATCH-06: Build-tools meta-package is family-specific.

Each family has a canonical "give me a build environment" name:
`build-essential` (Debian), `base-devel` (Arch), `gcc make` (Fedora,
SUSE), `build-base` (Alpine). The wrong name leaves the operator
with cryptic "command not found: gcc" mid-build.

Bound: internal/preflight/checks/distro_dispatch_test.go:RULE-PREFLIGHT-DISPATCH-06_build_tools_package_per_family

## Secure Boot chain (internal/preflight/checks/secure_boot.go)

## RULE-PREFLIGHT-SB-01: SB chain Detect short-circuits when SB is not enforcing.

Non-UEFI / SB-disabled hosts MUST short-circuit every SB Detect to
`!triggered`. Without this, the chain would offer to install kmod /
mokutil and generate a key on a host that will never need them.

Bound: internal/preflight/checks/secure_boot_test.go:RULE-PREFLIGHT-SB-01_chain_skipped_when_not_enforcing

## RULE-PREFLIGHT-SB-02: signfile_missing fires when sign-file is absent.

The first chain check fires when `HasBinary("sign-file") == false`.
The detail string MUST include "sign-file" so the operator can
search docs by the exact tool name.

Bound: internal/preflight/checks/secure_boot_test.go:RULE-PREFLIGHT-SB-02_signfile_missing_triggers_first

## RULE-PREFLIGHT-SB-03: signfile AutoFix dispatches via DistroInfo.

The kmod-installing AutoFix MUST issue the distro-correct install
command. On Arch, sign-file is bundled with `linux-headers` (not a
separate kmod package); elsewhere `kmod` is the package.

Bound: internal/preflight/checks/secure_boot_test.go:RULE-PREFLIGHT-SB-03_signfile_autofix_dispatches_per_distro

## RULE-PREFLIGHT-SB-04: MOK-keypair-missing detail names the configured key dir.

`SecureBootProbes.MOKKeyDir` is the directory the keypair lands in.
The detail string MUST include the configured dir so an operator
running with a non-default dir can verify; a hardcoded path in the
detail would mislead.

Bound: internal/preflight/checks/secure_boot_test.go:RULE-PREFLIGHT-SB-04_mok_keypair_check_uses_dir_field

## RULE-PREFLIGHT-SB-05: MOK-not-enrolled check sets RequiresReboot=true.

`mokutil --import` only takes effect after reboot + firmware MOK
Manager confirmation. The check MUST set `RequiresReboot: true` so
the orchestrator's end-of-run reboot prompt fires.

Bound: internal/preflight/checks/secure_boot_test.go:RULE-PREFLIGHT-SB-05_mok_enroll_requires_reboot

## RULE-PREFLIGHT-SB-06: MOK-not-enrolled defers when keypair is absent.

The 4th check depends on the 3rd — without a keypair on disk
there's nothing to enroll. Detect MUST return `!triggered` with a
detail naming the prerequisite, so the operator clears predecessor
checks first.

Bound: internal/preflight/checks/secure_boot_test.go:RULE-PREFLIGHT-SB-06_enroll_check_skips_when_keypair_absent

## RULE-PREFLIGHT-SB-07: All four SB checks MUST be Severity=Blocker.

A non-blocker SB issue would let the install continue and the
kernel module load to fail at modprobe time, defeating the
predict-not-react design.

Bound: internal/preflight/checks/secure_boot_test.go:RULE-PREFLIGHT-SB-07_all_four_severity_blocker

## RULE-PREFLIGHT-SB-08: signfile detection consults the injected HasBinary probe.

The signfile check MUST consult the `SecureBootProbes.HasBinary`
callback rather than hard-wiring `exec.LookPath`. The live HasBinary
implementation falls back to the canonical kernel-headers path
(`/usr/src/linux-headers-<release>/scripts/sign-file`) which DKMS
hardcodes for module signing — sign-file is rarely on PATH on
Debian/Ubuntu, so a PATH-only check falsely flags every host that
already signs modules successfully (caught on Phoenix's HIL desktop).

Bound: internal/preflight/checks/secure_boot_test.go:RULE-PREFLIGHT-SB-08_signfile_check_uses_injected_probe

## RULE-PREFLIGHT-SB-09: MOK fingerprint matcher strips colons and lowercases.

The fingerprint matcher MUST handle openssl's `SHA1 Fingerprint=AA:BB:...`
output and mokutil's `SHA1 Fingerprint: aa:bb:...` output uniformly.
openssl emits uppercase-with-equals; mokutil emits lowercase-with-
colon-after-Fingerprint. A case-sensitive or colon-sensitive
comparison would produce false negatives on every machine, masking
"key on disk but not enrolled" — the very condition the check
exists to detect.

Bound: internal/preflight/checks/secure_boot_test.go:RULE-PREFLIGHT-SB-09_normaliseFingerprint_strips_colons_and_case

## Build environment (internal/preflight/checks/build.go)

## RULE-PREFLIGHT-BUILD-01: gcc_missing AutoFix installs build-tools meta-package.

build-essential / base-devel / `gcc make` — installs the umbrella
meta-package because gcc + make are typically installed together.
An AutoFix that installed just gcc would leave make missing for the
very next check.

Bound: internal/preflight/checks/build_test.go:RULE-PREFLIGHT-BUILD-01_gcc_missing_dispatches_build_tools_install

## RULE-PREFLIGHT-BUILD-02: kernel_headers AutoFix uses release-specific package.

On Debian/Ubuntu the headers package name encodes the running
kernel release. Installing the unversioned `linux-headers` would
fetch the metapackage tracking HWE kernels, not necessarily the
running one.

Bound: internal/preflight/checks/build_test.go:RULE-PREFLIGHT-BUILD-02_kernel_headers_use_release_specific_pkg

## RULE-PREFLIGHT-BUILD-03: dkms_missing severity is Blocker, not Warning.

DKMS missing MUST be Blocker — the install path needs DKMS to
rebuild after kernel updates. A non-blocker dkms would let the
install succeed and silently break on the next kernel apt upgrade.

Bound: internal/preflight/checks/build_test.go:RULE-PREFLIGHT-BUILD-03_dkms_check_severity_blocker

## RULE-PREFLIGHT-BUILD-04: kernel_headers AutoFix returns errReleaseUnknown when uname empty.

When `KernelRelease()` returns "" (test fixture forgot to wire it,
or uname is unavailable), the kernel-headers AutoFix MUST refuse
rather than installing the wrong package. The error is recognisable
via `errors.Is`.

Bound: internal/preflight/checks/build_test.go:RULE-PREFLIGHT-BUILD-04_release_unknown_returns_actionable_err

## RULE-PREFLIGHT-BUILD-05: Unknown distro family returns docs-only error.

On a distro family the dispatch table doesn't recognise, the
AutoFix MUST surface a docs-only error rather than running an empty
install command.

Bound: internal/preflight/checks/build_test.go:RULE-PREFLIGHT-BUILD-05_unknown_distro_returns_docs_only

## Conflicts and runtime gates (internal/preflight/checks/conflicts.go)

## RULE-PREFLIGHT-CONFL-01: in_tree_driver_conflict AutoFix unbinds AND blacklists.

The fix MUST do both: `modprobe -r` (clears immediate state) AND
write to the blacklist drop-in (prevents the conflict from
reloading at next boot). A modprobe-only fix would let the next
reboot reload the in-tree driver and fail the wizard again.

Bound: internal/preflight/checks/conflicts_test.go:RULE-PREFLIGHT-CONFL-01_in_tree_autofix_unbinds_and_blacklists

## RULE-PREFLIGHT-CONFL-02: stale_dkms AutoFix runs `dkms remove --all`.

`--all` is required because we don't know which DKMS versions are
present without parsing dkms output. A version-specific remove
would leave stale state behind.

Bound: internal/preflight/checks/conflicts_test.go:RULE-PREFLIGHT-CONFL-02_stale_dkms_autofix_runs_remove_all

## RULE-PREFLIGHT-CONFL-03: userspace_fan_daemon AutoFix stops every active daemon.

Multiple competing daemons (rare but happens — fancontrol +
thinkfan on a Lenovo with both packages installed) MUST each be
stopped. A loop that returns after the first would leave the
second competing for the same hwmon paths.

Bound: internal/preflight/checks/conflicts_test.go:RULE-PREFLIGHT-CONFL-03_userspace_daemon_stops_each_active

## RULE-PREFLIGHT-CONFL-04: disk_full skips paths that don't exist.

On distros without `/usr/src` (some embedded ones), the
DiskFreeBytes probe returns ENOENT. The check MUST treat that as
skip — continue checking other paths — rather than fail
attributing it to a path the operator never had.

Bound: internal/preflight/checks/conflicts_test.go:RULE-PREFLIGHT-CONFL-04_disk_full_skips_missing_paths

## RULE-PREFLIGHT-CONFL-05: disk_full triggers below MinFreeBytes.

Below 256 MiB on any critical path triggers the blocker. Detail
string MUST include the offending path so the operator knows which
filesystem to free.

Bound: internal/preflight/checks/conflicts_test.go:RULE-PREFLIGHT-CONFL-05_disk_full_triggers_below_min_free

## RULE-PREFLIGHT-CONFL-06: concurrent_install is docs-only (nil AutoFix).

There is no safe AutoFix because killing the other wizard PID could
leave the install in a half-state. The Check MUST have nil AutoFix
so the orchestrator surfaces it as docs-only.

Bound: internal/preflight/checks/conflicts_test.go:RULE-PREFLIGHT-CONFL-06_concurrent_install_no_autofix

## RULE-PREFLIGHT-CONFL-07: in_tree blacklist drop-in path comes from DistroInfo.

The blacklist drop-in path is per-distro (always
`/etc/modprobe.d/ventd-blacklist.conf` today, but the indirection
allows future divergence). A hardcoded path would land somewhere
harmless on Alpine, etc.

Bound: internal/preflight/checks/conflicts_test.go:RULE-PREFLIGHT-CONFL-07_in_tree_uses_distro_blacklist_path

## RULE-PREFLIGHT-CONFL-08: apt_lock_held is docs-only (nil AutoFix).

Auto-fixing means waiting, which is what the operator would do
manually. Surfacing a "force-unlock" auto-fix would corrupt the
package DB. The Check MUST have nil AutoFix.

Bound: internal/preflight/checks/conflicts_test.go:RULE-PREFLIGHT-CONFL-08_apt_lock_no_autofix

## System-level (internal/preflight/checks/system.go)

## RULE-PREFLIGHT-SYS-01: containerised_environment is docs-only (nil AutoFix).

A container can't run calibration. There's no safe way to "exit
the container" from inside it; the Check MUST have nil AutoFix.

Bound: internal/preflight/checks/system_test.go:RULE-PREFLIGHT-SYS-01_container_is_blocker_no_autofix

## RULE-PREFLIGHT-SYS-02: no_sudo_no_root is docs-only (nil AutoFix).

sudoers config is too sensitive to auto-mutate. The check MUST be
docs-only — the operator must run `visudo` themselves.

Bound: internal/preflight/checks/system_test.go:RULE-PREFLIGHT-SYS-02_no_sudo_no_autofix

## RULE-PREFLIGHT-SYS-03: lib_modules_read_only detail names kernel release.

The detail string MUST include the kernel release so the operator
knows which path to investigate. A bare "/lib/modules read-only"
message would be useless on a system with multiple kernels
installed.

Bound: internal/preflight/checks/system_test.go:RULE-PREFLIGHT-SYS-03_lib_modules_ro_detail_names_release

## RULE-PREFLIGHT-SYS-04: kernel_too_new with empty MaxSupportedKernel disables the check.

`MaxSupportedKernel=""` disables the check entirely. Used by the
synthetic preflight invocation (legacy `--preflight-check`) where
the caller doesn't know the driver-specific ceiling.

Bound: internal/preflight/checks/system_test.go:RULE-PREFLIGHT-SYS-04_kernel_too_new_with_empty_max_skips

## RULE-PREFLIGHT-SYS-05: kernel_too_new compares dotted versions; ceiling is inclusive.

6.10.0 > 6.6.0 triggers; 6.5.0 < 6.6.0 doesn't. Equal versions
(6.6.0 == 6.6.0) MUST NOT trigger — the driver's
MaxSupportedKernel is inclusive.

Bound: internal/preflight/checks/system_test.go:RULE-PREFLIGHT-SYS-05_kernel_too_new_compares_dotted_versions

## RULE-PREFLIGHT-SYS-06: port_held check uses configured listen address.

`PortHeldCheck` reads the configured listen address. Empty addr
disables the check. A non-empty addr that fails to bind triggers
the blocker; the detail string includes the addr.

Bound: internal/preflight/checks/system_test.go:RULE-PREFLIGHT-SYS-06_port_held_uses_addr
