// Package checks defines the install-time precondition checks the
// orchestrator runs. Each Check pairs a Detect (read-only probe) with
// an AutoFix (shell-out remediation) and is wired into the default set
// by Default().
//
// AutoFix functions in this file dispatch to the running distro's
// package manager via internal/hwmon.DetectDistro. The dispatch table
// covers the families ventd ships against (debian/ubuntu, fedora/RHEL,
// arch/manjaro, suse, alpine); unknown families fall through to a
// docs-only return that leaves the operator with manual instructions.
package checks

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/ventd/ventd/internal/hwmon"
)

// runShell runs cmd via /bin/sh -c and returns the combined stdout +
// stderr on error, with the exit message wrapped. The orchestrator's
// AutoFix surface uses this via the helper functions below; tests
// substitute a fake implementation.
type cmdRunner func(ctx context.Context, command string) error

func liveRunShell(ctx context.Context, command string) error {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w (output: %s)", strings.SplitN(command, " ", 2)[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

// installPackage builds the distro-appropriate "install <pkg>" command
// and invokes runner. Returns an error when the family is unknown so
// the orchestrator surfaces a docs-only guidance message rather than
// silently no-opping.
func installPackage(ctx context.Context, d hwmon.DistroInfo, pkg string, runner cmdRunner) error {
	cmd, ok := installCommand(d, pkg)
	if !ok {
		return fmt.Errorf("unknown distro family; install %q manually for your system", pkg)
	}
	return runner(ctx, cmd)
}

// installCommand returns the per-family `install <pkg>` shell command.
// Splitting it from installPackage lets tests assert on the exact
// command string without invoking a real runner.
func installCommand(d hwmon.DistroInfo, pkg string) (string, bool) {
	// Debian/Ubuntu: -y for non-interactive, --no-install-recommends
	// keeps the install set minimal (kernel-headers in particular
	// pulls a lot otherwise).
	switch familyOf(d) {
	case "debian":
		return "DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends " + pkg, true
	case "fedora":
		return "dnf install -y " + pkg, true
	case "arch":
		return "pacman -S --needed --noconfirm " + pkg, true
	case "suse":
		return "zypper install -y " + pkg, true
	case "alpine":
		return "apk add " + pkg, true
	}
	return "", false
}

// familyOf re-implements the unexported familyKey on DistroInfo so we
// can reach it from this package without exporting it from hwmon. The
// logic mirrors hwmon.DistroInfo.familyKey() exactly; a divergence
// would surface as a per-family install failure on the affected
// distro, but since both functions read the same os-release fields
// they are kept in sync by review.
func familyOf(d hwmon.DistroInfo) string {
	ids := strings.ToLower(d.ID + " " + d.IDLike)
	switch {
	case strings.Contains(ids, "debian"), strings.Contains(ids, "ubuntu"):
		return "debian"
	case strings.Contains(ids, "fedora"), strings.Contains(ids, "rhel"), strings.Contains(ids, "centos"):
		return "fedora"
	case strings.Contains(ids, "arch"), strings.Contains(ids, "manjaro"):
		return "arch"
	case strings.Contains(ids, "suse"):
		return "suse"
	case strings.Contains(ids, "alpine"):
		return "alpine"
	}
	return ""
}

// kernelHeadersPackage returns the per-family package name for the
// running kernel's headers. Debian/Ubuntu encode the release string in
// the package name, Arch uses linux-headers (rolling), Fedora uses
// kernel-devel.
func kernelHeadersPackage(d hwmon.DistroInfo, release string) string {
	switch familyOf(d) {
	case "debian":
		return "linux-headers-" + release
	case "fedora":
		return "kernel-devel-" + release
	case "arch":
		return "linux-headers"
	case "suse":
		return "kernel-default-devel"
	case "alpine":
		return "linux-lts-dev"
	}
	return ""
}

// buildToolsPackage returns the umbrella build-tools meta-package for
// each family. Some (Debian's build-essential, Arch's base-devel)
// install gcc + make + binutils + libc-headers in one shot; Fedora
// needs gcc + make explicitly.
func buildToolsPackage(d hwmon.DistroInfo) string {
	switch familyOf(d) {
	case "debian":
		return "build-essential"
	case "fedora":
		return "gcc make"
	case "arch":
		return "base-devel"
	case "suse":
		return "gcc make"
	case "alpine":
		return "build-base"
	}
	return ""
}
