package checks

import (
	"context"

	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/preflight"
)

// BuildProbes captures the inputs to the build-environment checks.
// Each Detect is read-only; the AutoFix shells out to the per-distro
// package manager via DistroInfo + cmdRunner.
type BuildProbes struct {
	HasBinary      func(name string) bool
	BuildDirExists func(release string) bool
	KernelRelease  func() string
	Distro         hwmon.DistroInfo
	Run            cmdRunner
}

// DefaultBuildProbes wires the live system.
func DefaultBuildProbes() BuildProbes {
	dp := hwmon.DefaultProbes()
	return BuildProbes{
		HasBinary:      dp.HasBinary,
		BuildDirExists: dp.BuildDirExists,
		KernelRelease:  dp.KernelRelease,
		Distro:         hwmon.DetectDistro(),
		Run:            liveRunShell,
	}
}

// BuildChecks returns the four build-environment Checks: gcc, make,
// kernel-headers, dkms. Each has an AutoFix that installs the
// corresponding package via the per-family dispatch.
func BuildChecks(p BuildProbes) []preflight.Check {
	if p.Run == nil {
		p.Run = liveRunShell
	}
	binCheck := func(name string, pkgFn func() string, prompt string) preflight.Check {
		return preflight.Check{
			Name:     name + "_missing",
			Severity: preflight.SeverityBlocker,
			Detect: func(context.Context) (bool, string) {
				if p.HasBinary(name) {
					return false, ""
				}
				return true, name + " not on PATH"
			},
			Explain: func(string) string {
				return "Required to build the kernel module."
			},
			AutoFix: func(ctx context.Context) error {
				pkg := pkgFn()
				return installPackage(ctx, p.Distro, pkg, p.Run)
			},
			PromptText: prompt,
			DocURL:     "https://github.com/ventd/ventd/wiki/build-tools",
		}
	}

	return []preflight.Check{
		binCheck("gcc",
			func() string { return buildToolsPackage(p.Distro) },
			"Install build tools (gcc + make)?"),
		binCheck("make",
			func() string { return buildToolsPackage(p.Distro) },
			"Install build tools (gcc + make)?"),
		{
			Name:     "kernel_headers_missing",
			Severity: preflight.SeverityBlocker,
			Detect: func(context.Context) (bool, string) {
				release := p.KernelRelease()
				if release == "" {
					return false, ""
				}
				if p.BuildDirExists(release) {
					return false, ""
				}
				return true, "no /lib/modules/" + release + "/build"
			},
			Explain: func(string) string {
				return "Kernel headers for the running kernel are required to compile the OOT module."
			},
			AutoFix: func(ctx context.Context) error {
				release := p.KernelRelease()
				if release == "" {
					return errReleaseUnknown
				}
				pkg := kernelHeadersPackage(p.Distro, release)
				if pkg == "" {
					return errUnknownDistro
				}
				return installPackage(ctx, p.Distro, pkg, p.Run)
			},
			PromptText: "Install kernel headers for the running kernel?",
			DocURL:     "https://github.com/ventd/ventd/wiki/kernel-headers",
		},
		{
			Name:     "dkms_missing",
			Severity: preflight.SeverityBlocker,
			Detect: func(context.Context) (bool, string) {
				if p.HasBinary("dkms") {
					return false, ""
				}
				return true, "dkms not on PATH"
			},
			Explain: func(string) string {
				return "DKMS rebuilds the OOT module across kernel updates."
			},
			AutoFix: func(ctx context.Context) error {
				return installPackage(ctx, p.Distro, "dkms", p.Run)
			},
			PromptText: "Install DKMS?",
			DocURL:     "https://github.com/ventd/ventd/wiki/dkms",
		},
	}
}

// errReleaseUnknown is returned when the AutoFix can't determine the
// running kernel release. With no release we don't know which package
// to install; the operator should re-run with $(uname -r) populated.
var errReleaseUnknown = preflightErr("kernel release unknown — run from a TTY where uname -r is available")

// errUnknownDistro is returned when DistroInfo doesn't map to a known
// family. The orchestrator surfaces it as a docs-only fallback.
var errUnknownDistro = preflightErr("unknown distro family — install the kernel headers manually")

// preflightErr is a tiny error type so AutoFix returns are
// distinguishable from runner errors at log time.
type preflightErr string

func (e preflightErr) Error() string { return string(e) }
