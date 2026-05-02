package hwmon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Reason names a specific blocker in the out-of-tree module fallback chain.
// Each value maps to a distinct hwdiag entry with its own remediation. The
// numeric values are stable — the wizard's status JSON serialises Reason as
// an integer for the UI.
type Reason int

const (
	// ReasonOK means the preflight found no blockers — InstallDriver can run.
	ReasonOK Reason = iota
	// ReasonKernelHeadersMissing — /lib/modules/$(uname -r)/build is absent.
	ReasonKernelHeadersMissing
	// ReasonDKMSMissing — dkms binary not on PATH (and kernel ceiling would
	// require DKMS to track future kernel updates).
	ReasonDKMSMissing
	// ReasonSecureBootBlocks is the legacy aggregate Secure Boot blocker.
	// Predicate is "Secure Boot enforcing AND any of (sign-file missing,
	// mokutil missing, MOK key missing)". New code should branch on the
	// _NeedSignFile / _NeedMokutil / _NeedMOKKey / _Ready sub-reasons; this
	// constant remains for backwards-compat with the existing
	// emitPreflightDiag dispatch in setup.go.
	ReasonSecureBootBlocks
	// ReasonKernelTooNew — the driver is known to break on kernels above
	// DriverNeed.MaxSupportedKernel.
	ReasonKernelTooNew

	// — v0.5.9 PR-D additions ———————————————————————————————————————

	// ReasonGCCMissing — gcc binary is absent. The kernel module build
	// invokes the host gcc; the existing ensureBuildTools auto-fix can
	// install it.
	ReasonGCCMissing
	// ReasonMakeMissing — make binary is absent. Same auto-fix path as gcc.
	ReasonMakeMissing
	// ReasonSignFileMissing — Secure Boot is enforcing but the kmod
	// sign-file helper is absent on PATH and not at the canonical
	// /usr/src/linux-headers-$(uname -r)/scripts/sign-file location.
	// Distro auto-fix: install the kmod package.
	ReasonSignFileMissing
	// ReasonMokutilMissing — Secure Boot is enforcing and mokutil is
	// absent. We need mokutil to enroll the operator-generated MOK; the
	// install path cannot proceed without it on enforcing systems.
	ReasonMokutilMissing
	// ReasonLibModulesReadOnly — /lib/modules/$(uname -r)/extra cannot
	// be created or written. Snap-based or immutable-rootfs distros
	// (Silverblue, NixOS, Ubuntu Core) hit this.
	ReasonLibModulesReadOnly
	// ReasonContainerised — ventd is running inside a container. Calibration
	// requires real hardware access; refuse install rather than silently
	// produce garbage.
	ReasonContainerised
	// ReasonAptLockHeld — apt/dpkg lock is held by another process. Auto-fix
	// is "wait + retry", not bypass — clobbering the lock corrupts the
	// package DB.
	ReasonAptLockHeld
	// ReasonNoSudoNoRoot — daemon is not root AND `sudo -n true` fails.
	// We cannot escalate privileges non-interactively to run modprobe /
	// depmod / cp into /lib/modules.
	ReasonNoSudoNoRoot
	// ReasonStaleDKMSState — DKMS already tracks the target module under
	// a non-current version. Auto-fix: `dkms remove --all <pkg>/<ver>`
	// before re-registering.
	ReasonStaleDKMSState
	// ReasonInTreeDriverConflict — an in-tree driver that claims the same
	// platform device is currently loaded (e.g. `nct6683` loaded when we
	// want to install `nct6687d`). Auto-fix: modprobe -r + write a
	// blacklist drop-in.
	ReasonInTreeDriverConflict
	// ReasonAnotherWizardRunning — a sibling wizard process holds the
	// /run/ventd-wizard.lock and is alive. The PID liveness check
	// distinguishes stale locks from genuine concurrent runs.
	ReasonAnotherWizardRunning
	// ReasonDiskFull — one of the install-critical paths (/var/cache,
	// /lib/modules, /usr/src) has insufficient free bytes. Threshold:
	// MinFreeBytes (256 MiB) per path.
	ReasonDiskFull
)

// MinFreeBytes is the per-path free-space threshold (bytes). 256 MiB covers
// a typical kernel-headers download (~150 MiB on Debian + decompression
// overhead) plus a built module's intermediate .o files.
const MinFreeBytes uint64 = 256 * 1024 * 1024

// PreflightResult is the outcome of PreflightOOT. Detail is populated for
// non-OK reasons so the caller can log a specific message without re-deriving.
type PreflightResult struct {
	Reason Reason
	Detail string
}

