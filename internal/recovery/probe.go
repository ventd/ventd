package recovery

import (
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// AMDOverdriveState captures the status of the amdgpu OverDrive bit
// (0x4000 in `amdgpu.ppfeaturemask`) which gates the entire
// `gpu_od/fan_ctrl/` sysfs tree on RDNA1 → RDNA4 cards. Without
// this bit set, ventd cannot write fan curves on AMD discrete GPUs:
// the sysfs nodes simply don't appear (RDNA3+) or write attempts
// return EINVAL (RDNA1/2).
type AMDOverdriveState struct {
	// PpfeaturemaskFound reports whether
	// /sys/module/amdgpu/parameters/ppfeaturemask was readable.
	// false on hosts without the amdgpu module loaded (no AMD
	// discrete GPU), and on hosts where the kernel cmdline never
	// mentioned the parameter (default mask). Callers should treat
	// false as "no AMD GPU in scope" not "OverDrive disabled".
	PpfeaturemaskFound bool
	// Mask is the parsed ppfeaturemask value when found.
	Mask uint32
	// OverdriveBitSet reports whether bit 14 (0x4000) is set in
	// Mask. Required for fan control on every RDNA generation.
	OverdriveBitSet bool
	// TaintsKernel reports whether enabling OverDrive will mark
	// the running kernel as TAINT_CPU_OUT_OF_SPEC. Confirmed
	// 6.14+ via commit b472b8d829c1 ("drm/amd: Taint the kernel
	// when enabling overdrive"). The wizard surfaces this so the
	// operator can opt-in knowingly rather than discover the
	// taint after the fact.
	TaintsKernel bool
	// KernelRelease is the running kernel's `uname -r` value used
	// for the taint check. Empty when the probe couldn't read it.
	KernelRelease string
}

// DetectAMDOverdrive reads /sys/module/amdgpu/parameters/ppfeaturemask
// and returns a struct describing the OverDrive gate state. Used by
// the wizard preflight to surface a recovery card when the operator
// has an AMD discrete GPU but hasn't enabled OverDrive on the kernel
// cmdline — without the bit ventd's amdgpu fan-write path can't
// take control.
//
// rootFS is injectable so tests use testing/fstest.MapFS. Production
// callers pass nil; the function falls back to os.DirFS("/").
//
// kernelReleaseFS is the procfs root used to read /proc/sys/kernel/
// osrelease for the 6.14+ taint check. Same nil-fallback semantics.
//
// Both filesystems readable independently because the os-release
// file lives in proc, not sys; passing them as one fs would force
// tests to set up a merged fixture.
func DetectAMDOverdrive(sysFS fs.FS, procFS fs.FS) AMDOverdriveState {
	if sysFS == nil {
		sysFS = os.DirFS("/sys")
	}
	if procFS == nil {
		procFS = os.DirFS("/proc")
	}
	out := AMDOverdriveState{}
	data, err := fs.ReadFile(sysFS, "module/amdgpu/parameters/ppfeaturemask")
	if err != nil {
		// amdgpu not loaded or no AMD discrete GPU on this host.
		// Leave PpfeaturemaskFound=false; caller treats as out-of-scope.
		return out
	}
	out.PpfeaturemaskFound = true
	// The sysfs file content is "0xNNNNNNNN\n" or a decimal —
	// kernel docs aren't strict. Try hex first (the canonical form
	// the kernel emits when the user passed amdgpu.ppfeaturemask=0x...
	// on cmdline), fall back to decimal.
	raw := strings.TrimSpace(string(data))
	mask, perr := parseMaskValue(raw)
	if perr != nil {
		// Unparseable — best-effort: leave OverdriveBitSet false so
		// the wizard surfaces the recovery card even though we
		// couldn't confirm. Conservative default.
		return out
	}
	out.Mask = mask
	out.OverdriveBitSet = (mask & 0x4000) != 0
	// Taint check: kernel 6.14+ taints when OverDrive is enabled.
	rel, _ := fs.ReadFile(procFS, "sys/kernel/osrelease")
	out.KernelRelease = strings.TrimSpace(string(rel))
	out.TaintsKernel = kernelAtLeast614(out.KernelRelease)
	return out
}

// parseMaskValue accepts either "0xNNNNNNNN" or a bare decimal
// uint32 and returns the value. Hex form is the canonical kernel
// emit; decimal is accepted for resilience against future format
// changes.
func parseMaskValue(s string) (uint32, error) {
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		var v uint64
		_, err := fmt.Sscanf(s, "0x%x", &v)
		return uint32(v), err
	}
	var v uint64
	_, err := fmt.Sscanf(s, "%d", &v)
	return uint32(v), err
}

// kernelAtLeast614 reports whether release encodes a kernel ≥ 6.14.
// Strict prefix parse — "6.14.0-...", "6.14-...", "6.15.0-...",
// "6.20-..." all true; "6.13.x" / "6.6.x" / "5.15.x" all false.
// Empty / unparseable returns false (conservative — the wizard
// won't show a taint warning we can't substantiate).
func kernelAtLeast614(release string) bool {
	if release == "" {
		return false
	}
	// Strip any post-version suffix ("-generic", "-pve", etc.) by
	// splitting on the first non-numeric/non-dot character.
	end := 0
	for end < len(release) {
		c := release[end]
		if (c < '0' || c > '9') && c != '.' {
			break
		}
		end++
	}
	prefix := release[:end]
	parts := strings.Split(prefix, ".")
	if len(parts) < 2 {
		return false
	}
	var major, minor int
	if _, err := fmt.Sscanf(parts[0], "%d", &major); err != nil {
		return false
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &minor); err != nil {
		return false
	}
	if major > 6 {
		return true
	}
	return major == 6 && minor >= 14
}
