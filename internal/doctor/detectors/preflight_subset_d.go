// Package detectors holds the v0.5.10 doctor detectors. Each file
// implements one Detector that emits zero or more Facts per Probe call.
//
// New detectors should:
//   - Be pure read (RULE-DOCTOR-01); no /sys, /dev, or /var/lib/ventd writes.
//   - Cost <50 ms in the common path; the runner caps Probe at 200 ms
//     (RULE-DOCTOR-09).
//   - Compute a stable EntityHash so the suppression store can scope
//     dismissals per-entity instead of per-detector.
package detectors

import (
	"context"
	"fmt"
	"time"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/recovery"
)

// PreflightSubsetDetector runs PR-D's PreflightOOT chain against current
// system state and surfaces any non-OK reason as a doctor Fact. This
// catches "the system was healthy at install time but a precondition
// regressed since" — e.g. a snap refresh remounted /lib/modules
// read-only, an ABRT package update brought back an in-tree driver
// conflict, the operator's apt is now wedged on a stale lock.
//
// The detector wraps PR-D's existing classifier instead of duplicating
// the per-Reason intelligence; new Reason constants flow through
// automatically once the mapping table here is updated.
type PreflightSubsetDetector struct {
	// Driver is the OOT module the operator's system depends on.
	// The runtime wiring populates this from the resolved catalog
	// match. With multiple drivers, register one detector per driver.
	Driver hwmon.DriverNeed

	// ProbesFn returns the live Probes value used to evaluate
	// preflight. Defaults to hwmon.DefaultProbes() at construction
	// time; tests inject a stub.
	ProbesFn func() hwmon.Probes
}

// NewPreflightSubsetDetector constructs a detector for the given
// driver. probes nil means "use hwmon.DefaultProbes() each tick" —
// re-evaluated per call so live state changes are visible.
func NewPreflightSubsetDetector(driver hwmon.DriverNeed, probes func() hwmon.Probes) *PreflightSubsetDetector {
	if probes == nil {
		probes = hwmon.DefaultProbes
	}
	return &PreflightSubsetDetector{Driver: driver, ProbesFn: probes}
}

// Name returns the stable detector ID per RULE-DOCTOR-DETECTOR-*.
func (d *PreflightSubsetDetector) Name() string { return "preflight_subset" }

// Probe runs the PR-D preflight chain and returns either no facts
// (Reason=OK) or one Fact describing the regression.
func (d *PreflightSubsetDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	probes := d.ProbesFn()
	res := hwmon.PreflightOOT(d.Driver, probes)
	if res.Reason == hwmon.ReasonOK {
		return nil, nil
	}

	severity, class := classifyReason(res.Reason)

	fact := doctor.Fact{
		Detector:   d.Name(),
		Severity:   severity,
		Class:      class,
		Title:      preflightReasonTitle(res.Reason),
		Detail:     res.Detail,
		EntityHash: doctor.HashEntity(d.Driver.Module, reasonToken(res.Reason)),
		Observed:   timeNowFromDeps(deps),
	}
	return []doctor.Fact{fact}, nil
}

// timeNowFromDeps returns deps.Now() or time.Now if deps.Now is nil.
// Detectors call through this so tests can pass Deps{} without
// panicking on a nil Now field.
func timeNowFromDeps(deps doctor.Deps) time.Time {
	if deps.Now != nil {
		return deps.Now()
	}
	return time.Now()
}

// classifyReason maps a hwmon.Reason to (Severity, FailureClass).
// Centralised so adding a new Reason in PR-D-follow-ups updates the
// detector's behaviour with one new switch arm and no other code.
func classifyReason(r hwmon.Reason) (doctor.Severity, recovery.FailureClass) {
	switch r {
	// Hard blockers — install absolutely cannot proceed; at runtime
	// these mean a precondition regressed.
	case hwmon.ReasonContainerised:
		return doctor.SeverityBlocker, recovery.ClassContainerised
	case hwmon.ReasonNoSudoNoRoot:
		return doctor.SeverityBlocker, recovery.ClassDaemonNotRoot
	case hwmon.ReasonLibModulesReadOnly:
		return doctor.SeverityBlocker, recovery.ClassReadOnlyRootfs
	case hwmon.ReasonAnotherWizardRunning:
		return doctor.SeverityBlocker, recovery.ClassConcurrentInstall
	case hwmon.ReasonInTreeDriverConflict:
		return doctor.SeverityBlocker, recovery.ClassInTreeConflict
	case hwmon.ReasonStaleDKMSState:
		return doctor.SeverityBlocker, recovery.ClassDKMSStateCollision
	case hwmon.ReasonDiskFull:
		return doctor.SeverityBlocker, recovery.ClassDiskFull

	// Secure-Boot prerequisite chain — Warning at runtime (module is
	// already loaded; these matter for re-install).
	case hwmon.ReasonSecureBootBlocks,
		hwmon.ReasonSignFileMissing,
		hwmon.ReasonMokutilMissing:
		return doctor.SeverityWarning, recovery.ClassSecureBoot

	// Build-tool absence — Warning. Module is loaded; rebuild on next
	// kernel update would fail.
	case hwmon.ReasonGCCMissing,
		hwmon.ReasonMakeMissing:
		return doctor.SeverityWarning, recovery.ClassMissingBuildTools

	case hwmon.ReasonAptLockHeld:
		return doctor.SeverityWarning, recovery.ClassPackageManagerBusy

	case hwmon.ReasonKernelHeadersMissing:
		return doctor.SeverityWarning, recovery.ClassMissingHeaders
	case hwmon.ReasonDKMSMissing:
		return doctor.SeverityWarning, recovery.ClassDKMSBuildFailed
	case hwmon.ReasonKernelTooNew:
		// Running kernel exceeds the OOT driver's max-supported. At
		// runtime this is a Warning (the module is loaded; suspect
		// behaviour) until / unless the driver actually fails.
		return doctor.SeverityWarning, recovery.ClassUnknown

	default:
		return doctor.SeverityWarning, recovery.ClassUnknown
	}
}