// Probes bundles the injection points for PreflightOOT. Production callers
// use DefaultProbes(); tests substitute a fixture. Every probe is a function
// so test fixtures can return synthetic values without touching the live
// filesystem or invoking subprocesses.
//
// A nil probe is treated as "unknown" — PreflightOOT skips the corresponding
// check rather than panicking. This lets a partial test fixture exercise a
// single rule without populating every field.
type Probes struct {
	// KernelRelease returns `uname -r`.
	KernelRelease func() string
	// BuildDirExists reports whether /lib/modules/<release>/build exists.
	BuildDirExists func(release string) bool
	// HasBinary reports whether `name` is on PATH.
	HasBinary func(name string) bool
	// SecureBootEnabled returns (enabled, known). known=false means we
	// could not determine Secure Boot state (non-UEFI system, missing tools).
	SecureBootEnabled func() (enabled bool, known bool)

	// — v0.5.9 PR-D additions ———————————————————————————————————————

	// MOKKeyAvailable reports whether a usable MOK signing key pair is
	// present on disk. Production resolves to LocateMOKKey().
	MOKKeyAvailable func() bool
	// LibModulesWritable reports whether the install path can create
	// /lib/modules/<release>/extra and write a file into it.
	LibModulesWritable func(release string) bool
	// IsContainerised reports whether ventd is running inside a container.
	// Lightweight heuristic — covers /.dockerenv, /proc/1/cgroup keywords,
	// systemd-detect-virt --container exit code.
	IsContainerised func() bool
	// AptLockHeld reports whether dpkg's frontend or download lock is
	// currently held. Returns false on non-apt systems.
	AptLockHeld func() bool
	// HaveRootOrPasswordlessSudo reports whether the daemon can elevate
	// non-interactively. True when euid==0 OR `sudo -n true` succeeds.
	HaveRootOrPasswordlessSudo func() bool
	// StaleDKMSState reports whether DKMS already tracks the target
	// module under any version (any state — built, installed, broken).
	// The auto-fix's `dkms remove` is gated on this returning true.
	StaleDKMSState func(module string) bool
	// InTreeDriverConflict reports whether a kernel-tree driver that
	// claims the same hardware as the OOT module is currently loaded.
	// Returns the conflicting module name and true, or "" + false.
	InTreeDriverConflict func(target string) (conflicting string, ok bool)
	// AnotherWizardRunning reports whether the wizard lock file names a
	// live PID. The lock helper in internal/setup/lock.go is the
	// canonical writer.
	AnotherWizardRunning func() bool
	// DiskFreeBytes returns the free bytes on the filesystem mounted at
	// path, or 0 + an error when statfs fails.
	DiskFreeBytes func(path string) (uint64, error)
}

// DefaultProbes wires PreflightOOT against the live system. Every field is
// non-nil — the live impls live in preflight_probes.go.
func DefaultProbes() Probes {
	return Probes{
		KernelRelease:              liveKernelRelease,
		BuildDirExists:             liveBuildDirExists,
		HasBinary:                  liveHasBinary,
		SecureBootEnabled:          liveSecureBootEnabled,
		MOKKeyAvailable:            liveMOKKeyAvailable,
		LibModulesWritable:         liveLibModulesWritable,
		IsContainerised:            liveIsContainerised,
		AptLockHeld:                liveAptLockHeld,
		HaveRootOrPasswordlessSudo: liveHaveRootOrPasswordlessSudo,
		StaleDKMSState:             liveStaleDKMSState,
		InTreeDriverConflict:       liveInTreeDriverConflict,
		AnotherWizardRunning:       liveAnotherWizardRunning,
		DiskFreeBytes:              liveDiskFreeBytes,
	}
}

