// Package checks implements per-feature precondition detection logic for
// experimental flags. Each file covers one flag; results feed back through
// internal/experimental.Check().
package checks

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// overdriveBit is the OverDrive feature-mask bit in amdgpu.ppfeaturemask.
// Verified against kernel drivers/gpu/drm/amd/amdgpu/amdgpu_drv.c:
//
//	uint amdgpu_pp_feature_mask = 0xfff7bfff; // "OverDrive(bit 14) disabled by default"
const overdriveBit uint32 = 0x4000

// rePPFeatureMask matches the parameter key and captures any non-whitespace value
// so that malformed values (e.g. "0xGGGG") are caught by strconv.ParseUint rather
// than silently treated as "parameter absent".
var rePPFeatureMask = regexp.MustCompile(`amdgpu\.ppfeaturemask=(\S+)`)

// DetectAMDOverdrive parses cmdlinePath (normally /proc/cmdline) for the
// amdgpu.ppfeaturemask parameter and checks whether bit 14 (0x4000) is set.
// Returns (enabled, mask, nil). Returns (false, 0, nil) when the parameter is
// absent. Returns a non-nil error only on read or parse failure.
func DetectAMDOverdrive(cmdlinePath string) (enabled bool, mask uint32, err error) {
	data, err := os.ReadFile(cmdlinePath)
	if err != nil {
		return false, 0, fmt.Errorf("amd_overdrive: read %s: %w", cmdlinePath, err)
	}
	m := rePPFeatureMask.FindStringSubmatch(string(data))
	if m == nil {
		return false, 0, nil
	}
	// ParseUint with base 0 handles both "0x..." and decimal forms.
	val, err := strconv.ParseUint(strings.TrimSpace(m[1]), 0, 32)
	if err != nil {
		return false, 0, fmt.Errorf("amd_overdrive: parse ppfeaturemask %q: %w", m[1], err)
	}
	return (uint32(val) & overdriveBit) != 0, uint32(val), nil
}

// CheckAMDOverdrivePrecondition returns (met bool, detail string) describing
// whether the OverDrive kernel parameter is active. cmdlinePath is normally
// /proc/cmdline; callers may override for testing.
func CheckAMDOverdrivePrecondition(cmdlinePath string) (met bool, detail string) {
	enabled, mask, err := DetectAMDOverdrive(cmdlinePath)
	if err != nil {
		return false, fmt.Sprintf("unable to check ppfeaturemask: %v", err)
	}
	if !enabled {
		if mask == 0 {
			return false, "amdgpu.ppfeaturemask not set in kernel cmdline; OverDrive (bit 0x4000) inactive. " +
				"Add amdgpu.ppfeaturemask=0xffffffff (or at least 0x4000) to kernel cmdline and reboot."
		}
		return false, fmt.Sprintf(
			"amdgpu.ppfeaturemask=0x%08x: OverDrive bit (0x4000) not set. "+
				"Include bit 0x4000 in amdgpu.ppfeaturemask and reboot.", mask)
	}
	return true, fmt.Sprintf("amdgpu.ppfeaturemask=0x%08x: OverDrive bit (0x4000) active", mask)
}
