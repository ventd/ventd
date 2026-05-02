package recovery

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
)

// VendorDaemon names the OEM-shipped fan-control daemon detected on
// the host. Empty when no vendor daemon is active. Linux-first OEM
// laptop vendors (System76, ASUS ROG, Tuxedo, Lenovo Legion,
// Framework) ship working fan daemons that ventd should defer to in
// monitor-only mode rather than fight for control.
//
// R28 Agent G's #1 architectural finding, validated against current
// upstream (2024-2026) by an agent research pass before this list
// went into production. Earlier drafts mistakenly included
// slimbookbattery (TLP frontend, NOT a fan daemon) and
// asusctl.service (the CLI binary, never a systemd unit upstream).
// Both removed.
type VendorDaemon string

const (
	VendorDaemonNone      VendorDaemon = ""
	VendorDaemonSystem76  VendorDaemon = "system76-power"
	VendorDaemonAsusctl   VendorDaemon = "asusctl"
	VendorDaemonTuxedo    VendorDaemon = "tuxedo" // covers both tccd and tailord
	VendorDaemonLegion    VendorDaemon = "legiond"
	VendorDaemonFramework VendorDaemon = "fw-fanctrl"
)

// vendorDaemonUnits maps each vendor daemon to the systemd unit names
// queried via `systemctl is-active`. Each entry below is the canonical
// upstream name today; aliases were pruned after research validation:
//
//   - System76: only system76-power.service is a fan owner. The
//     system76-scheduler.service is a CFS / process-priority tweaker
//     and does NOT touch fans — DO NOT include it as a defer trigger.
//   - ASUS: only asusd.service exists upstream (binary `asusd`).
//     `asusctl` is the CLI client, not a daemon — there is no
//     asusctl.service on any distro packaging.
//   - Tuxedo ships TWO competing daemons today: tccd (Node.js, on
//     Tuxedo OS / Ubuntu) and tailord (Rust rewrite from
//     AaronErhardt/tuxedo-rs, default on NixOS / Arch). Either active
//     is sufficient to defer.
//   - Lenovo Legion: legiond from johnfanv2/LenovoLegionLinux drives
//     fan profile switching via the legion-laptop kernel module.
//   - Framework: fw-fanctrl (TamtamHero/fw-fanctrl) is a community
//     project that drives fans via ectool. Framework themselves ship
//     no fan daemon — pure firmware-managed by default. When
//     fw-fanctrl is active, defer.
//
// Slimbook removed entirely: slimbookbattery is a TLP frontend, no
// fan control. Slimbook hardware otherwise has no vendor fan daemon;
// fans are firmware/EC-managed.
var vendorDaemonUnits = map[VendorDaemon][]string{
	VendorDaemonSystem76:  {"system76-power.service"},
	VendorDaemonAsusctl:   {"asusd.service"},
	VendorDaemonTuxedo:    {"tccd.service", "tailord.service"},
	VendorDaemonLegion:    {"legiond.service"},
	VendorDaemonFramework: {"fw-fanctrl.service"},
}

// SystemctlIsActive reports whether the named systemd unit is active.
// Implementations return true when `systemctl is-active <unit>` exits
// 0; false otherwise (including when systemctl itself isn't on PATH —
// non-systemd hosts can't have a vendor unit running).
type SystemctlIsActive func(unit string) bool

// liveSystemctlIsActive is the production implementation.
func liveSystemctlIsActive(unit string) bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	cmd := exec.Command("systemctl", "is-active", "--quiet", unit)
	return cmd.Run() == nil
}

// DetectVendorDaemon walks the vendor-daemon unit table and returns
// the first match. Returns VendorDaemonNone when no vendor daemon
// is active. Caller is expected to ignore non-matches and fall
// through to the normal install flow.
//
// The check is intentionally narrow: it does not gate on DMI vendor
// match. A System76 daemon running on a non-System76 host (rare,
// usually a misconfiguration) still produces a deferral signal —
// the operator chose to install the daemon and ventd should respect
// that without arguing about whether it "belongs" on the host.
//
// ctx is honoured so a wizard preflight can bound the probe; on
// timeout the function returns VendorDaemonNone (conservative
// default — proceed with normal install rather than mis-detect).
func DetectVendorDaemon(ctx context.Context, isActive SystemctlIsActive) VendorDaemon {
	if isActive == nil {
		isActive = liveSystemctlIsActive
	}
	// Walk the vendors in a stable order so tests are deterministic.
	// Map iteration in Go is unordered; iterate through the slice of
	// keys we declare. Order is alphabetical-ish + System76 first
	// because it's the most common Linux-first OEM in the wild;
	// changing the order breaks the multiple-active tie-break test.
	for _, v := range []VendorDaemon{
		VendorDaemonSystem76,
		VendorDaemonAsusctl,
		VendorDaemonTuxedo,
		VendorDaemonLegion,
		VendorDaemonFramework,
	} {
		select {
		case <-ctx.Done():
			return VendorDaemonNone
		default:
		}
		for _, unit := range vendorDaemonUnits[v] {
			if isActive(unit) {
				return v
			}
		}
	}
	return VendorDaemonNone
}

// DetectNixOS reports whether the running host is NixOS, which
// silently ignores ventd's auto-fix targets under /etc/modprobe.d
// and /etc/modules-load.d in favour of declarative
// configuration.nix entries.
//
// rootFS is the virtual root the probe reads from (production:
// os.DirFS("/"); tests: testing/fstest.MapFS so the probe is
// hermetic). Two signals, either is sufficient:
//
//   - /etc/NIXOS exists (the canonical NixOS marker file)
//   - /etc/os-release ID line is "nixos"
//
// Returning true means ventd's auto-fix endpoints will be no-ops
// from the operator's perspective — the wizard surface should
// route to the docs-only NixOS remediation card instead.
func DetectNixOS(rootFS fs.FS) bool {
	if rootFS == nil {
		rootFS = os.DirFS("/")
	}
	if _, err := fs.Stat(rootFS, "etc/NIXOS"); err == nil {
		return true
	}
	data, err := fs.ReadFile(rootFS, "etc/os-release")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		// `ID=nixos` (without quotes) is the canonical form on NixOS;
		// `ID="nixos"` (quoted) is the alternative shape some distros
		// adopted historically. Match both.
		switch line {
		case "ID=nixos", `ID="nixos"`:
			return true
		}
	}
	return false
}
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
