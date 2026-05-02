package checks

import (
	"context"
	"strings"

	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/preflight"
)

// ConflictProbes captures the inputs to the in-tree-driver / stale-
// DKMS / userspace-conflict / wizard-lock checks. Each AutoFix
// performs a small system mutation (modprobe -r, dkms remove,
// systemctl stop) so they are gated behind the orchestrator's Y/N
// prompt.
type ConflictProbes struct {
	// InTreeDriverConflict returns the loaded in-tree module that
	// claims the same hardware as `target`, and ok=true. Empty
	// + false means no conflict.
	InTreeDriverConflict func(target string) (string, bool)
	// StaleDKMSState reports whether DKMS already tracks the target
	// module under any version.
	StaleDKMSState func(module string) bool
	// AnotherWizardRunning reports whether a sibling wizard PID is
	// alive. The lock writer in internal/setup/lock.go is canonical.
	AnotherWizardRunning func() bool
	// UserspaceFanDaemon returns the systemd unit name of an active
	// fancontrol/thinkfan/etc., or "". Multiple active daemons
	// produce a comma-separated list — all must be stopped.
	UserspaceFanDaemon func() string
	// AptLockHeld reports whether dpkg's frontend or download lock
	// is currently held by another process.
	AptLockHeld func() bool
	// DiskFreeBytes returns the free byte count for path; an error
	// means the path doesn't exist (treated as skip, not fail).
	DiskFreeBytes func(path string) (uint64, error)
	// TargetModule is the OOT module name the install wants — drives
	// the in-tree-conflict and stale-DKMS lookups.
	TargetModule string
	// BlacklistDropInPath is the modprobe drop-in path used by the
	// in-tree conflict AutoFix to write a permanent blacklist line.
	BlacklistDropInPath string
	// Run is the shell command runner.
	Run cmdRunner
}

// DefaultConflictProbes wires the live system. Callers that need to
// override TargetModule/BlacklistDropInPath should copy this and set
// the field after the call.
func DefaultConflictProbes() ConflictProbes {
	dp := hwmon.DefaultProbes()
	return ConflictProbes{
		InTreeDriverConflict: dp.InTreeDriverConflict,
		StaleDKMSState:       dp.StaleDKMSState,
		AnotherWizardRunning: dp.AnotherWizardRunning,
		UserspaceFanDaemon:   liveUserspaceFanDaemon,
		AptLockHeld:          dp.AptLockHeld,
		DiskFreeBytes:        dp.DiskFreeBytes,
		TargetModule:         "nct6687",
		BlacklistDropInPath:  hwmon.DetectDistro().BlacklistDropInPath(),
		Run:                  liveRunShell,
	}
}

// MinFreeBytes mirrors hwmon.MinFreeBytes for the disk-space check.
const MinFreeBytes uint64 = 256 * 1024 * 1024

// diskCheckPaths is the install-critical list — same as
// hwmon.diskCheckPaths but re-declared because that var is package-
// private to hwmon. A future refactor could merge them.
var diskCheckPaths = []string{"/lib/modules", "/usr/src", "/var/cache", "/boot"}

// userspaceFanDaemons is the canonical list of daemons that compete
// with ventd for fan control. Ordering matters only for the detail
// string — the AutoFix loops over the full set anyway.
var userspaceFanDaemons = []string{"fancontrol", "thinkfan", "afancontrol", "i8kmon"}