// preflightReasonTitle renders a one-line operator-facing summary for
// the given reason. The recovery catalogue's Remediation entries
// provide the actionable card body; this is just the headline.
func preflightReasonTitle(r hwmon.Reason) string {
	switch r {
	case hwmon.ReasonContainerised:
		return "ventd is running in a container — calibration unsafe"
	case hwmon.ReasonNoSudoNoRoot:
		return "Daemon lacks root and passwordless sudo"
	case hwmon.ReasonLibModulesReadOnly:
		return "/lib/modules is read-only — module install/rebuild blocked"
	case hwmon.ReasonAnotherWizardRunning:
		return "Another wizard run holds the install lock"
	case hwmon.ReasonInTreeDriverConflict:
		return "An in-tree driver claims the same chip as the OOT module"
	case hwmon.ReasonStaleDKMSState:
		return "DKMS holds stale state for the target module"
	case hwmon.ReasonDiskFull:
		return "Disk space below 256 MiB on a path the install pipeline writes to"
	case hwmon.ReasonSecureBootBlocks:
		return "Secure Boot is enforcing without an enrolled MOK signing key"
	case hwmon.ReasonSignFileMissing:
		return "Secure Boot enforcing but kmod sign-file is not installed"
	case hwmon.ReasonMokutilMissing:
		return "Secure Boot enforcing but mokutil is not installed"
	case hwmon.ReasonGCCMissing:
		return "gcc is not installed — module rebuild on next kernel update will fail"
	case hwmon.ReasonMakeMissing:
		return "make is not installed — module rebuild on next kernel update will fail"
	case hwmon.ReasonAptLockHeld:
		return "apt/dpkg lock is held — package operations will block"
	case hwmon.ReasonKernelHeadersMissing:
		return fmt.Sprintf("Kernel headers are missing for the running kernel")
	case hwmon.ReasonDKMSMissing:
		return "DKMS is not installed — automatic module rebuild on kernel updates is unavailable"
	case hwmon.ReasonKernelTooNew:
		return "Running kernel exceeds the OOT driver's max-supported version"
	default:
		return fmt.Sprintf("Preflight reported reason %s", reasonToken(r))
	}
}

// reasonToken renders a hwmon.Reason as a stable snake_case identifier
// for entity-hash composition + JSON output. Kept local to the
// detector so adding a Reason in hwmon doesn't force an export.
func reasonToken(r hwmon.Reason) string {
	switch r {
	case hwmon.ReasonOK:
		return "ok"
	case hwmon.ReasonKernelHeadersMissing:
		return "kernel_headers_missing"
	case hwmon.ReasonDKMSMissing:
		return "dkms_missing"
	case hwmon.ReasonSecureBootBlocks:
		return "secure_boot_blocks"
	case hwmon.ReasonKernelTooNew:
		return "kernel_too_new"
	case hwmon.ReasonGCCMissing:
		return "gcc_missing"
	case hwmon.ReasonMakeMissing:
		return "make_missing"
	case hwmon.ReasonSignFileMissing:
		return "sign_file_missing"
	case hwmon.ReasonMokutilMissing:
		return "mokutil_missing"
	case hwmon.ReasonLibModulesReadOnly:
		return "lib_modules_read_only"
	case hwmon.ReasonContainerised:
		return "containerised"
	case hwmon.ReasonAptLockHeld:
		return "apt_lock_held"
	case hwmon.ReasonNoSudoNoRoot:
		return "no_sudo_no_root"
	case hwmon.ReasonStaleDKMSState:
		return "stale_dkms_state"
	case hwmon.ReasonInTreeDriverConflict:
		return "in_tree_driver_conflict"
	case hwmon.ReasonAnotherWizardRunning:
		return "another_wizard_running"
	case hwmon.ReasonDiskFull:
		return "disk_full"
	default:
		return fmt.Sprintf("reason_%d", int(r))
	}
}
