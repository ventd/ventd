// Package checks provides doctor-report entries for individual subsystems.
package checks

import (
	"fmt"

	"github.com/ventd/ventd/internal/experimental"
)

// AMDOverdriveEntry holds the doctor-report data for the amd_overdrive flag.
type AMDOverdriveEntry struct {
	// Active reflects whether --enable-amd-overdrive is set for this daemon run.
	Active bool
	// Mask is the parsed amdgpu.ppfeaturemask from /proc/cmdline (0 = absent).
	Mask uint32
	// StatusLine is a single human-readable summary line for doctor output.
	StatusLine string
}

// CheckAMDOverdrive builds a doctor report entry for the amd_overdrive
// experimental flag. mask is the ppfeaturemask value returned by
// checks.DetectAMDOverdrive (0 when the parameter is absent from cmdline).
func CheckAMDOverdrive(flags experimental.Flags, mask uint32) AMDOverdriveEntry {
	active := flags.AMDOverdrive
	var status string
	switch {
	case active && mask != 0:
		status = fmt.Sprintf("active (ppfeaturemask=0x%08x, OverDrive bit set)", mask)
	case active && mask == 0:
		status = "active (ppfeaturemask not detected in cmdline — check kernel parameters)"
	case !active && mask != 0:
		status = fmt.Sprintf("inactive (ppfeaturemask=0x%08x present but flag not enabled)", mask)
	default:
		status = "inactive"
	}
	return AMDOverdriveEntry{
		Active:     active,
		Mask:       mask,
		StatusLine: fmt.Sprintf("experimental.amd_overdrive: %s", status),
	}
}
