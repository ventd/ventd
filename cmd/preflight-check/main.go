// Command preflight-check is a validation-only helper that runs PreflightOOT
// with a synthetic DriverNeed against the live system and prints the Reason
// as JSON. It exists so the Tier 0.5 VM validation matrix (which has no real
// Super I/O chip and therefore never triggers the setup-manager preflight
// branch) can still confirm that the classification chain behaves correctly
// on each target distro. Not shipped in release binaries.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/ventd/ventd/internal/hwmon"
)

func main() {
	maxKernel := flag.String("max-kernel", "", "synthetic MaxSupportedKernel ceiling (e.g. 6.6)")
	flag.Parse()

	nd := hwmon.DriverNeed{
		Key:                "synthetic",
		ChipName:           "SYNTHETIC",
		Module:             "synthetic",
		MaxSupportedKernel: *maxKernel,
	}
	res := hwmon.PreflightOOT(nd, hwmon.DefaultProbes())

	out := map[string]any{
		"reason":        res.Reason,
		"reason_string": reasonString(res.Reason),
		"detail":        res.Detail,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(1)
	}
}

func reasonString(r hwmon.Reason) string {
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
	}
	return "UNKNOWN"
}
