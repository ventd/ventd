package main

// preflight-check was historically a separate binary at
// cmd/preflight-check. It runs PreflightOOT with a synthetic
// DriverNeed against the live system and prints the Reason as JSON.
//
// Folded into the main ventd binary as `ventd --preflight-check`
// so distros only need to package one binary, and so the validation
// tooling stays bisectable with the daemon code that backs it.
//
// Used by the Tier 0.5 VM validation matrix on environments that
// have no real Super-I/O chip and therefore never trigger the
// setup-manager preflight branch organically.

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/ventd/ventd/internal/hwmon"
)

// runPreflightCheck is the subcommand entry point. maxKernel is the
// synthetic MaxSupportedKernel ceiling (e.g. "6.6"); pass an empty
// string to leave it unbounded. Returns the exit code main should
// pass to os.Exit.
func runPreflightCheck(maxKernel string) int {
	nd := hwmon.DriverNeed{
		Key:                "synthetic",
		ChipName:           "SYNTHETIC",
		Module:             "synthetic",
		MaxSupportedKernel: maxKernel,
	}
	res := hwmon.PreflightOOT(nd, hwmon.DefaultProbes())

	out := map[string]any{
		"reason":        res.Reason,
		"reason_string": preflightReasonString(res.Reason),
		"detail":        res.Detail,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		return 1
	}
	return 0
}

func preflightReasonString(r hwmon.Reason) string {
	switch r {
	case hwmon.ReasonOK:
		return "OK"
	case hwmon.ReasonKernelHeadersMissing:
		return "KERNEL_HEADERS_MISSING"
	case hwmon.ReasonDKMSMissing:
		return "DKMS_MISSING"
	case hwmon.ReasonSecureBootBlocks:
		return "SECURE_BOOT_BLOCKS"
	case hwmon.ReasonKernelTooNew:
		return "KERNEL_TOO_NEW"

	// v0.5.9 PR-D additions — every new Reason gets a string so the
	// CLI surface stays exhaustive (the smoke test in
	// preflightcheck_smoke_test.go pins this).
	case hwmon.ReasonGCCMissing:
		return "GCC_MISSING"
	case hwmon.ReasonMakeMissing:
		return "MAKE_MISSING"
	case hwmon.ReasonSignFileMissing:
		return "SIGN_FILE_MISSING"
	case hwmon.ReasonMokutilMissing:
		return "MOKUTIL_MISSING"
	case hwmon.ReasonLibModulesReadOnly:
		return "LIB_MODULES_READ_ONLY"
	case hwmon.ReasonContainerised:
		return "CONTAINERISED"
	case hwmon.ReasonAptLockHeld:
		return "APT_LOCK_HELD"
	case hwmon.ReasonNoSudoNoRoot:
		return "NO_SUDO_NO_ROOT"
	case hwmon.ReasonStaleDKMSState:
		return "STALE_DKMS_STATE"
	case hwmon.ReasonInTreeDriverConflict:
		return "IN_TREE_DRIVER_CONFLICT"
	case hwmon.ReasonAnotherWizardRunning:
		return "ANOTHER_WIZARD_RUNNING"
	case hwmon.ReasonDiskFull:
		return "DISK_FULL"
	}
	return "UNKNOWN"
}
