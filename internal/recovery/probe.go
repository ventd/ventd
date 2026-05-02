package recovery

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"strings"
)

// VendorDaemon names the OEM-shipped fan-control daemon detected on
// the host. Empty when no vendor daemon is active. Linux-first OEM
// laptop vendors (System76, Tuxedo Computers, ASUS ROG, Slimbook)
// ship working fan daemons that ventd should defer to in monitor-
// only mode rather than fight for control. R28 Agent G's #1
// architectural finding.
type VendorDaemon string

const (
	VendorDaemonNone        VendorDaemon = ""
	VendorDaemonSystem76    VendorDaemon = "system76-power"
	VendorDaemonAsusctl     VendorDaemon = "asusctl"
	VendorDaemonTuxedo      VendorDaemon = "tccd"
	VendorDaemonSlimbook    VendorDaemon = "slimbookbattery"
)

// vendorDaemonUnits maps each vendor daemon to the systemd unit name
// to query via `systemctl is-active`. Multiple unit aliases per
// vendor are checked because distros sometimes rename (asusd vs
// asusctl on some Arch packagings; tccd vs tuxedofancontrol).
var vendorDaemonUnits = map[VendorDaemon][]string{
	VendorDaemonSystem76: {"system76-power.service", "system76-scheduler.service"},
	VendorDaemonAsusctl:  {"asusd.service", "asusctl.service"},
	VendorDaemonTuxedo:   {"tccd.service", "tuxedofancontrol.service"},
	VendorDaemonSlimbook: {"slimbookbattery.service"},
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
	// Map iteration in Go is unordered; iterate through the slice
	// of keys we declare.
	for _, v := range []VendorDaemon{
		VendorDaemonSystem76,
		VendorDaemonAsusctl,
		VendorDaemonTuxedo,
		VendorDaemonSlimbook,
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
