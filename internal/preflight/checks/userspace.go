package checks

import (
	"os/exec"
	"strings"
)

// liveUserspaceFanDaemon walks the canonical competing-daemon list and
// reports any unit `systemctl is-active` reports as active. Multiple
// active units join with ", ".
//
// The check is best-effort: a host without systemd (Alpine without
// OpenRC-systemd-shim, etc.) returns "" because every is-active call
// errors. That degrades to "no conflict" — the install proceeds and
// any actual conflict surfaces at modprobe time as a separate
// blocker. We don't try to detect non-systemd init systems here
// because the population of users running fancontrol on non-systemd
// hosts is vanishingly small.
func liveUserspaceFanDaemon() string {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return ""
	}
	var active []string
	for _, name := range userspaceFanDaemons {
		cmd := exec.Command("systemctl", "is-active", "--quiet", name)
		if err := cmd.Run(); err == nil {
			active = append(active, name)
		}
	}
	return strings.Join(active, ", ")
}
