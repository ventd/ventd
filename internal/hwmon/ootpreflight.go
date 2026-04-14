package hwmon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Reason names a specific blocker in the out-of-tree module fallback chain.
// Each value maps to a distinct hwdiag entry with its own remediation.
type Reason int

const (
	// ReasonOK means the preflight found no blockers — InstallDriver can run.
	ReasonOK Reason = iota
	// ReasonKernelHeadersMissing — /lib/modules/$(uname -r)/build is absent.
	ReasonKernelHeadersMissing
	// ReasonDKMSMissing — dkms binary not on PATH (and kernel ceiling would
	// require DKMS to track future kernel updates).
	ReasonDKMSMissing
	// ReasonSecureBootBlocks — Secure Boot is enabled and the driver is
	// unsigned, so the kernel will refuse to load it.
	ReasonSecureBootBlocks
	// ReasonKernelTooNew — the driver is known to break on kernels above
	// DriverNeed.MaxSupportedKernel.
	ReasonKernelTooNew
)

// PreflightResult is the outcome of PreflightOOT. Detail is populated for
// non-OK reasons so the caller can log a specific message without re-deriving.
type PreflightResult struct {
	Reason Reason
	Detail string
}

// Probes bundles the injection points for PreflightOOT. Production callers
// use DefaultProbes(); tests substitute a fixture.
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
}

// DefaultProbes wires PreflightOOT against the live system.
func DefaultProbes() Probes {
	return Probes{
		KernelRelease:     liveKernelRelease,
		BuildDirExists:    liveBuildDirExists,
		HasBinary:         liveHasBinary,
		SecureBootEnabled: liveSecureBootEnabled,
	}
}

// PreflightOOT runs the fallback chain and returns the first blocker found,
// or ReasonOK if every check passed. The chain order is fixed:
//
//  1. Secure Boot (driver will be refused at load time)
//  2. Kernel version ceiling (known-incompatible kernel)
//  3. Kernel headers (cannot build without them)
//  4. DKMS (cannot survive kernel upgrade without it — warning, not hard fail
//     for kernels with headers present; but we surface it as a distinct
//     diagnostic so the user can opt in)
func PreflightOOT(nd DriverNeed, p Probes) PreflightResult {
	if enabled, known := p.SecureBootEnabled(); known && enabled {
		return PreflightResult{
			Reason: ReasonSecureBootBlocks,
			Detail: "Secure Boot is enabled; the unsigned " + nd.Module +
				" module will be refused by the kernel. Enroll a Machine " +
				"Owner Key (MOK) to sign the module, or disable Secure Boot in firmware.",
		}
	}

	release := p.KernelRelease()
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

	if release != "" && !p.BuildDirExists(release) {
		return PreflightResult{
			Reason: ReasonKernelHeadersMissing,
			Detail: "Kernel headers for " + release + " are not installed. They " +
				"are required to build the " + nd.Module + " module.",
		}
	}

	if !p.HasBinary("dkms") {
		return PreflightResult{
			Reason: ReasonDKMSMissing,
			Detail: "DKMS is not installed. Without it the " + nd.Module +
				" module will need to be rebuilt manually after every kernel update.",
		}
	}

	return PreflightResult{Reason: ReasonOK}
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
