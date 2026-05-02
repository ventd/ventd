package checks

import (
	"github.com/ventd/ventd/internal/preflight"
)

// DefaultOptions configures the standard install-time check set.
// Callers can copy this and override TargetModule (the OOT module
// being installed — drives in-tree-conflict and stale-DKMS lookups)
// or MaxSupportedKernel (the per-driver ceiling from the catalog
// entry).
type DefaultOptions struct {
	// TargetModule is the OOT module name. Empty defaults to
	// "nct6687" (the most common case among our shipped boards;
	// install.sh overrides per detected hardware).
	TargetModule string
	// MaxSupportedKernel is the version ceiling for the target
	// driver. Empty disables the kernel-too-new check.
	MaxSupportedKernel string
	// ChipName is the human-readable chip identifier used in error
	// detail strings. Empty produces "the driver".
	ChipName string
	// PortAddr is the host:port the daemon will bind on first start.
	// Empty disables the port_held check.
	PortAddr string
	// AppArmorProfilePath is the path to the shipped ventd AppArmor
	// profile. Empty disables the apparmor_parse check.
	AppArmorProfilePath string
}

// Default returns the standard ordered Check slice. Order matters
// because some checks share preconditions (e.g. all SB checks short-
// circuit when SB isn't enforcing). The orchestrator runs every
// Detect even on a no-fix run, so ordering is purely about which
// blocker the operator is asked to fix first.
//
// Sequence:
//
//  1. System-level (container, sudo, lib-modules) — must be clean
//     before any package install can succeed.
//  2. Conflicts (in-tree drivers, stale DKMS, userspace daemons)
//     that would prevent modprobe.
//  3. Build environment (gcc, make, kernel-headers, dkms).
//  4. Secure Boot chain (kmod → mokutil → keypair → enrollment).
//  5. Kernel-too-new + apparmor + port held — last because they're
//     either rare (kernel-too-new) or non-fatal (apparmor warning).
//  6. Concurrent install + apt lock + disk full — fast read-only
//     gates, last because their detail strings are the most
//     timing-dependent.
func Default(opts DefaultOptions) []preflight.Check {
	if opts.TargetModule == "" {
		opts.TargetModule = "nct6687"
	}

	out := []preflight.Check{}
	out = append(out, SystemChecks(DefaultSystemProbes())...)
	cp := DefaultConflictProbes()
	cp.TargetModule = opts.TargetModule
	out = append(out, ConflictChecks(cp)...)
	out = append(out, BuildChecks(DefaultBuildProbes())...)
	out = append(out, SecureBootChecks(DefaultSecureBootProbes())...)
	if opts.MaxSupportedKernel != "" {
		out = append(out, KernelTooNewCheck(KernelTooNewProbes{
			KernelRelease:      DefaultBuildProbes().KernelRelease,
			MaxSupportedKernel: opts.MaxSupportedKernel,
			ChipName:           opts.ChipName,
		}))
	}
	if opts.AppArmorProfilePath != "" {
		out = append(out, AppArmorParseCheck(opts.AppArmorProfilePath, liveRunShell))
	}
	if opts.PortAddr != "" {
		out = append(out, PortHeldCheck(opts.PortAddr))
	}
	return out
}
