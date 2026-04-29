package idle

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// HardPreconditions is the set of checks that are never skippable (battery,
// container) or skippable with operator override.
type HardPreconditions struct {
	// NeverSkip: items 1–2.
	OnBattery   bool
	InContainer bool
	// Skippable with override: items 3–6.
	StorageMaintenance bool
	BlockedProcess     string // first found process name; "" if none
	BootWarmup         bool
	PostResumeWarmup   bool
}

// Any returns true when any precondition is active.
func (h HardPreconditions) Any() bool {
	return h.OnBattery || h.InContainer || h.StorageMaintenance ||
		h.BlockedProcess != "" || h.BootWarmup || h.PostResumeWarmup
}

// Reason returns the first active precondition Reason.
func (h HardPreconditions) Reason() Reason {
	switch {
	case h.OnBattery:
		return ReasonOnBattery
	case h.InContainer:
		return ReasonInContainer
	case h.StorageMaintenance:
		return ReasonStorageMaintenance
	case h.BlockedProcess != "":
		return ReasonBlockedProcess.WithDetail(h.BlockedProcess)
	case h.BootWarmup:
		return ReasonBootWarmup
	case h.PostResumeWarmup:
		return ReasonPostResumeWarmup
	}
	return ReasonOK
}

// CheckHardPreconditions evaluates all six hard preconditions.
// allowOverride skips items 3–6 per R5 §7.7 (battery and container are
// NEVER skipped, per RULE-IDLE-09).
func CheckHardPreconditions(procRoot, sysRoot string, allowOverride bool) HardPreconditions {
	var h HardPreconditions

	// Item 1: battery — never skippable.
	h.OnBattery = checkOnBattery(sysRoot)

	// Item 2: container — never skippable.
	h.InContainer = checkInContainer(procRoot)

	if allowOverride {
		return h
	}

	// Item 3: storage maintenance.
	sf := captureStructuralFlags(procRoot, sysRoot)
	h.StorageMaintenance = sf.MDRAIDActive || sf.ZFSScrub || sf.BTRFSScrub

	// Item 4: blocked process.
	procs := captureProcesses(procRoot)
	for name := range procs {
		h.BlockedProcess = name
		break
	}

	// Item 5: boot warmup < 600s.
	if uptime, err := uptimeSeconds(procRoot); err == nil && uptime < 600 {
		h.BootWarmup = true
	}

	// Item 6: post-resume warmup < 600s.
	h.PostResumeWarmup = checkPostResumeWarmup()

	return h
}

// checkOnBattery returns true when AC/online is absent or "0", or any
// battery status file reads "Discharging".
func checkOnBattery(sysRoot string) bool {
	root := sysRoot
	if root == "" {
		root = "/sys"
	}

	// Check AC online status.
	acPattern := root + "/class/power_supply/AC*/online"
	acMatches, _ := filepath.Glob(acPattern)
	for _, p := range acMatches {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) == "0" {
			return true
		}
	}
	// If no AC online file exists, check for battery discharging.
	batPattern := root + "/class/power_supply/BAT*/status"
	batMatches, _ := filepath.Glob(batPattern)
	for _, p := range batMatches {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) == "Discharging" {
			return true
		}
	}
	return false
}

// checkInContainer returns true when running in a container detected via
// /proc/1/cgroup keywords and (when using the real /proc) systemd-detect-virt.
func checkInContainer(procRoot string) bool {
	path := procRoot
	if path == "" {
		path = "/proc"
	}

	// Check /proc/1/cgroup for container runtime keywords.
	cgroup, err := os.ReadFile(path + "/1/cgroup")
	if err == nil {
		lower := strings.ToLower(string(cgroup))
		for _, kw := range []string{"docker", "lxc", "kubepods", "garden"} {
			if strings.Contains(lower, kw) {
				return true
			}
		}
	}

	// Only run systemd-detect-virt when operating on the real /proc (not in tests
	// using a synthetic procRoot temp dir). An explicit temp-dir procRoot means the
	// caller is providing a hermetic fixture; real exec calls would bypass it.
	if path == "/proc" {
		out, err := exec.Command("systemd-detect-virt", "--container").Output()
		if err == nil {
			result := strings.TrimSpace(string(out))
			if result != "" && result != "none" {
				return true
			}
		}
	}

	return false
}

// checkPostResumeWarmup returns true when the system resumed from suspend
// within the last 600 seconds.
func checkPostResumeWarmup() bool {
	out, err := exec.Command("systemctl", "show", "systemd-suspend.service",
		"-p", "ActiveEnterTimestamp").Output()
	if err != nil {
		return false
	}
	s := strings.TrimPrefix(strings.TrimSpace(string(out)), "ActiveEnterTimestamp=")
	if s == "" || s == "n/a" {
		return false
	}
	// Parse timestamp — systemd uses a locale-dependent format; attempt RFC3339
	// and common systemd formats.
	for _, layout := range []string{
		time.RFC3339,
		"Mon 2006-01-02 15:04:05 MST",
		"Mon 2006-01-02 15:04:05 UTC",
	} {
		t, err := time.Parse(layout, s)
		if err == nil {
			return time.Since(t) < 600*time.Second
		}
	}
	return false
}