// PreflightOOT runs the fallback chain and returns the first blocker found,
// or ReasonOK if every check passed.
//
// Chain order (most-blocking first; each refused gate prevents later gates
// from masking it):
//
//  1. Containerised — calibration is unsafe and writes can't reach hwmon.
//  2. No root/passwordless-sudo — every later step needs elevation.
//  3. Another wizard already running — abort early so we don't race a
//     sibling's modprobe.
//  4. In-tree driver conflict — must unbind before insmod will succeed.
//  5. /lib/modules read-only — cannot install regardless of build.
//  6. Disk full on any of /lib/modules, /usr/src, /var/cache.
//  7. Apt lock held — auto-fix is "wait", not "bypass".
//  8. Secure Boot enforcing → check sign-file, then mokutil, then MOK key
//     (each missing piece returns its own Reason).
//  9. Kernel version ceiling — known-incompatible kernel.
//
// 10.  Kernel headers — cannot build without them.
// 11.  Build tools (gcc, make).
// 12.  DKMS — soft, last in the chain.
// 13.  Stale DKMS state for this module — warn so we run the cleanup auto-fix.
//
// Each "if probe == nil" arm is a deliberate "skip when caller didn't wire
// the probe" — required for the partial-fixture test pattern.
func PreflightOOT(nd DriverNeed, p Probes) PreflightResult {
	if p.IsContainerised != nil && p.IsContainerised() {
		return PreflightResult{
			Reason: ReasonContainerised,
			Detail: "ventd is running inside a container — calibration cannot run safely from a containerised environment. Run ventd directly on the host.",
		}
	}

	if p.HaveRootOrPasswordlessSudo != nil && !p.HaveRootOrPasswordlessSudo() {
		return PreflightResult{
			Reason: ReasonNoSudoNoRoot,
			Detail: "ventd is not running as root and `sudo -n` is not available. Driver install needs to write to /lib/modules and run modprobe; configure passwordless sudo for the ventd user or run ventd as root.",
		}
	}

	if p.AnotherWizardRunning != nil && p.AnotherWizardRunning() {
		return PreflightResult{
			Reason: ReasonAnotherWizardRunning,
			Detail: "Another ventd setup wizard is already running on this machine. Wait for it to finish, or take over the existing run.",
		}
	}

	if p.InTreeDriverConflict != nil {
		if conflicting, ok := p.InTreeDriverConflict(nd.Module); ok {
			return PreflightResult{
				Reason: ReasonInTreeDriverConflict,
				Detail: "The in-tree " + conflicting + " driver is currently loaded and would conflict with " + nd.Module + ". Unbind it (modprobe -r " + conflicting + ") and blacklist it before installing.",
			}
		}
	}

	release := p.KernelRelease()
	if p.LibModulesWritable != nil && release != "" && !p.LibModulesWritable(release) {
		return PreflightResult{
			Reason: ReasonLibModulesReadOnly,
			Detail: "/lib/modules/" + release + "/extra is not writable. This usually means an immutable rootfs (Silverblue, NixOS, Ubuntu Core) — driver install cannot proceed without manual intervention.",
		}
	}

	if p.DiskFreeBytes != nil {
		for _, path := range diskCheckPaths {
			free, err := p.DiskFreeBytes(path)
			if err != nil {
				continue // path doesn't exist on this system, skip
			}
			if free < MinFreeBytes {
				return PreflightResult{
					Reason: ReasonDiskFull,
					Detail: "Insufficient free space on " + path + " (" + humanBytes(free) + " available, " + humanBytes(MinFreeBytes) + " required). Free up space before installing.",
				}
			}
		}
	}

	if p.AptLockHeld != nil && p.AptLockHeld() {
		return PreflightResult{
			Reason: ReasonAptLockHeld,
			Detail: "Another package manager (apt/dpkg) is currently running. Wait for it to finish, then retry.",
		}
	}

	// Secure Boot prerequisite chain. We split the legacy ReasonSecureBootBlocks
	// into ordered prereqs so the wizard cards have a clear sequence: install
	// kmod (sign-file) → install mokutil → generate MOK key → enroll → reboot.
	if p.SecureBootEnabled != nil {
		if enabled, known := p.SecureBootEnabled(); known && enabled {
			if p.HasBinary != nil && !p.HasBinary("sign-file") {
				return PreflightResult{
					Reason: ReasonSignFileMissing,
					Detail: "Secure Boot is enforcing but the kmod sign-file helper is missing. Install the kmod package (or equivalent) so ventd can sign the " + nd.Module + " module before loading it.",
				}
			}
			if p.HasBinary != nil && !p.HasBinary("mokutil") {
				return PreflightResult{
					Reason: ReasonMokutilMissing,
					Detail: "Secure Boot is enforcing but mokutil is missing. Install the mokutil package so ventd can enroll a Machine Owner Key.",
				}
			}
			// Treat a nil MOK probe as "key absence is unknown → assume
			// missing" so legacy callers (test fixtures without the new
			// probe wired, and the v0.5.8 emitPreflightDiag dispatch
			// that still consumes the aggregate ReasonSecureBootBlocks)
			// keep their refuse-on-SB behaviour.
			if p.MOKKeyAvailable == nil || !p.MOKKeyAvailable() {
				return PreflightResult{
					Reason: ReasonSecureBootBlocks,
					Detail: "Secure Boot is enforcing and no MOK signing key is enrolled yet. Generate and enroll a key, then ventd can sign the " + nd.Module + " module.",
				}
			}
		}
	}

	if nd.MaxSupportedKernel != "" && release != "" {
		if kernelAbove(release, nd.MaxSupportedKernel) {
			return PreflightResult{
				Reason: ReasonKernelTooNew,
				Detail: "Kernel " + release + " is newer than the last version " +
					nd.ChipName + " is known to build against (" + nd.MaxSupportedKernel +
					"). The upstream driver has not been updated; build will likely fail.",
			}
		}
	}

	if p.BuildDirExists != nil && release != "" && !p.BuildDirExists(release) {
		return PreflightResult{
			Reason: ReasonKernelHeadersMissing,
			Detail: "Kernel headers for " + release + " are not installed. They " +
				"are required to build the " + nd.Module + " module.",
		}
	}

	if p.HasBinary != nil {
		if !p.HasBinary("gcc") {
			return PreflightResult{
				Reason: ReasonGCCMissing,
				Detail: "gcc is not installed. The kernel module build cannot run without a C compiler.",
			}
		}
		if !p.HasBinary("make") {
			return PreflightResult{
				Reason: ReasonMakeMissing,
				Detail: "make is not installed. The kernel module build cannot run without it.",
			}
		}
	}

	if p.HasBinary != nil && !p.HasBinary("dkms") {
		return PreflightResult{
			Reason: ReasonDKMSMissing,
			Detail: "DKMS is not installed. Without it the " + nd.Module +
				" module will need to be rebuilt manually after every kernel update.",
		}
	}

	if p.StaleDKMSState != nil && p.StaleDKMSState(nd.Module) {
		return PreflightResult{
			Reason: ReasonStaleDKMSState,
			Detail: "DKMS already tracks " + nd.Module + " under a previous version. The remediation will run `dkms remove` to clear that state before installing the current version.",
		}
	}

	return PreflightResult{Reason: ReasonOK}
}

