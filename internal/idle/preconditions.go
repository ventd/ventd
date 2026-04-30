package idle

import (
	"os"
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

// checkInContainer returns true when running in a container, detected by
// reading the same files systemd-detect-virt --container reads:
//
//   - /proc/1/cgroup for container-runtime keywords
//     (docker / lxc / kubepods / garden)
//   - /proc/1/environ for the `container=` env var that LXC and
//     systemd-nspawn export to PID 1
//   - /.dockerenv (Docker), /run/.containerenv (Podman),
//     /run/host/container-manager (some sandboxes)
//
// The previous implementation fork-exec'd `systemd-detect-virt --container`
// when the cgroup keyword check missed. The fork briefly let the child
// inherit the daemon's open notify-socket FD, which systemd then logged
// as `Got notification message from PID X, but reception only permitted
// for main PID Y` once per gate tick. Replacing the exec with file reads
// removes the fork entirely.
func checkInContainer(procRoot string) bool {
	path := procRoot
	if path == "" {
		path = "/proc"
	}

	// /proc/1/cgroup for container-runtime keywords.
	if cgroup, err := os.ReadFile(path + "/1/cgroup"); err == nil {
		lower := strings.ToLower(string(cgroup))
		for _, kw := range []string{"docker", "lxc", "kubepods", "garden"} {
			if strings.Contains(lower, kw) {
				return true
			}
		}
	}

	// /proc/1/environ for the `container=` env var. PID 1 in an LXC
	// container or systemd-nspawn slice has `container=lxc` /
	// `container=systemd-nspawn` etc. exported. Entries are NUL-delimited.
	if env, err := os.ReadFile(path + "/1/environ"); err == nil {
		for _, entry := range strings.Split(string(env), "\x00") {
			if strings.HasPrefix(entry, "container=") {
				val := strings.TrimPrefix(entry, "container=")
				if val != "" && val != "none" {
					return true
				}
			}
		}
	}

	// Real-system marker files. Skip when the caller passed an explicit
	// procRoot — that's a test fixture, and stat-ing /.dockerenv etc.
	// on the host would bypass the fixture's hermetic boundary.
	if path == "/proc" {
		for _, marker := range []string{
			"/.dockerenv",
			"/run/.containerenv",
			"/run/host/container-manager",
		} {
			if _, err := os.Stat(marker); err == nil {
				return true
			}
		}
	}

	return false
}

// checkPostResumeWarmup returns true when the system resumed from suspend
// within the last 600 seconds.
//
// Native implementation: read systemd-suspend.service's persistent state
// file at /var/lib/systemd/timers/stamp-systemd-suspend.timer (when
// present) or fall back to mtime of /sys/power/wakeup_count. The
// previous implementation fork-exec'd `systemctl show -p
// ActiveEnterTimestamp`; the fork briefly let the child inherit the
// daemon's open notify-socket FD, which systemd then logged as
// `Got notification message from PID X, but reception only permitted
// for main PID Y` once per gate tick. Replacing the exec with file
// reads removes the fork entirely. The 600s warmup window has
// generous slack — file mtime resolution (1s) is well within it.
func checkPostResumeWarmup() bool {
	resume, ok := lastResumeTime()
	if !ok {
		return false
	}
	return time.Since(resume) < 600*time.Second
}

// lastResumeTime returns the most recent suspend-resume time, or
// (zero, false) when the host hasn't suspended since boot.
//
// Both candidates live on tmpfs and only appear after at least one
// systemd-suspend cycle, so a never-suspended host returns false —
// the same answer the old systemctl-based implementation produced
// (ActiveEnterTimestamp=n/a). The kernel's /sys/power/wakeup_count
// is intentionally NOT used here: its mtime tracks kernel-side wake
// events including initial boot, so it would falsely report
// "warming up from suspend" on a freshly-booted host.
func lastResumeTime() (time.Time, bool) {
	for _, path := range []string{
		"/run/systemd/units/invocation:systemd-suspend.service",
		"/run/systemd/transient/systemd-suspend.service",
	} {
		if fi, err := os.Stat(path); err == nil {
			return fi.ModTime(), true
		}
	}
	return time.Time{}, false
}