// ConflictChecks returns the conflict / preconditions Checks. None
// are warnings — every one is a blocker because each leaves the
// install in a state where modprobe will fail or DKMS will reject.
func ConflictChecks(p ConflictProbes) []preflight.Check {
	if p.Run == nil {
		p.Run = liveRunShell
	}
	return []preflight.Check{
		{
			Name:     "in_tree_driver_conflict",
			Severity: preflight.SeverityBlocker,
			Detect: func(context.Context) (bool, string) {
				conflict, ok := p.InTreeDriverConflict(p.TargetModule)
				if !ok {
					return false, ""
				}
				return true, "in-tree " + conflict + " is loaded; conflicts with " + p.TargetModule
			},
			Explain: func(string) string {
				return "Unbind the in-tree module and add a permanent blacklist drop-in."
			},
			AutoFix: func(ctx context.Context) error {
				conflict, ok := p.InTreeDriverConflict(p.TargetModule)
				if !ok {
					return nil
				}
				if err := p.Run(ctx, "modprobe -r "+conflict); err != nil {
					return err
				}
				return p.Run(ctx, "echo 'blacklist "+conflict+"' >> "+p.BlacklistDropInPath)
			},
			PromptText: "Unbind in-tree driver and write blacklist drop-in?",
			DocURL:     "https://github.com/ventd/ventd/wiki/in-tree-conflict",
		},
		{
			Name:     "stale_dkms_state",
			Severity: preflight.SeverityBlocker,
			Detect: func(context.Context) (bool, string) {
				if p.StaleDKMSState(p.TargetModule) {
					return true, "DKMS tracks " + p.TargetModule + " under a previous version"
				}
				return false, ""
			},
			Explain: func(string) string {
				return "Run `dkms remove --all` to clear stale state before installing the current version."
			},
			AutoFix: func(ctx context.Context) error {
				return p.Run(ctx, "dkms remove --all "+p.TargetModule)
			},
			PromptText: "Clear stale DKMS state?",
			DocURL:     "https://github.com/ventd/ventd/wiki/dkms",
		},
		{
			Name:     "userspace_fan_daemon_active",
			Severity: preflight.SeverityBlocker,
			Detect: func(context.Context) (bool, string) {
				active := p.UserspaceFanDaemon()
				if active == "" {
					return false, ""
				}
				return true, "active competing daemons: " + active
			},
			Explain: func(detail string) string {
				return "fancontrol/thinkfan/etc. write to the same hwmon files. Stop them so ventd can take control."
			},
			AutoFix: func(ctx context.Context) error {
				active := p.UserspaceFanDaemon()
				for _, name := range strings.Split(active, ",") {
					name = strings.TrimSpace(name)
					if name == "" {
						continue
					}
					if err := p.Run(ctx, "systemctl disable --now "+name); err != nil {
						return err
					}
				}
				return nil
			},
			PromptText: "Stop and disable competing fan daemons?",
			DocURL:     "https://github.com/ventd/ventd/wiki/userspace-conflict",
		},
		{
			Name:     "apt_lock_held",
			Severity: preflight.SeverityBlocker,
			Detect: func(context.Context) (bool, string) {
				if p.AptLockHeld() {
					return true, "another package manager is running"
				}
				return false, ""
			},
			Explain: func(string) string {
				return "Wait for the other package manager (apt/dpkg) to finish, then re-run the install."
			},
			DocURL: "https://github.com/ventd/ventd/wiki/apt-lock",
		},
		{
			Name:     "disk_full",
			Severity: preflight.SeverityBlocker,
			Detect: func(context.Context) (bool, string) {
				for _, path := range diskCheckPaths {
					free, err := p.DiskFreeBytes(path)
					if err != nil {
						continue // path doesn't exist; skip
					}
					if free < MinFreeBytes {
						return true, path + " has only " + humanBytes(free) + " free (need " + humanBytes(MinFreeBytes) + ")"
					}
				}
				return false, ""
			},
			Explain: func(detail string) string {
				return "Install needs ~256 MiB free per critical path. Free up space by clearing the package cache."
			},
			DocURL: "https://github.com/ventd/ventd/wiki/disk-full",
		},
		{
			Name:     "concurrent_install",
			Severity: preflight.SeverityBlocker,
			Detect: func(context.Context) (bool, string) {
				if p.AnotherWizardRunning() {
					return true, "another wizard PID is alive"
				}
				return false, ""
			},
			Explain: func(string) string {
				return "Wait for the other wizard run to finish, or take over via the web UI."
			},
			DocURL: "https://github.com/ventd/ventd/wiki/concurrent-wizard",
		},
	}
}

// humanBytes renders a byte count in MiB/GiB. Re-implemented here
// because hwmon.humanBytes is package-private; trivial enough that
// duplicating beats exposing a hwmon export.
func humanBytes(n uint64) string {
	const (
		mi = 1024 * 1024
		gi = 1024 * mi
	)
	switch {
	case n >= gi:
		return formatFloat1(float64(n)/float64(gi)) + " GiB"
	case n >= mi:
		return formatFloat1(float64(n)/float64(mi)) + " MiB"
	}
	return formatU64(n) + " B"
}

func formatFloat1(f float64) string {
	x := int64(f * 10)
	whole := x / 10
	frac := x % 10
	return formatI64(whole) + "." + formatI64(frac)
}

func formatI64(n int64) string {
	if n < 0 {
		return "-" + formatU64(uint64(-n))
	}
	return formatU64(uint64(n))
}

func formatU64(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