// diskCheckPaths are the install-critical paths checked for free space.
// Each is a candidate write target during the install pipeline.
var diskCheckPaths = []string{"/lib/modules", "/usr/src", "/var/cache"}

// humanBytes renders a byte count in MiB/GiB for the operator-facing detail
// string. Truncates to 1 decimal place.
func humanBytes(n uint64) string {
	const (
		mi = 1024 * 1024
		gi = 1024 * mi
	)
	switch {
	case n >= gi:
		return strconv.FormatFloat(float64(n)/float64(gi), 'f', 1, 64) + " GiB"
	case n >= mi:
		return strconv.FormatFloat(float64(n)/float64(mi), 'f', 1, 64) + " MiB"
	default:
		return strconv.FormatUint(n, 10) + " B"
	}
}

func liveKernelRelease() string {
	out, err := exec.Command("uname", "-r").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func liveBuildDirExists(release string) bool {
	if release == "" {
		return false
	}
	_, err := os.Stat("/lib/modules/" + release + "/build")
	return err == nil
}

func liveHasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// liveSecureBootEnabled first tries `mokutil --sb-state`. On systems without
// mokutil it falls back to reading the SecureBoot EFI variable directly.
func liveSecureBootEnabled() (bool, bool) {
	if _, err := exec.LookPath("mokutil"); err == nil {
		out, err := exec.Command("mokutil", "--sb-state").Output()
		if err == nil {
			s := strings.ToLower(string(out))
			if strings.Contains(s, "secureboot enabled") {
				return true, true
			}
			if strings.Contains(s, "secureboot disabled") {
				return false, true
			}
		}
	}

	matches, _ := filepathGlobEFIVar()
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		// efivars layout: 4 bytes of attributes, then the value. A single
		// nonzero byte after the attrs means Secure Boot is on.
		if len(data) >= 5 {
			return data[4] != 0, true
		}
	}
	return false, false
}

// filepathGlobEFIVar returns the SecureBoot efivar paths (there is only ever
// one, but the GUID suffix varies).
func filepathGlobEFIVar() ([]string, error) {
	return filepath.Glob("/sys/firmware/efi/efivars/SecureBoot-*")
}

// kernelAbove reports whether running > ceiling, comparing dotted-number
// prefixes (5.15.0 > 5.14.9). Non-numeric suffixes are ignored. When either
// side fails to parse, returns false (we don't gate on ambiguous versions).
func kernelAbove(running, ceiling string) bool {
	a := parseKernelVersion(running)
	b := parseKernelVersion(ceiling)
	if a == nil || b == nil {
		return false
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return len(a) > len(b)
}

func parseKernelVersion(s string) []int {
	// Strip anything after the first non-version character: "6.17.0-20-generic"
	// → "6.17.0".
	end := len(s)
	for i, r := range s {
		if (r < '0' || r > '9') && r != '.' {
			end = i
			break
		}
	}
	parts := strings.Split(s[:end], ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			return nil
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
