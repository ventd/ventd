package calibrate

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultHwmonRoot is the production sysfs root used by ComputeHardwareIdentity
// when no override is supplied. Mirrors internal/hwmon's default.
const DefaultHwmonRoot = "/sys/class/hwmon"

// ComputeHardwareIdentity returns a stable 16-character hex fingerprint of
// the host's fan-relevant hardware identity. The inputs are:
//
//   - DMI board_vendor + board_name (from /sys/devices/virtual/dmi/id/),
//     which change when the operator swaps motherboards
//   - sorted list of hwmon chip names (from /sys/class/hwmon/hwmon*/name),
//     which change when a new chip becomes visible (e.g. after an OOT
//     driver install) or when an old chip disappears
//
// A binary upgrade alone does NOT change the identity — chip names stay the
// same when the kernel module is rebuilt against a new kernel — so a daemon
// upgrade preserves calibration. A hardware swap, driver install that
// surfaces a new chip, or motherboard replacement DOES change the identity,
// causing the loader to auto-invalidate stale calibration and force a fresh
// sweep.
//
// hwmonRoot is the sysfs root. Production callers pass DefaultHwmonRoot;
// tests inject a fixture path.
//
// Returns "" when both DMI fields and the hwmon scan are empty — the
// loader treats an empty identity as "skip identity gating," which matches
// the legacy v2 behaviour and avoids spurious invalidations in test
// environments where sysfs is absent.
func ComputeHardwareIdentity(hwmonRoot string) string {
	signature := buildIdentitySignature(hwmonRoot)
	if signature == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(signature))
	return hex.EncodeToString(sum[:])[:16]
}

// buildIdentitySignature assembles the canonical input string the identity
// hash is computed over. Split out for testability — the exact format must
// be stable across releases because changing it would invalidate every
// host's calibration on upgrade.
//
// Format: "vendor=<vendor>|board=<board>|chips=<c1>,<c2>,..."
//   - trims whitespace from DMI values
//   - lowercases chip names (kernel sometimes flips case across releases)
//   - sorts chip names so order changes don't affect the hash
//
// Returns "" when both the DMI values and the chip list are empty.
func buildIdentitySignature(hwmonRoot string) string {
	vendor := strings.TrimSpace(readDMIField(hwmonRoot, "board_vendor"))
	board := strings.TrimSpace(readDMIField(hwmonRoot, "board_name"))
	chips := scanChipNames(hwmonRoot)

	if vendor == "" && board == "" && len(chips) == 0 {
		return ""
	}

	return "vendor=" + vendor + "|board=" + board + "|chips=" + strings.Join(chips, ",")
}

// readDMIField reads a single field from /sys/devices/virtual/dmi/id/.
// hwmonRoot is used to derive the /sys root via the well-known sibling
// layout (both live under /sys), so a test fixture can override DMI via the
// same hwmonRoot override. Returns "" on any read error.
func readDMIField(hwmonRoot, field string) string {
	sysRoot := deriveSysRootForIdentity(hwmonRoot)
	path := filepath.Join(sysRoot, "devices", "virtual", "dmi", "id", field)
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// scanChipNames walks hwmonRoot/hwmon*/name and returns a lowercase,
// deduplicated, sorted list of chip names. Empty when the hwmon tree is
// absent (e.g. test fixture or unusual container).
func scanChipNames(hwmonRoot string) []string {
	dirs, err := filepath.Glob(filepath.Join(hwmonRoot, "hwmon*"))
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{}, len(dirs))
	for _, dir := range dirs {
		b, err := os.ReadFile(filepath.Join(dir, "name"))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(strings.ToLower(string(b)))
		if name != "" {
			seen[name] = struct{}{}
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// deriveSysRootForIdentity maps a hwmonRoot like /sys/class/hwmon back to
// its parent /sys. For test fixtures rooted at <tmp>/sys/class/hwmon, this
// yields <tmp>/sys. Falls back to "/sys" when the suffix doesn't match.
func deriveSysRootForIdentity(hwmonRoot string) string {
	const suffix = "/class/hwmon"
	if strings.HasSuffix(hwmonRoot, suffix) {
		return strings.TrimSuffix(hwmonRoot, suffix)
	}
	return "/sys"
}
