package checks

import (
	"context"
	"os"
	"strings"

	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/preflight"
)

// SystemProbes captures host-level read-only probes for the checks in
// this file. None of these have an AutoFix — they all surface as
// docs-only blockers because the resolution is operator action
// (reboot to host, exit container, etc.).
type SystemProbes struct {
	IsContainerised        func() bool
	HaveRootOrPasswordless func() bool
	LibModulesWritable     func(release string) bool
	KernelRelease          func() string
}

// DefaultSystemProbes wires the live system via hwmon's existing live
// implementations.
func DefaultSystemProbes() SystemProbes {
	dp := hwmon.DefaultProbes()
	return SystemProbes{
		IsContainerised:        dp.IsContainerised,
		HaveRootOrPasswordless: dp.HaveRootOrPasswordlessSudo,
		LibModulesWritable:     dp.LibModulesWritable,
		KernelRelease:          dp.KernelRelease,
	}
}

// SystemChecks returns the host-environment Checks. None auto-fix —
// each blocker requires the operator to change the environment
// (move out of container, configure sudo, switch to a writable distro)
// before re-running.
func SystemChecks(p SystemProbes) []preflight.Check {
	return []preflight.Check{
		{
			Name:     "containerised_environment",
			Severity: preflight.SeverityBlocker,
			Detect: func(context.Context) (bool, string) {
				if p.IsContainerised() {
					return true, "container runtime detected"
				}
				return false, ""
			},
			Explain: func(string) string {
				return "ventd cannot control fans from inside a container. Run on the host."
			},
			DocURL: "https://github.com/ventd/ventd/wiki/containers",
		},
		{
			Name:     "no_sudo_no_root",
			Severity: preflight.SeverityBlocker,
			Detect: func(context.Context) (bool, string) {
				if !p.HaveRootOrPasswordless() {
					return true, "ventd is not root and `sudo -n true` failed"
				}
				return false, ""
			},
			Explain: func(string) string {
				return "Driver install requires root or passwordless sudo. Configure sudoers or run as root."
			},
			DocURL: "https://github.com/ventd/ventd/wiki/sudo",
		},
		{
			Name:     "lib_modules_read_only",
			Severity: preflight.SeverityBlocker,
			Detect: func(context.Context) (bool, string) {
				release := p.KernelRelease()
				if release == "" {
					return false, "(no kernel release)"
				}
				if p.LibModulesWritable(release) {
					return false, ""
				}
				return true, "/lib/modules/" + release + "/extra is not writable"
			},
			Explain: func(string) string {
				return "Immutable rootfs (Silverblue, NixOS, Ubuntu Core) — install via the distro's system-modification path."
			},
			DocURL: "https://github.com/ventd/ventd/wiki/immutable-distros",
		},
	}
}

// KernelTooNewProbes are the inputs to the kernel-version-ceiling
// check. The check only triggers when MaxSupportedKernel is non-empty
// — for a synthetic preflight invocation (the legacy
// `--preflight-check` path) callers may pass empty to skip.
type KernelTooNewProbes struct {
	KernelRelease      func() string
	MaxSupportedKernel string
	ChipName           string
}

// KernelTooNewCheck returns the docs-only "kernel newer than driver"
// blocker. There is no AutoFix — the operator either downgrades the
// kernel or waits for a driver update upstream.
func KernelTooNewCheck(p KernelTooNewProbes) preflight.Check {
	return preflight.Check{
		Name:     "kernel_too_new",
		Severity: preflight.SeverityBlocker,
		Detect: func(context.Context) (bool, string) {
			if p.MaxSupportedKernel == "" {
				return false, ""
			}
			release := p.KernelRelease()
			if release == "" || !kernelAbove(release, p.MaxSupportedKernel) {
				return false, ""
			}
			return true, "running " + release + " > " + p.MaxSupportedKernel + " (" + p.ChipName + " ceiling)"
		},
		Explain: func(detail string) string {
			return "Kernel is newer than the driver's known-good ceiling. Downgrade the kernel or wait for a driver update."
		},
		DocURL: "https://github.com/ventd/ventd/wiki/kernel-ceiling",
	}
}

// kernelAbove compares dotted-number kernel versions (5.15.0 > 5.14.9).
// Returns false when either side fails to parse — non-numeric prefix
// or empty strings.
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
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				return nil
			}
			n = n*10 + int(c-'0')
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// PortHeldCheck exposes the legacy install.sh check (port 9999 bound
// at install time) as an orchestrator Check. A bound port is a
// docs-only blocker — the operator either stops the conflicting
// service or edits ventd's listen address before retry.
//
// `addr` is the host:port pair that ventd will bind on first start;
// install.sh passes the resolved value from the daemon's config so
// non-default listens are checked correctly.
func PortHeldCheck(addr string) preflight.Check {
	return preflight.Check{
		Name:     "port_held",
		Severity: preflight.SeverityBlocker,
		Detect: func(context.Context) (bool, string) {
			if isPortFree(addr) {
				return false, ""
			}
			return true, addr + " is bound by another process"
		},
		Explain: func(detail string) string {
			return "ventd defaults to :9999. Stop the conflicting service or change ventd's listen address in /etc/ventd/config.yaml."
		},
		DocURL: "https://github.com/ventd/ventd/wiki/port-conflict",
	}
}

// isPortFree returns true when nothing is listening on addr. The
// check uses a Listen attempt rather than ss/netstat parsing for
// portability across distros without ss installed.
//
// The portFreeFn indirection lets tests stub the system call without
// holding a real socket open during -race runs.
func isPortFree(addr string) bool {
	if addr == "" {
		return true
	}
	return portFreeFn(addr)
}

// portFreeFn is the live implementation. Tests override it via the
// portFreeFn variable, restoring it via t.Cleanup.
var portFreeFn = livePortFree

// AppArmorParseCheck returns the warn-level apparmor profile parse
// gate. Failure isn't fatal — ventd works without apparmor — but the
// operator should know if their distro is rejecting our shipped
// profile so we can ship a per-distro variant.
func AppArmorParseCheck(profilePath string, runner cmdRunner) preflight.Check {
	if runner == nil {
		runner = liveRunShell
	}
	return preflight.Check{
		Name:     "apparmor_profile_parse_failed",
		Severity: preflight.SeverityWarning,
		Detect: func(ctx context.Context) (bool, string) {
			if _, err := os.Stat(profilePath); err != nil {
				return false, "" // nothing shipped; not applicable
			}
			if !commandExists("apparmor_parser") {
				return false, ""
			}
			if err := runner(ctx, "apparmor_parser --replace --skip-cache --skip-kernel-load "+profilePath); err != nil {
				return true, "apparmor_parser refused profile: " + err.Error()
			}
			return false, ""
		},
		Explain: func(string) string {
			return "AppArmor refused our shipped profile. ventd will run unconfined; please file an issue with your distro."
		},
		DocURL: "https://github.com/ventd/ventd/wiki/apparmor",
	}
}

// commandExists is a tiny LookPath wrapper kept here so each check
// file can use it without re-importing exec.
func commandExists(name string) bool {
	return liveHasBinary(name)
}
